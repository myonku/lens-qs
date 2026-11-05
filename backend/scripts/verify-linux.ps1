param([string]$Image='lens-qs:g2-verify', [switch]$SkipPerformance)
$ErrorActionPreference='Stop'
$suffix=[guid]::NewGuid().ToString('N').Substring(0,10)
$network="lens-g2-$suffix"
$postgres="lens-g2-pg-$suffix"
$password=[guid]::NewGuid().ToString('N')
$cache='lens-g2-go-cache:/root/.cache/go-build'
try {
    & docker network create $network | Out-Null
    & docker run --rm -d --name $postgres --network $network --network-alias postgres -e "POSTGRES_PASSWORD=$password" -e POSTGRES_DB=lens_g2 postgres:17 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Disposable PostgreSQL fixture failed' }
    $ready=$false
    for ($attempt=0;$attempt -lt 30;$attempt++) {
        & docker exec $postgres pg_isready -U postgres -d lens_g2 *> $null
        if ($LASTEXITCODE -eq 0) { $ready=$true;break }
        Start-Sleep -Milliseconds 500
    }
    if (-not $ready) { throw 'PostgreSQL fixture did not become ready' }
    & docker run --rm -v $cache $Image sh -c 'go test -race ./... -count=1 && go vet ./... && go build ./...'
    if ($LASTEXITCODE -ne 0) { throw 'Linux SQLite verification failed' }
    $pgOptions=@('--rm','--network',$network,'-v',$cache,'-v','/var/run/docker.sock:/var/run/docker.sock','-e',"LENS_TEST_POSTGRES_DSN=postgres://postgres:${password}@postgres:5432/lens_g2?sslmode=disable",'-e',"LENS_TEST_PG_DOCKER_CONTAINER=$postgres")
    & docker run @pgOptions $Image sh -c 'go test -race ./... -count=1 && go vet ./... && go build ./...'
    if ($LASTEXITCODE -ne 0) { throw 'Linux PostgreSQL verification failed' }
    if (-not $SkipPerformance) {
        & docker run --rm -v $cache -e LENS_G2_PERF=1 $Image go test ./internal/server -run TestG2AnnualScalePerformance -count=1 -v
        if ($LASTEXITCODE -ne 0) { throw 'Linux SQLite performance failed' }
        & docker run @pgOptions -e LENS_G2_PERF=1 $Image go test ./internal/server -run TestG2AnnualScalePerformance -count=1 -v
        if ($LASTEXITCODE -ne 0) { throw 'Linux PostgreSQL performance failed' }
    }
    Write-Output 'PASS: Linux SQLite/PostgreSQL race, vet, build and requested performance checks'
} finally {
    & docker stop $postgres 2>$null | Out-Null
    & docker network rm $network 2>$null | Out-Null
}

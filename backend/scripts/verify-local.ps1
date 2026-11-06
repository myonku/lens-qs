param([switch]$WithPostgres, [switch]$SkipPerformance)
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
$previousLocation = Get-Location
$testKeys = @('LENS_TEST_POSTGRES_DSN', 'LENS_TEST_PG_DOCKER_CONTAINER', 'LENS_G2_PERF')
$previousEnv = @{}
foreach ($key in $testKeys) { $previousEnv[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
$postgres = $null
function Invoke-GoCheck([string[]]$GoArgs) {
    & go @GoArgs
    if ($LASTEXITCODE -ne 0) { throw "go $($GoArgs -join ' ') failed" }
}
try {
    Set-Location -LiteralPath $taskRoot
    # Tests own temporary DB files/schemas; never use the application's DB_DSN.
    foreach ($key in $testKeys) { [Environment]::SetEnvironmentVariable($key, $null, 'Process') }
    Invoke-GoCheck @('test', '-race', './...', '-count=1')
    Invoke-GoCheck @('vet', './...')
    Invoke-GoCheck @('build', './...')
    if (-not $SkipPerformance) {
        $env:LENS_G2_PERF = '1'
        Invoke-GoCheck @('test', './internal/server', '-run', '^TestG2AnnualScalePerformance$', '-count=1', '-v')
        Remove-Item Env:LENS_G2_PERF
    }
    if ($WithPostgres) {
        $postgres = 'lens-local-test-' + [guid]::NewGuid().ToString('N').Substring(0, 10)
        $password = [guid]::NewGuid().ToString('N')
        & docker run --rm -d --name $postgres -p '127.0.0.1::5432' -e "POSTGRES_PASSWORD=$password" -e POSTGRES_DB=lens_test postgres:17 | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL test dependency failed to start' }
        $ready = $false
        for ($attempt = 0; $attempt -lt 60; $attempt++) {
            & docker exec $postgres pg_isready -U postgres -d lens_test *> $null
            if ($LASTEXITCODE -eq 0) { $ready = $true; break }
            Start-Sleep -Milliseconds 500
        }
        if (-not $ready) { throw 'PostgreSQL test dependency is not ready' }
        $binding = & docker port $postgres 5432/tcp
        if ($LASTEXITCODE -ne 0 -or $binding -notmatch '^127\.0\.0\.1:(\d+)$') { throw 'Unexpected PostgreSQL port binding' }
        $env:LENS_TEST_POSTGRES_DSN = "postgres://postgres:${password}@127.0.0.1:$($Matches[1])/lens_test?sslmode=disable"
        $env:LENS_TEST_PG_DOCKER_CONTAINER = $postgres
        Invoke-GoCheck @('test', '-race', './...', '-count=1')
        if (-not $SkipPerformance) {
            $env:LENS_G2_PERF = '1'
            Invoke-GoCheck @('test', './internal/server', '-run', '^TestG2AnnualScalePerformance$', '-count=1', '-v')
        }
    }
    Write-Output 'PASS: native local verification completed'
} finally {
    if ($postgres) { & docker stop $postgres 2>$null | Out-Null }
    foreach ($key in $testKeys) { [Environment]::SetEnvironmentVariable($key, $previousEnv[$key], 'Process') }
    Set-Location -LiteralPath $previousLocation
}

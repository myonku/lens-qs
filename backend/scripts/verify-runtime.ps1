param([string]$Image = 'lens-qs:g2-development')
$ErrorActionPreference = 'Stop'
$suffix = [guid]::NewGuid().ToString('N').Substring(0, 10)
$volume = "lens-g2-runtime-$suffix"
$api = "lens-g2-api-$suffix"
$password = [guid]::NewGuid().ToString('N')
$options = @('--rm', '-v', "${volume}:/app/data", '-e', 'APP_ENV=dev', '-e', 'DB_DRIVER=sqlite', '-e', 'DB_DSN=/app/data/g2.db', '-e', 'CORS_ORIGINS=https://allowed.example')
function Inventory {
    $raw = & docker run @options $Image /app/lens-qs-migrate -status
    if ($LASTEXITCODE -ne 0) { throw 'Runtime inventory failed' }
    return ($raw | ConvertFrom-Json)
}
function Start-VerifiedApi {
    & docker run @options -d --name $api -p '127.0.0.1::8080' $Image | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'API container failed' }
    $port = (& docker port $api 8080/tcp).Split(':')[-1].Trim()
    $script:base = "http://127.0.0.1:$port"
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        try { $health = Invoke-RestMethod "$base/healthz"; if ($health.status -eq 'ok') { return } } catch { }
        Start-Sleep -Milliseconds 300
    }
    throw 'API did not become ready'
}
function Call-Api([string]$Method, [string]$Path, $Body) {
    $headers = @{Authorization="Bearer $script:token"; 'X-Idempotency-Key'=[guid]::NewGuid().ToString('N')}
    if ($null -eq $Body) { return (Invoke-RestMethod -Method $Method -Uri "$base/api/v2$Path" -Headers $headers).data }
    return (Invoke-RestMethod -Method $Method -Uri "$base/api/v2$Path" -Headers $headers -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Depth 30)).data
}
try {
    & docker volume create $volume | Out-Null
    & docker run @options $Image /app/lens-qs-migrate -initialize | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Initialization failed' }
    & docker run @options $Image /app/lens-qs-seed | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Seed failed' }
    & docker run @options $Image /app/lens-qs-createuser -username runtime-review -password $password -admin | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Explicit account creation failed' }
    Start-VerifiedApi
    $login = Invoke-RestMethod -Method POST -Uri "$base/api/v2/auth/login" -ContentType 'application/json' -Body (@{username='runtime-review';password=$password} | ConvertTo-Json)
    $script:token = $login.data.token
    $lot = Call-Api POST /product-lots @{shared_form_no='RUNTIME';sample_identity='X'}
    $operations = Call-Api GET '/dict/items?category=inspection_operation' $null
    $group = Call-Api POST "/product-lots/$($lot.id)/operation-groups" @{operation_id=$operations.items[0].id}
    $submission = Call-Api POST /submissions @{operation_group_id=$group.id;content=@{paper_unit_text='片';counts=@{total_qty=100;qualified_qty=96;defect_qty=4}}}
    $summary = Call-Api GET '/stats/summary' $null
    if ($summary.total.counts.total_qty.value -ne 100 -or $summary.total.pass_rate -ne 96) { throw 'Runtime aggregate mismatch' }
    $report = Call-Api GET "/reports/product-lots/$($lot.id)" $null
    if ($report.included_revision_ids[0] -ne $submission.current_revision.id) { throw 'Runtime report mismatch' }
    $cancelled = Call-Api POST "/submissions/$($submission.submission.id)/cancel" @{expected_version=1;reason='runtime verification'}
    if ($cancelled.submission.state -ne 'CANCELLED') { throw 'Runtime cancel failed' }
    & docker stop --time 12 $api | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Graceful stop failed' }
    $before = Inventory
    Start-VerifiedApi
    & docker stop --time 12 $api | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Second graceful stop failed' }
    $after = Inventory
    if ($before.fingerprint -ne $after.fingerprint) { throw 'Restart changed persisted data' }
    if ($after.version -ne 8) { throw 'Unexpected schema version' }
    Write-Output 'PASS: image HTTP lifecycle, history, cancellation, graceful stop and restart preservation (schema 0008)'
} finally {
    & docker rm -f $api 2>$null | Out-Null
    & docker volume rm $volume | Out-Null
}

param([int]$Port=0,[switch]$NoWait)
$ErrorActionPreference='Stop'
$taskRoot=Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
if ($Port -gt 0) {$env:APP_ADDR=":$Port"}
New-Item -ItemType Directory -Force -Path (Join-Path $taskRoot 'bin') | Out-Null
go build -o bin/lens-qs-api.exe ./cmd/api
if ($LASTEXITCODE -ne 0) {throw 'Build failed'}
$taskExe=Join-Path $taskRoot 'bin/lens-qs-api.exe'
if ($NoWait) {Start-Process -FilePath $taskExe -WorkingDirectory $taskRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $taskRoot 'bin/api.log') -RedirectStandardError (Join-Path $taskRoot 'bin/api.err')} else {& $taskExe}

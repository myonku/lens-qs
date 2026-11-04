# 本地开发一键启动：构建 → 迁移 → 种子 → 建管理员 → 起服务 → 健康检查
#
# 用法：
#   pwsh -File scripts\dev.ps1                # 前台运行（Ctrl+C 停止，能看实时日志）
#   pwsh -File scripts\dev.ps1 -NoWait        # 后台运行并做健康检查
#   pwsh -File scripts\dev.ps1 -Reset         # 删库重建（会清掉本地数据，重新引导管理员）
#   pwsh -File scripts\dev.ps1 -Port 8081     # 换端口
#
# 账号：管理员由 backend/.env 的 BOOTSTRAP_ADMIN_USER / BOOTSTRAP_ADMIN_PASS 决定，
#       **仅在数据库里一个用户都没有时**创建（所以首次启动或 -Reset 之后才会生效）。
param(
    [switch]$Reset,
    [int]$Port = 0,
    [switch]$NoSeed,
    [switch]$NoWait
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# 读取 .env（未设置的真实环境变量优先）
$envFile = Join-Path $root ".env"
$cfg = @{}
if (Test-Path $envFile) {
    Get-Content $envFile | ForEach-Object {
        $line = $_.Trim()
        if ($line -eq "" -or $line.StartsWith("#")) { return }
        $i = $line.IndexOf("=")
        if ($i -le 0) { return }
        $k = $line.Substring(0, $i).Trim()
        $v = $line.Substring($i + 1).Trim().Trim('"').Trim("'")
        $cfg[$k] = $v
    }
}
function Cfg($key, $default) {
    $fromEnv = [Environment]::GetEnvironmentVariable($key)
    if ($fromEnv) { return $fromEnv }
    if ($cfg.ContainsKey($key) -and $cfg[$key] -ne "") { return $cfg[$key] }
    return $default
}

$addr = Cfg "APP_ADDR" ":8080"
if ($Port -gt 0) { $addr = ":$Port" }
$dbDriver = Cfg "DB_DRIVER" "sqlite"
$dbDsn = Cfg "DB_DSN" "./data/lens.db"
$adminUser = Cfg "BOOTSTRAP_ADMIN_USER" "admin"
$adminPass = Cfg "BOOTSTRAP_ADMIN_PASS" "Admin@12345"
$cors = Cfg "CORS_ORIGINS" ""

# 统一导出，供 api/migrate/seed 子进程使用
$env:APP_ADDR = $addr
$env:DB_DRIVER = $dbDriver
$env:DB_DSN = $dbDsn
$env:AUTO_MIGRATE = "true"
if ($cors -ne "") { $env:CORS_ORIGINS = $cors }
$env:BOOTSTRAP_ADMIN_USER = $adminUser
$env:BOOTSTRAP_ADMIN_PASS = $adminPass

Write-Host "=== lens-qs 本地启动 ===" -ForegroundColor Cyan
Write-Host "  监听地址 : $addr"
Write-Host "  数据库   : $dbDriver / $dbDsn"
Write-Host "  跨域来源 : $(if ($cors) { $cors } else { '(未设置 → 不下发 CORS 头，浏览器联调会被拦)' })"

if ($Reset) {
    if ($dbDriver -eq "sqlite") {
        $dbPath = Join-Path $root ($dbDsn -replace '^\./', '')
        foreach ($suffix in @("", "-shm", "-wal")) {
            $p = "$dbPath$suffix"
            if (Test-Path $p) { Remove-Item $p -Force; Write-Host "  已删除 $p" }
        }
    } else {
        Write-Warning "-Reset 仅对 sqlite 生效；postgres 请手工清库"
    }
}

Write-Host "`n[1/4] 构建..." -ForegroundColor Green
New-Item -ItemType Directory -Force -Path (Join-Path $root "bin") | Out-Null
go build -o bin/lens-qs-api.exe ./cmd/api
if ($LASTEXITCODE -ne 0) { throw "构建失败" }
go build -o bin/lens-qs-migrate.exe ./cmd/migrate
go build -o bin/lens-qs-seed.exe ./cmd/seed
go build -o bin/lens-qs-createuser.exe ./cmd/createuser
go build -o bin/lens-qs-rebuild.exe ./cmd/rebuild

Write-Host "`n[2/4] 迁移..." -ForegroundColor Green
& ./bin/lens-qs-migrate.exe
if ($LASTEXITCODE -ne 0) { throw "迁移失败" }

if (-not $NoSeed) {
    Write-Host "`n[3/4] 初始化字典..." -ForegroundColor Green
    & ./bin/lens-qs-seed.exe
} else {
    Write-Host "`n[3/4] 跳过种子" -ForegroundColor DarkGray
}

Write-Host "`n[4/4] 启动服务..." -ForegroundColor Green
$exe = Join-Path $root "bin/lens-qs-api.exe"
$log = Join-Path $root "bin/api.log"
$base = "http://127.0.0.1:$($addr.TrimStart(':'))"

if ($NoWait) {
    # 后台启动。
    # 注意：不能用 `Start-Process -NoNewWindow -PassThru`——它会让子进程继承父进程的
    # stdout 管道，调用方（脚本、CI、工具执行器）会一直等这个管道关闭，表现为"卡住"。
    # 这里用 WMI 创建**完全脱离句柄**的进程，并让 cmd 先切换工作目录
    # （否则 sqlite 的相对 DSN `./data/lens.db` 会解析到别的目录而打不开库）。
    $logName = Join-Path $root "bin/api.log"
    $errName = Join-Path $root "bin/api.err"
    Remove-Item $logName, $errName -Force -ErrorAction SilentlyContinue
    $inner = 'cd /d "' + $root + '" && bin\lens-qs-api.exe > "' + $logName + '" 2> "' + $errName + '"'
    $cmd = 'cmd.exe /c "' + $inner + '"'

    $created = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = $cmd }
    if ($created.ReturnValue -ne 0) { throw "启动进程失败，Win32_Process.Create 返回 $($created.ReturnValue)" }

    $ok = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Milliseconds 400
        try {
            if ((Invoke-WebRequest -Uri "$base/healthz" -UseBasicParsing -TimeoutSec 2).StatusCode -eq 200) {
                $ok = $true; break
            }
        } catch { }
    }
    if (-not $ok) {
        Write-Host "服务未能在超时内就绪，日志尾部：" -ForegroundColor Red
        Get-Content $logName -ErrorAction SilentlyContinue | Select-Object -Last 20
        Get-Content $errName -ErrorAction SilentlyContinue | Select-Object -Last 20
        throw "启动失败"
    }
    $live = Get-Process lens-qs-api -ErrorAction SilentlyContinue
    $log = $logName
    Write-Host "服务已在后台就绪（PID $(($live.Id) -join ',')），日志: $logName" -ForegroundColor Green
} else {
    Write-Host "前台运行（Ctrl+C 停止）；日志同时写入 $log" -ForegroundColor Green
    Write-Host "`n按 Ctrl+C 结束服务`n" -ForegroundColor DarkGray
    & $exe 2>&1 | Tee-Object -FilePath $log
    exit $LASTEXITCODE
}

Write-Host "  健康检查 : $base/healthz"
Write-Host "  接口前缀 : $base/api"
Write-Host "`n登录账号：" -ForegroundColor Yellow
Write-Host "  管理员   : $adminUser / $adminPass"
Write-Host "  （管理员只在"库里没有任何用户"时自动创建；若库里已有用户，"
Write-Host "    请用 bin\lens-qs-createuser.exe -username xx -password yy -admin 新建）"
Write-Host "`n常用验证命令：" -ForegroundColor DarkGray
Write-Host @"
  `$t = (Invoke-RestMethod -Uri "$base/api/auth/login" -Method Post -ContentType 'application/json' -Body '{"username":"$adminUser","password":"$adminPass"}').data.token
  Invoke-RestMethod -Uri "$base/api/form-fields" -Headers @{ Authorization = "Bearer `$t" }
"@
Write-Host "`n停止服务： Stop-Process -Name lens-qs-api -Force   （或 Stop-Process -Id <PID>）" -ForegroundColor DarkGray

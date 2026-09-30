$ErrorActionPreference = "Continue"
$base = "http://127.0.0.1:18080"
$tmp = Join-Path $PSScriptRoot "tmp"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

function J($json) { $json | ConvertFrom-Json }
function PostJson($url, $token, $body) {
  $file = Join-Path $tmp ("body-" + [guid]::NewGuid().ToString("N") + ".json")
  [System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
  $out = curl.exe -s -X POST $url -H "Content-Type: application/json" -H "Authorization: Bearer $token" --data-binary "@$file"
  Remove-Item $file -Force
  return $out
}
function PatchJson($url, $token, $body) {
  $file = Join-Path $tmp ("body-" + [guid]::NewGuid().ToString("N") + ".json")
  [System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
  $out = curl.exe -s -X PATCH $url -H "Content-Type: application/json" -H "Authorization: Bearer $token" --data-binary "@$file"
  Remove-Item $file -Force
  return $out
}

$login = J (curl.exe -s -X POST "$base/api/auth/login" -H "Content-Type: application/json" -d '{"username":"zhangsai","password":"Test@12345"}')
$token = $login.data.token
$f = J (curl.exe -s "$base/api/forms/M-260908-01" -H "Authorization: Bearer $token")
$rid = $f.data.records[0].id
Write-Output "表单现有笔数=$($f.data.records.Count)"

Write-Output "`n=== 6) 第 2 笔（另一道工序，序号自动排 2）==="
$body = '{"record":{"process_code":"二次加硬","inspected_by":"张三","inspected_at":"2026-09-10T09:00:00+08:00","submitted_by":"李四","submitted_at":"2026-09-09T08:00:00+08:00","machine_operator":"王五","handover_by":"赵六"},"lines":[{"total_qty":50,"qualified_qty":48,"defect_qty":2,"spot_concave":2}]}'
$r3 = J (PostJson "$base/api/forms/M-260908-01/records" $token $body)
Write-Output "seq=$($r3.data.record.sequence_no) is_complete=$($r3.data.is_complete) 良率=$($r3.data.record.pass_rate) 缺失='$($r3.data.missing_fields -join ',')' 警告数=$($r3.data.warnings.Count)"

Write-Output "`n=== 7) 场景 B：一次提交多笔（records 数组）==="
$body = '{"form":{"sample_raw":"M1 S1 MD L VID1 Rx","sample_project":"M1","unit_text":"一厂"},"records":[{"record":{"process_code":"一次加硬","inspected_by":"张赛赛","inspected_at":"2026-10-01T09:00:00+08:00","submitted_by":"李四","submitted_at":"2026-10-01T08:00:00+08:00","machine_operator":"王五","handover_by":"赵六"},"lines":[{"total_qty":200,"qualified_qty":198,"defect_qty":2}]},{"record":{"process_code":"二次加硬","inspected_by":"张赛赛","inspected_at":"2026-10-02T09:00:00+08:00","submitted_by":"李四","submitted_at":"2026-10-02T08:00:00+08:00","machine_operator":"王五","handover_by":"赵六"},"lines":[{"total_qty":200,"qualified_qty":200,"defect_qty":0}]}]}'
$batch = J (PostJson "$base/api/forms/M-261001-01/records" $token $body)
Write-Output "提交笔数=$($batch.data.count)"
$batch.data.results | ForEach-Object { "  seq=$($_.record.sequence_no) 工序=$($_.record.process_code) 良率=$($_.record.pass_rate) 完整=$($_.is_complete)" }

Write-Output "`n=== 8) strict 模式阻断自相矛盾数据 ==="
$body = '{"form":{},"record":{},"lines":[{"total_qty":100,"qualified_qty":90,"defect_qty":5}],"enforce":"strict"}'
$file = Join-Path $tmp "strict.json"
[System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
$code = curl.exe -s -o NUL -w "%{http_code}" -X POST "$base/api/forms/M-STRICT-1/records" -H "Content-Type: application/json" -H "Authorization: Bearer $token" --data-binary "@$file"
$strict = J (curl.exe -s -X POST "$base/api/forms/M-STRICT-1/records" -H "Content-Type: application/json" -H "Authorization: Bearer $token" --data-binary "@$file")
Remove-Item $file -Force
Write-Output "HTTP=$code code=$($strict.code)"
$strict.details | ForEach-Object { "  阻断 [$($_.rule)] $($_.message)" }

Write-Output "`n=== 9) 默认模式同数据可保存 ==="
$body = '{"form":{},"record":{},"lines":[{"total_qty":100,"qualified_qty":90,"defect_qty":5}]}'
$warn = J (PostJson "$base/api/forms/M-WARN-1/records" $token $body)
Write-Output "action=$($warn.data.action) 警告=$($warn.data.warnings.Count) 错误=$($warn.data.errors.Count)"
$warn.data.warnings | ForEach-Object { "  [warn $($_.rule)] $($_.message)" }

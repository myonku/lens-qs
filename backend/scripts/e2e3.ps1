$ErrorActionPreference = "Continue"
$base = "http://127.0.0.1:18083"
$tmp = Join-Path $PSScriptRoot "tmp"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
function J($json) { $json | ConvertFrom-Json }
function PostJson($url, $token, $body, $extraHeaders) {
  $file = Join-Path $tmp ("b-" + [guid]::NewGuid().ToString("N") + ".json")
  [System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
  $args = @("-s", "-X", "POST", $url, "-H", "Content-Type: application/json", "--data-binary", "@$file")
  if ($token) { $args += @("-H", "Authorization: Bearer $token") }
  if ($extraHeaders) { foreach ($h in $extraHeaders) { $args += @("-H", $h) } }
  $out = & curl.exe @args
  Remove-Item $file -Force
  return $out
}
function PostCode($url, $token, $body, $extraHeaders) {
  $file = Join-Path $tmp ("b-" + [guid]::NewGuid().ToString("N") + ".json")
  [System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
  $args = @("-s", "-o", "NUL", "-w", "%{http_code}", "-X", "POST", $url, "-H", "Content-Type: application/json", "--data-binary", "@$file")
  if ($token) { $args += @("-H", "Authorization: Bearer $token") }
  if ($extraHeaders) { foreach ($h in $extraHeaders) { $args += @("-H", $h) } }
  $out = & curl.exe @args
  Remove-Item $file -Force
  return $out
}

$login = J (curl.exe -s -X POST "$base/api/auth/login" -H "Content-Type: application/json" -d '{"username":"zhangsai","password":"Test@12345"}')
$token = $login.data.token
Write-Output "登录: $($login.data.user.display_name)"

Write-Output "`n=== 1) 幂等：同一个 client_request_id 重复提交（请求体幂等键）==="
$body = '{"client_request_id":"idem-body-1","record":{"process_code":"一次加硬","inspected_by":"张赛赛","submitted_by":"李四","submitted_at":"2026-09-30T08:00:00+08:00","inspected_at":"2026-09-30T09:00:00+08:00","machine_operator":"王五","handover_by":"赵六"},"lines":[{"total_qty":100,"qualified_qty":100}]}'
$r1 = J (PostJson "$base/api/forms/IDEM-01/records" $token $body)
$r2 = J (PostJson "$base/api/forms/IDEM-01/records" $token $body)
Write-Output "首次: action=$($r1.data.action) save_count=$($r1.data.record.save_count)"
Write-Output "重放: action=$($r2.data.action) save_count=$($r2.data.record.save_count)（应与首次完全一致）"
$same = ($r1.data.record.id -eq $r2.data.record.id) -and ($r1.data.record.save_count -eq $r2.data.record.save_count)
Write-Output "两次响应的 record.id 与 save_count 一致: $same"
$f = J (curl.exe -s "$base/api/forms/IDEM-01" -H "Authorization: Bearer $token")
Write-Output "表单实际笔数=$($f.data.records.Count)（应为 1，说明没有重复写入）"
$hist = J (curl.exe -s "$base/api/records/$($f.data.records[0].id)/history" -H "Authorization: Bearer $token")
Write-Output "历史快照数=$($hist.data.total)（应为 0：第二次没有真的再写一次）"

Write-Output "`n=== 2) 幂等：请求头 X-Idempotency-Key 优先 ==="
$body2 = '{"record":{"process_code":"二次加硬","inspected_by":"张三"},"lines":[{"total_qty":50,"qualified_qty":50}]}'
$h1 = J (PostJson "$base/api/forms/IDEM-02/records" $token $body2 @("X-Idempotency-Key: header-abc"))
$h2 = J (PostJson "$base/api/forms/IDEM-02/records" $token $body2 @("X-Idempotency-Key: header-abc"))
Write-Output "首次 seq=$($h1.data.record.sequence_no) 重放 seq=$($h2.data.record.sequence_no) 同一笔=$($h1.data.record.id -eq $h2.data.record.id)"

Write-Output "`n=== 3) 幂等：同键不同请求体 -> 409 ==="
$code = PostCode "$base/api/forms/IDEM-02/records" $token '{"record":{"process_code":"不一样的内容"},"lines":[{"total_qty":1}]}' @("X-Idempotency-Key: header-abc")
Write-Output "HTTP=$code（应 409）"

Write-Output "`n=== 4) 不用幂等键时行为不变（每次都是新的一笔）==="
$n1 = J (PostJson "$base/api/forms/IDEM-03/records" $token '{"record":{"process_code":"一次加硬"}}')
$n2 = J (PostJson "$base/api/forms/IDEM-03/records" $token '{"record":{"process_code":"一次加硬"}}')
Write-Output "seq=$($n1.data.record.sequence_no), seq=$($n2.data.record.sequence_no)（应 1、2）"

Write-Output "`n=== 5) 批量提交是原子的：第二笔 strict 校验失败 -> 整批回滚 ==="
$batch = '{"form":{"sample_raw":"M2 S2 SM R VID2","sample_project":"M2"},"enforce":"strict","records":[{"record":{"process_code":"一次加硬","inspected_by":"张赛赛"},"lines":[{"total_qty":100,"qualified_qty":100}]},{"record":{"process_code":"二次加硬","inspected_by":"张三"},"lines":[{"total_qty":100,"qualified_qty":90,"defect_qty":5}]}]}'
$bcode = PostCode "$base/api/forms/ATOMIC-01/records" $token $batch
Write-Output "HTTP=$bcode（应 422）"
$bresp = J (PostJson "$base/api/forms/ATOMIC-01/records" $token $batch)
Write-Output "错误信息: $($bresp.message)"
$check = curl.exe -s -o NUL -w "%{http_code}" "$base/api/forms/ATOMIC-01" -H "Authorization: Bearer $token"
Write-Output "整批回滚后查该表单 -> HTTP $check（应 404，说明第一笔也没落库）"

Write-Output "`n=== 6) 批量成功路径（整批原子写入，序号连续）==="
$batchOk = '{"form":{"sample_raw":"M1 S1 MD L VID1 Rx","sample_project":"M1"},"records":[{"record":{"process_code":"一次加硬","inspected_by":"张赛赛"},"lines":[{"total_qty":200,"qualified_qty":198,"defect_qty":2}]},{"record":{"process_code":"二次加硬","inspected_by":"张赛赛"},"lines":[{"total_qty":200,"qualified_qty":200}]}]}'
$ok = J (PostJson "$base/api/forms/ATOMIC-02/records" $token $batchOk)
Write-Output "count=$($ok.data.count) atomic=$($ok.data.atomic)"
$ok.data.results | ForEach-Object { "  seq=$($_.record.sequence_no) 工序=$($_.record.process_code) 良率=$($_.record.pass_rate)" }

Write-Output "`n=== 7) 失败响应不缓存：同键可重试 ==="
$badBody = '{"client_request_id":"retry-me","enforce":"strict","record":{},"lines":[{"total_qty":100,"qualified_qty":90,"defect_qty":5}]}'
$c1 = PostCode "$base/api/forms/RETRY-01/records" $token $badBody
$goodBody = '{"client_request_id":"retry-me","record":{"process_code":"重试成功"},"lines":[{"total_qty":100,"qualified_qty":100}]}'
$c2 = PostCode "$base/api/forms/RETRY-01/records" $token $goodBody
Write-Output "第一次(矛盾数据,strict) HTTP=$c1 ; 同键改成合法数据 HTTP=$c2（若第一次被缓存会是 409，实际应为 200）"

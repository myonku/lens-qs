$ErrorActionPreference = "Continue"
$base = "http://127.0.0.1:18080"
function J($json) { $json | ConvertFrom-Json }

$login = J (curl.exe -s -X POST "$base/api/auth/login" -H "Content-Type: application/json" -d '{"username":"zhangsai","password":"Test@12345"}')
$T = "Authorization: Bearer $($login.data.token)"

Write-Output "=== 10) 按样品信息各子项汇总（需求 §4.7 的基础能力）==="
$s1 = J (curl.exe -s "$base/api/stats/summary?group_by=sample_spec" -H $T)
Write-Output "按规格分组:"
$s1.data.groups | ForEach-Object { "  $($_.key): 笔数=$($_.record_count) 总数=$($_.total_qty) 合格=$($_.qualified_qty) 不良=$($_.defect_qty) 良率=$($_.pass_rate)%" }
Write-Output "  总计: 笔数=$($s1.data.total.record_count) 总数=$($s1.data.total.total_qty) 良率=$($s1.data.total.pass_rate)%"

$s2 = J (curl.exe -s "$base/api/stats/summary?group_by=process" -H $T)
Write-Output "按工序分组:"
$s2.data.groups | ForEach-Object { "  $($_.key): 笔数=$($_.record_count) 良率=$($_.pass_rate)%" }

$s3 = J (curl.exe -s "$base/api/stats/summary?group_by=sample_project,sample_spec" -H $T)
Write-Output "按项目+规格组合分组:"
$s3.data.groups | ForEach-Object { "  $($_.key): 笔数=$($_.record_count)" }

$s4 = J (curl.exe -s "$base/api/stats/summary" -H $T)
Write-Output "不分组总计: 笔数=$($s4.data.total.record_count) 总数=$($s4.data.total.total_qty) 良率=$($s4.data.total.pass_rate)%"

Write-Output "`n=== 11) 不良类别分布 ==="
$d = J (curl.exe -s "$base/api/stats/defects" -H $T)
Write-Output "点伤: 凹=$($d.data.point.concave) 凸=$($d.data.point.convex) 双=$($d.data.point.both) 合计=$($d.data.point.total)"
Write-Output "划伤: 合计=$($d.data.scratch.total)  脏污: 合计=$($d.data.dirt.total)  加硬点: 合计=$($d.data.hard_spot.total)  垃圾点: 合计=$($d.data.debris.total)"

Write-Output "`n=== 12) 未完成表单清单 ==="
$inc = J (curl.exe -s "$base/api/stats/incomplete-forms" -H $T)
Write-Output "条数=$($inc.data.total)"
$inc.data.items | Select-Object -First 5 | ForEach-Object { "  $($_.form_no) 第$($_.sequence_no)笔 缺失=$($_.missing_fields)" }

Write-Output "`n=== 13) 工作量（按录入人 / 检验人）==="
$w1 = J (curl.exe -s "$base/api/stats/workload?by=inspected_by" -H $T)
$w1.data.items | ForEach-Object { "  检验人 $($_.key): 笔数=$($_.record_count) 总数=$($_.total_qty)" }
$w2 = J (curl.exe -s "$base/api/stats/workload?by=recorded_by" -H $T)
$w2.data.items | ForEach-Object { "  录入账号 $($_.key): 笔数=$($_.record_count)" }

Write-Output "`n=== 14) 表单列表（筛选）==="
$list = J (curl.exe -s "$base/api/forms?page=1&page_size=10" -H $T)
Write-Output "表单总数=$($list.data.total)"
$list.data.items | ForEach-Object { "  $($_.form_no) 原始串=$($_.sample_raw) 需核对=$($_.needs_review)" }
$list2 = J (curl.exe -s "$base/api/forms?keyword=M-261001" -H $T)
Write-Output "按关键字筛选 M-261001: $($list2.data.total) 条"

Write-Output "`n=== 15) 作废一笔（逻辑标记，数据保留）==="
$f = J (curl.exe -s "$base/api/forms/M-260908-01" -H $T)
$target = $f.data.records[($f.data.records.Count - 1)].id
$void = J (curl.exe -s -X POST "$base/api/records/$target/void" -H "Content-Type: application/json" -H $T -d '{"reason":"录入有误，已重填"}')
Write-Output "state=$($void.data.state) reason=$($void.data.void_reason)"
$after = J (curl.exe -s "$base/api/records/$target" -H $T)
Write-Output "作废后仍可读: 明细行数=$($after.data.record.lines.Count) 工序=$($after.data.record.process_code)"

Write-Output "`n=== 16) 操作日志 ==="
$logs = J (curl.exe -s "$base/api/audit-logs?limit=8" -H $T)
$logs.data.items | ForEach-Object { "  $($_.created_at) $($_.action) $($_.entity) by $($_.actor_name)" }

Write-Output "`n=== 17) 管理员接口（普通用户应 403，管理员应 200）==="
$code403 = curl.exe -s -o NUL -w "%{http_code}" "$base/api/users" -H $T
Write-Output "普通用户 GET /api/users -> HTTP $code403"
$adm = J (curl.exe -s -X POST "$base/api/auth/login" -H "Content-Type: application/json" -d '{"username":"admin","password":"Admin@12345"}')
$code200 = curl.exe -s -o NUL -w "%{http_code}" "$base/api/users" -H "Authorization: Bearer $($adm.data.token)"
Write-Output "管理员 GET /api/users -> HTTP $code200"
$users = J (curl.exe -s "$base/api/users" -H "Authorization: Bearer $($adm.data.token)")
$users.data.items | ForEach-Object { "  用户: $($_.username) 姓名=$($_.display_name) 管理员=$($_.is_admin) 状态=$($_.status)" }

Write-Output "`n=== 18) 字典与参考部件号 ==="
$items = J (curl.exe -s "$base/api/dict/items?category=process" -H $T)
Write-Output "工序字典: $(($items.data.items | ForEach-Object { $_.code }) -join ' / ')"
$dr = J (curl.exe -s "$base/api/dict/drawings?spec=S2&size=MD" -H $T)
Write-Output "参考部件号 S2+MD: $($dr.data.total) 条"
$dr.data.items | ForEach-Object { "  $($_.seq_no) $($_.drawing_no)" }

$ErrorActionPreference = "Continue"
$base = "http://127.0.0.1:18085"
$tmp = Join-Path $PSScriptRoot "tmp"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
function J($json) { $json | ConvertFrom-Json }
function Req($method, $url, $token, $body) {
  $args = @("-s", "-X", $method, $url, "-H", "Content-Type: application/json")
  if ($token) { $args += @("-H", "Authorization: Bearer $token") }
  if ($body) {
    $file = Join-Path $tmp ("b-" + [guid]::NewGuid().ToString("N") + ".json")
    [System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
    $args += @("--data-binary", "@$file")
  }
  $out = & curl.exe @args
  if ($body) { Remove-Item $file -Force }
  return $out
}
function Code($method, $url, $token, $body) {
  $args = @("-s", "-o", "NUL", "-w", "%{http_code}", "-X", $method, $url, "-H", "Content-Type: application/json")
  if ($token) { $args += @("-H", "Authorization: Bearer $token") }
  if ($body) {
    $file = Join-Path $tmp ("b-" + [guid]::NewGuid().ToString("N") + ".json")
    [System.IO.File]::WriteAllText($file, $body, (New-Object System.Text.UTF8Encoding($false)))
    $args += @("--data-binary", "@$file")
  }
  $out = & curl.exe @args
  if ($body) { Remove-Item $file -Force }
  return $out
}

$login = J (Req "POST" "$base/api/auth/login" $null '{"username":"zhangsai","password":"Test@12345"}')
$T = $login.data.token
Write-Output "登录: $($login.data.user.display_name)"

Write-Output "`n=== 1) 字段目录（顶层栏目 + 不良缺陷明细）==="
$fields = J (Req "GET" "$base/api/form-fields" $T)
Write-Output "顶层栏目: $(($fields.data.header_columns | ForEach-Object { $_.label }) -join ' / ')"
Write-Output "缺陷列数: $($fields.data.defect_group.columns.Count)   子列数: $($fields.data.defect_columns.Count)"
Write-Output "表面维度: $(($fields.data.surfaces) -join '/')"
Write-Output "前 6 个缺陷列:"
$fields.data.defect_group.columns | Select-Object -First 6 | ForEach-Object { "  $($_.code) = $($_.label)" }
Write-Output "前 6 个子列字段名: $(($fields.data.defect_columns | Select-Object -First 6 | ForEach-Object { $_.field }) -join ', ')"

Write-Output "`n=== 2) 工序表单目录 ==="
$tpls = J (Req "GET" "$base/api/form-templates" $T)
Write-Output "工序数: $($tpls.data.total)"
$tpls.data.items | ForEach-Object { "  $($_.operation.code) / $($_.operation.label)  可填列数=$($_.field_count)" }
$one = J (Req "GET" "$base/api/form-templates/一次加硬" $T)
Write-Output "取「一次加硬」模板: 列数=$($one.data.layout.defect_columns.Count)"

Write-Output "`n=== 3) 提交一张完整表单（一次加硬，含多个缺陷列）==="
$body = @'
{
  "form_no": "QS-260930-001",
  "operation_code": "一次加硬",
  "form": {
    "sample_raw": "M2 S2 SM R VID2",
    "sample_project": "M2",
    "sample_spec": "S2",
    "sample_size": "SM",
    "sample_side": "R",
    "unit_text": "一厂",
    "lines": [
      {
        "total_qty": 100, "qualified_qty": 96, "defect_qty": 4,
        "defects": {
          "prick": { "concave": 1, "convex": 1 },
          "scratch": { "both": 1 },
          "flange_recess": { "concave": 1 },
          "hardening_bubble": { "convex": 1 }
        }
      }
    ]
  }
}
'@
$r1 = J (Req "POST" "$base/api/forms" $T $body)
Write-Output "action=$($r1.data.action) 表单号=$($r1.data.submission.form_no) save_count=$($r1.data.submission.save_count)"
Write-Output "汇总: 总数=$($r1.data.submission.total_qty) 合格=$($r1.data.submission.qualified_qty) 不合格=$($r1.data.submission.defect_qty) 合格率=$($r1.data.submission.pass_rate)"
Write-Output "完成度: is_complete=$($r1.data.is_complete) missing=$($r1.data.missing_fields -join ',')"
Write-Output "录入人(系统用户)=$($r1.data.submission.submitted_by)"
$id = $r1.data.submission.id
Write-Output "缺陷回读: prick=凹$($r1.data.submission.lines[0].defects.prick.concave)/凸$($r1.data.submission.lines[0].defects.prick.convex) scratch=双$($r1.data.submission.lines[0].defects.scratch.both) 法兰边凹痕=凹$($r1.data.submission.lines[0].defects.flange_recess.concave)"

Write-Output "`n=== 4) 分次补填（只提交部分栏目；未提交的必须保持不变）==="
$body2 = '{"form": {"qualified_qty": 90, "defects": {"dirt": {"convex": 3}}, "remark": "二次核对"}, "reason": "补填脏污"}'
$r2 = J (Req "PATCH" "$base/api/forms/$id" $T $body2)
$s2 = $r2.data.submission
Write-Output "save_count=$($s2.save_count) 工序=$($s2.operation_code) 样品原始串=$($s2.sample_raw) 单位=$($s2.unit_text)"
Write-Output "合格品(显式给出)=$($s2.qualified_qty)  明细行数=$($s2.lines.Count)（未提交 lines，应保持 1 行）"
Write-Output "合格率重算=$($s2.pass_rate)"

Write-Output "`n=== 5) 历史快照与差异 ==="
$hist = J (Req "GET" "$base/api/forms/$id/history" $T)
Write-Output "历史条数=$($hist.data.total) 版本=$($hist.data.items.save_count -join ',')"
$diff = J (Req "GET" "$base/api/forms/$id/history/diff?from=$($hist.data.items[0].id)&to=current" $T)
Write-Output "差异数=$($diff.data.total)"
$diff.data.changes | Select-Object -First 8 | ForEach-Object { "  [$($_.scope)] $($_.label): '$($_.from)' -> '$($_.to)'" }

Write-Output "`n=== 6) 列表筛选（按工序与样品维度）==="
foreach ($q in @('operation_code=一次加硬', 'sample_spec=S2', 'sample_size=SM', 'state=ACTIVE')) {
  $r = J (Req "GET" "$base/api/forms?$q" $T)
  "  ?$q -> total=$($r.data.total)"
}

Write-Output "`n=== 7) 汇总统计（按工序 / 样品维度 / 不良分布）==="
$sum = J (Req "GET" "$base/api/stats/summary?group_by=operation_code" $T)
$sum.data.groups | ForEach-Object { "  工序 $($_.key): 表单数=$($_.form_count) 总数=$($_.total_qty) 合格率=$($_.pass_rate)%" }
$sum2 = J (Req "GET" "$base/api/stats/summary?group_by=sample_spec,sample_size" $T)
$sum2.data.groups | ForEach-Object { "  规格/大小 $($_.key): 表单数=$($_.form_count)" }
$def = J (Req "GET" "$base/api/stats/defects" $T)
Write-Output "  不良分布: 点伤=$($def.data.defects.prick.surfaces.total) 划伤=$($def.data.defects.scratch.surfaces.total) 脏污=$($def.data.defects.dirt.surfaces.total) 法兰边凹痕=$($def.data.defects.flange_recess.surfaces.total) 合计=$($def.data.grand_total)"
$dims = J (Req "GET" "$base/api/stats/dimensions" $T)
Write-Output "  可分组维度($($dims.data.dimensions.Count)): $($dims.data.dimensions -join ', ')"

Write-Output "`n=== 8) 校验分级：矛盾数据 ==="
$badWarn = '{"operation_code":"二次加硬","form":{"lines":[{"total_qty":100,"qualified_qty":90,"defect_qty":5}]}}'
$warn = J (Req "POST" "$base/api/forms" $T $badWarn)
Write-Output "默认(warn): action=$($warn.data.action) 警告=$($warn.data.warnings.Count) 错误=$($warn.data.errors.Count)"
$warn.data.warnings | ForEach-Object { "  [warn $($_.rule)] $($_.message)" }
$badStrict = '{"enforce":"strict","operation_code":"二次加硬","form":{"lines":[{"total_qty":100,"qualified_qty":90,"defect_qty":5}]}}'
$code = Code "POST" "$base/api/forms" $T $badStrict
Write-Output "strict: HTTP=$code（应 422）"

Write-Output "`n=== 9) 批量提交（整批原子）==="
$batchBad = '{"enforce":"strict","forms":[{"form_no":"B-1","operation_code":"一次加硬","form":{"lines":[{"total_qty":10,"qualified_qty":10}]}},{"form_no":"B-2","operation_code":"二次加硬","form":{"lines":[{"total_qty":10,"qualified_qty":1,"defect_qty":1}]}}]}'
$bc = Code "POST" "$base/api/forms/batch" $T $batchBad
Write-Output "第二张失败时 HTTP=$bc（应 422）"
$chk1 = Code "GET" "$base/api/forms/B-1" $T
Write-Output "整批回滚后查 B-1: HTTP=$chk1（应 404）"
$batchOk = '{"forms":[{"form_no":"B-10","operation_code":"一次加硬","form":{"lines":[{"total_qty":20,"qualified_qty":20}]}},{"form_no":"B-11","operation_code":"二次加硬","form":{"lines":[{"total_qty":30,"qualified_qty":30}]}}]}'
$bok = J (Req "POST" "$base/api/forms/batch" $T $batchOk)
Write-Output "批量成功: count=$($bok.data.count) atomic=$($bok.data.atomic)"
$bok.data.results | ForEach-Object { "  $($_.submission.form_no) 工序=$($_.submission.operation_code) 总数=$($_.submission.total_qty)" }

Write-Output "`n=== 10) 幂等与作废 ==="
$idem = '{"client_request_id":"e2e-new-1","operation_code":"一次加硬","form":{"lines":[{"total_qty":5,"qualified_qty":5}]}}'
$i1 = J (Req "POST" "$base/api/forms" $T $idem)
$i2 = J (Req "POST" "$base/api/forms" $T $idem)
Write-Output "幂等回放一致: $($i1.data.submission.id -eq $i2.data.submission.id)"
$void = J (Req "POST" "$base/api/forms/$id/void" $T '{"reason":"录入有误"}')
Write-Output "作废: state=$($void.data.state) reason=$($void.data.void_reason)"
$after = J (Req "GET" "$base/api/forms/$id" $T)
Write-Output "作废后仍可读: 明细行数=$($after.data.submission.lines.Count)"

Write-Output "`n=== 11) 未知字段应报 400 ==="
$unknown = Code "POST" "$base/api/forms" $T '{"form":{"sample_projekt":"M2"}}'
Write-Output "HTTP=$unknown（应 400）"

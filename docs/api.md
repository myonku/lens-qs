# 无纸化检验台账 — v2 接口契约

版本：v2.0-draft.2。日期：2026-11-04。**全部 `/api/v2` 接口均为拟定契约，当前尚无实现，不可按已上线接口对接。**

依据：[需求](requirements.md)、[设计](design.md)。进度及待确认项见[计划](rebuild-plan.md)。当前旧接口见[归档 API](../archive/2026-11-03/legacy/docs/api.md)，实际行为以源码为准。

## 1. 通用约定

### 1.1 认证与复用能力

继续使用现有 `POST /api/auth/login`，请求 `{"username":"...","password":"..."}`，成功 data 含 `token/expires_at/user`。业务请求使用 Cookie（默认 sid）或 `Authorization: Bearer <token>`，沿用现有会话。退出、本人、改密分别为 `/api/auth/logout`、`/api/auth/me`、`/api/auth/change-password`。`/api/users` 系列仍仅管理员可用。

`/api/dict/items`、`/api/units`、`/api/dict/drawings`、`/api/sample/parse` 等基础能力沿用，具体旧契约见归档文档。样品解析结果不自动充当完整身份型号，不自动选择/合并产品批。工序组引用工序字典的稳定 `operation_id`；新增工序先维护字典，不限制预置集合。

`/healthz` 无需登录。v2 普通业务默认对登录用户开放；更正权限暂沿用普通用户可操作，待 Q-05 评审。不设审批接口。

### 1.2 响应、分页及错误

成功：`{"code":"ok","message":"","data":{...}}`。新建 201，查询/修改/动作 200。失败：`{"code":"conflict","message":"...","details":{...}}`。

列表统一 `page=1&page_size=20`，page_size 范围 1–100；响应 data 为 `{items,total,page,page_size}`，空列表 items 为 `[]`。稳定排序以业务排序字段加 ID；所有未列明查询参数、未知请求字段报 400。

| HTTP | code | 场景 |
|---|---|---|
| 400 | `bad_request` | JSON/类型错误、未知字段、缺控制字段、无幂等键 |
| 401/403 | `unauthorized/forbidden` | 未登录/无权 |
| 404 | `not_found` | ID 不存在 |
| 409 | `conflict` | 身份重复、整批/分批互斥、版本/状态/幂等冲突 |
| 422 | `validation_failed` | 负数、非法规范日期、事件日期/时点同时非空、发布前未归组等 |
| 503 | `temporarily_unavailable` | 有界等待后数据库忙/存储不可用；不得继续写业务 |

409 的 `details.reason` 为可编程分支，中文 message 供展示。普通缺项、参考清单不匹配、多缺陷重复计数以及待定的单批数量算式使用 `issues` 警告。v2 不接收旧 `enforce/mode/expected_save_count`，硬性约束不能降级警告。

### 1.3 幂等、并发及批量

每个 v2 写请求必带 `X-Idempotency-Key: <调用方生成的唯一键>`，不接受正文别名；读取接口不需要。键作用域是当前系统用户，不能给不同 URL/对象复用同一个键。同键同方法、实际 URI 和相同原始请求体在 TTL 内重试，回放原状态/响应并附 `X-Idempotent-Replay: true`；同键不同内容或目标 409 `idempotency_key_reused`。

更新既有对象必须有正文 `expected_version`；不匹配 409 `version_conflict`，返回 current_version 和对象 ID。幂等回放先执行，不因上次成功推进 version 而报冲突。成功结果与业务写入同事务；失败整笔回滚。修正请求内容使用新键；503/断线后可原键原文重试。并发同键处理中返回 409 `request_in_progress` 或有界等待后回放。

单据 `version` 与内容版本 `revision_no` 不同：发布、取消也推进 version。PATCH 保存追加完整内容快照，不能原地改历史。批量键属于整个 HTTP 请求，任一项失败全部回滚。

## 2. 字段与时间契约

### 2.1 核心对象

| 对象 | 主要响应字段 |
|---|---|
| ProductLot | `id, shared_form_no, sample_identity, sample_identity_key, sample_raw, sample_project, sample_model, sample_spec, sample_size, sample_side, version, created_at, updated_at` |
| OperationGroup | `id, product_lot_id, operation_id, operation_code, inspection_kind, template_version, reporting_state, batch_mode, version`；状态 `OPEN/CLOSED`，模式 `EMPTY/WHOLE/SPLIT` |
| Submission | `id, operation_group_id, batch_no, scope, inspection_kind, state, version, current_revision_id, draft_revision_id, created_by, created_at, cancelled_by, cancelled_at, cancel_reason` |
| Revision | `id, submission_id, revision_no, base_revision_id, template_version, content, saved_by, saved_at, published_by, published_at, reason` |

state 为 `DRAFT/PUBLISHED/CANCELLED`。PUBLISHED 可同时有 draft_revision_id（修订草稿）；正式统计仍只取 current_revision_id。未归组草稿 group_id 为 null。取消后当前指针仅供历史查阅，不能继续计入统计。

正式归组以 ID 明确选择，单据不直接接收 `shared_form_no` 作为更新目标。绑定后的工序组/批号不能普通修改；录错先取消再建。产品批身份纠错/合并接口本期未定义。

### 2.2 批号空值

仅在**新建**或**未归组草稿的批号修改**中：`batch_no` 不传、null、`""`、`"  "` 表示整批，响应统一为 `batch_no:null, scope:"WHOLE"`。非空批号为 SPLIT，去首尾空白，保留大小写。`scope/batch_mode/batch_key` 不能客户端写入。

PATCH 未归组草稿时不传 batch_no 表示保持原值，显式 null 表示改为整批；归组后即使传同值也拒绝该身份字段 PATCH，避免与内容补填混用。未归组草稿尚不执行组内唯一检查，绑定时执行。

### 2.3 Revision.content（可写内容）

| 字段 | 类型/语义 |
|---|---|
| `paper_unit_id/paper_unit_text` | string/null，单位 ID/原文 |
| `paper_submitter/paper_inspector/paper_machine_operator/paper_handover_by` | string/null，实际或纸面人员，不是登录用户 ID |
| `machine_no` | string/null，本批固定机台号，可复用，不代替批号 |
| `paper_submitted_date/paper_inspected_date` | 规范日期 string/null |
| `paper_submitted_at/paper_inspected_at` | 规范准确时点 string/null |
| `paper_submitted_raw/paper_inspected_raw` | 原始日期文字 string/null；保留证据，不用于日期筛选 |
| `sample_raw` | string/null，本次样品原文，辅助解析不可改写；正式版本保存当时原文快照 |
| `sample_project/sample_model/sample_spec/sample_size/sample_side` | string/null，解析/人工录入的五个核心项；不改变产品批身份 |
| `counts` | `{total_qty,qualified_qty,defect_qty}`，每项为非负整数/null，唯一正式计数源 |
| `declared_counts` | 同 counts，纸面声明，仅核对不入正式求和 |
| `lines` | 数组；每行 `{seq,total_qty,qualified_qty,defect_qty,defects,remark}`；计数非负整数/null，seq 正整数且版本内唯一 |
| `quantity_changes` | 数组；见 §2.5 |
| `remark` | string/null |

`counts:null` 清三项；`counts:{}` 不改；对象内不传保持、null 清空；`lines/quantity_changes` 不传保持，传数组整组替换，`[]` 清空、null 报 400。数组元素是完整新行，遗漏格子为未知，不继承旧行。`defects` 嵌套 `{类别:{concave,convex,both}}`，每格为非负整数/null，省略格子为未知；没有旧版“null 清零”语义，明确零必须传 0。

只读派生项：`pass_rate, line_totals, declared_differences, is_complete, missing_fields, paper_submitted_day, paper_inspected_day`。服务端不按某次表头是否出现来切换计数来源。系统操作者/创建发布取消时间只从会话和服务器写，不接受客户端提交。v2 单位字段仅用 paper_unit_*，无旧 unit_* 别名。

### 2.4 日期/时间仍存字符串

| 类型 | 合法示例 | 规则 |
|---|---|---|
| 日期 | `2026-11-03` | YYYY-MM-DD，真实公历日期；不补造时刻 |
| 准确时点输入 | `2026-11-03T00:30:00+08:00` | RFC3339 秒精度，必须带 Z 或偏移 |
| 准确时点存储/输出 | `2026-11-02T16:30:00Z` | UTC 固定秒精度 TEXT |
| 原文 | `2026.11.3` | 仅放 *_raw，不冒充标准字段 |

同事件 *_date 与 *_at 至多一个非空；两者均空为未知。切换精度的 PATCH 需显式清空旧字段。`2026-02-30`、`2026.11.3` 写标准字段、无时区时刻及小数秒均 422，不静默截断。年份暂定 0001–9999。业务时区拟定 Asia/Shanghai；上例业务日为 2026-11-03。数据库仍存 TEXT，不要求原生 DATE/TIMESTAMP。

### 2.5 数量变化

每项：`{kind,direction,quantity,reason,occurred_date,occurred_at}`。建议 kind 为 LOSS/ADJUSTMENT/OTHER，direction 为 DECREASE/INCREASE/UNKNOWN，LOSS 只允许 DECREASE；枚举待 Q-07。quantity 可为 null，已知时为非负整数；reason 为非空文字。日期/时点可省略，二者不同时有值。

系统为每版条目生成只读 ID，并记录保存用户/时间。修订整组替换，旧版保留。统计只取当前正式版本，不自动扣改 counts；不同批次同原因不保证是不同实物事件。

### 2.6 缺陷目录

三个表面：`concave=凹面`、`convex=凸面`、`both=双面`。下列 35 类全部保留；每格是该类缺陷镜片数量，多类之间可能重复。

| 编码 | 中文 | 编码 | 中文 |
|---|---|---|---|
| prick | 点伤 | raised_point | 凸点 |
| scratch | 划伤 | recess | 凹坑 |
| flange_prick | 法兰边点伤 | bubble | 气泡 |
| flange_scratch | 法兰边划伤 | shape | 面形 |
| collapse_edge | 崩边 | watermark | 水印 |
| coating_particle | 涂层瑕疵 | hard_spot | 加硬点 |
| bright_spot | 亮点 | flange_round_point | 法兰边圆点 |
| crack | 裂 | severe_flange_damage | 严重法兰边伤 |
| overflow_glue | 溢胶 | garbage | 垃圾 |
| discolor_particle | 变色不良颗粒 | flange_damage | 法兰边伤 |
| protective_particle | 保护不良颗粒 | flange_imprint | 法兰边印痕 |
| spin_particle | 旋涂颗粒 | press_imprint | 压痕/法兰边压痕 |
| fiber | 纤维 | flow_mark | 流痕 |
| flange_particle | 法兰边颗粒 | inner_fold | 镜内褶皱 |
| hardening_bubble | 加硬气泡 | flange_fold | 褶皱/法兰边褶皱 |
| hairline_crack | 发丝裂 | inner_flow_glue | 内挂流/挂流 |
| dirt | 脏污 | flange_recess | 法兰边凹痕 |
| spin_mark | 旋痕 | | |

### 2.7 抽检类型标记

`inspection_kind` 为 `NORMAL`（正常）或 `SAMPLING`（抽检），不是 content 内的计数或 scope。它与 WHOLE/SPLIT 正交：抽检也可留空批次或填写真实批号。

- 创建工序组：正文增加 inspection_kind，缺省 NORMAL；同产品批/工序按类型分别创建组，唯一冲突不跨类型。
- 新建单据及 batch.items：有 operation_group_id 时继承组类型；可显式传同值，传不同值返回 422 `validation_failed`，details.reason=`inspection_kind_mismatch`。未归组时可显式指定，缺省 NORMAL；null/非法枚举为 400。
- PATCH draft 不接受 inspection_kind；草稿绑定组时类型不一致返回上述 422，整笔不变。单据、版本、列表、历史和差异响应均回显类型。
- 产品批下工序组列表及 submissions 列表增加 inspection_kind 筛选；未传显示两类并逐条标记。统计及产品批报告未传默认 NORMAL，可传 SAMPLING 或 ALL；ALL 按类型分区（summary 的 groups 强制含类型，total 改为按类型的 totals），无混合总计。group_by 白名单增加 inspection_kind，统计不接受与筛选类型不一致的组 ID（422）。
- 损耗结构仍共用 quantity_changes；前端在 SAMPLING 时拒绝 LOSS 条目，后端不新增此类型专属校验。`stats/quantity-changes` 的 SAMPLING 结果不汇总 LOSS，并说明该业务类型无损耗填报；直接 API 写入的原始条目仍可在详情/历史查看。

例如先创建抽检组，再用普通表单接口提交；不传任何正常检验单 ID：

```json
{"operation_id":"op-a","inspection_kind":"SAMPLING"}
```

```json
{
  "operation_group_id":"group-a-sampling",
  "inspection_kind":"SAMPLING",
  "batch_no":"a",
  "content":{
    "counts":{"total_qty":10,"qualified_qty":9,"defect_qty":1},
    "paper_inspected_date":"2026-11-04",
    "quantity_changes":[]
  }
}
```

上例同产品批正常组中的批号 a 不造成冲突；抽检组再次新建 a 仍返回 batch_no_exists。两类分别留空批次也可共存；正常组关闭不阻断抽检组新增。模板与字段目录共用，前端显示抽检标记，不增加第二套表单字段。

## 3. 拟定接口清单

本节路径统一加 `/api/v2`，全部需登录。

| 方法 | 路径 | 语义 |
|---|---|---|
| GET/POST | `/product-lots` | 分页检索/明确新建产品批 |
| GET | `/product-lots/{id}` | 产品批详情 |
| GET | `/product-lots/{id}/operation-groups` | 本批各工序填报组及进度 |
| POST | `/product-lots/{id}/operation-groups` | 创建某工序组 |
| GET | `/operation-groups/{id}` | 工序组详情、占用方式及进度 |
| POST | `/operation-groups/{id}/close` | 声明结束填报 |
| POST | `/operation-groups/{id}/reopen` | 重新开放 |
| GET/POST | `/submissions` | 分页检索/明确新建草稿，不按号更新 |
| POST | `/submissions/batch` | 多单草稿新建，整批原子 |
| GET | `/submissions/{id}` | 单据及当前正式版/当前草稿 |
| PATCH | `/submissions/{id}/draft` | 保存初始草稿或追加修订草稿 |
| POST | `/submissions/{id}/publish` | 发布指定草稿，明确本批结束 |
| POST | `/submissions/{id}/cancel` | 取消并保留历史 |
| GET | `/submissions/{id}/revisions` | 全部内容版本，revision_no 倒序、分页 |
| GET | `/submissions/{id}/revisions/{revision_id}` | 一版完整内容；版本必须属于本单 |
| GET | `/submissions/{id}/diff` | `from/to` 两个明确 revision ID 的差异 |
| GET | `/form-fields` | v2 字段目录、类型、可空/只读、缺陷顺序及时间格式 |
| GET | `/form-templates`、`/form-templates/{operation_id}` | 版本化工序模板，含 template_version；默认全栏目开放 |
| GET | `/reports/product-lots/{id}` | 按工序分区的只读当前报告 |
| GET | `/stats/summary`、`/stats/defects`、`/stats/quantity-changes` | 当前正式版统计 |

日志继续使用现有 `/api/audit-logs` 路径，但 v2 对象/动作筛选需在实施时扩展。持久报告快照、导出/导入、产品批整体关闭、产品批改键/合并、移组、返工轮次接口本期尚未定稿，不冒充已提供。

## 4. 产品批与工序组

### 4.1 产品批新建/查找

`POST /api/v2/product-lots`：

```json
{
  "shared_form_no": "QS-001",
  "sample_identity": "M2 S2 ND2 SMR",
  "sample_raw": "M2 S2 ND2 SMR",
  "sample_project": "M2",
  "sample_model": "VID2"
}
```

共享号、完整型号必须非空；暂仅去首尾空白生成身份键，保留完整原文，不自动从 sample_model 拼接。重复身份 409 `product_lot_exists` 并返回 `product_lot_id`，由调用方明确选用现有批，不覆盖其资料。缺身份可先建未归组单据草稿。

GET 列表支持 `shared_form_no/sample_identity/keyword`，前两者按规范键精确查找。响应 ProductLot 含 version，供未来受控身份修正使用，本期无普通 PATCH。

### 4.2 工序组

`POST /api/v2/product-lots/{id}/operation-groups` 请求 `{"operation_id":"op-a"}`；201 返回 OperationGroup，初始 `OPEN/EMPTY/version=1`。同产品批同工序同 inspection_kind 已存在则 409 `operation_group_exists` 并返回 group ID。每组模板版本由服务端固定，不按请求时间悄然改变已存版本。

close/reopen 请求 `{"expected_version":3,"reason":"本工序填报结束"}`，原因必填。close 有初始草稿未发布则 409 `unfinished_submissions`，返回单据 IDs；已有正式单的修订草稿不阻止关闭。关闭仅限制新建/归组，已有正式单更正或取消仍允许。reopen 只改变本组。

工序组占用单新增/取消及开放状态变化均推进组 version，调用方应读取最新值；单据内容更正只推进单据 version。重复 close/reopen 的原键重试回放成功，新键请求已处于目标状态则 409 `group_state_conflict`。

## 5. 检验单保存、发布与取消

### 5.1 建立整批草稿

`POST /api/v2/submissions`，不填 batch_no：

```json
{
  "operation_group_id": "group-a",
  "content": {
    "machine_no": "M01",
    "sample_raw": "M2 S2 ND2 SMR",
    "paper_unit_text": "一厂",
    "paper_submitter": "王五",
    "paper_submitted_date": "2026-11-02",
    "paper_inspector": "张三",
    "paper_inspected_date": "2026-11-03",
    "paper_machine_operator": "李四",
    "paper_handover_by": "钱七",
    "counts": {"total_qty": 100, "qualified_qty": 96, "defect_qty": 4},
    "lines": [{"seq": 1, "defects": {"prick": {"concave": 2, "convex": 0, "both": 0}}}],
    "quantity_changes": [{"kind": "LOSS", "direction": "DECREASE", "quantity": null, "reason": "搬运破损，数量待核对"}]
  }
}
```

响应 201（选摘）：

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "submission": {
      "id": "sub-whole-a", "operation_group_id": "group-a",
      "batch_no": null, "scope": "WHOLE", "state": "DRAFT", "version": 1,
      "current_revision_id": null, "draft_revision_id": "rev-1"
    },
    "draft_revision": {"id": "rev-1", "revision_no": 1},
    "issues": [], "is_complete": true, "missing_fields": []
  }
}
```

选摘省略完整 content；真实响应需返回该版本全部内容及派生 pass_rate=96。完整度提示按工序、单位、检验人、样品原文及至少一条明细判断，非发布门槛；以上数量变化未知量亦不会使内容完整度等于单批未结束。

具名分批新建只需另传 `"batch_no":"a"`，其余相同。不传/null operation_group_id 可以先存未归组 DRAFT，不占组、不入统计；content 可为空对象以允许先建草稿，发布前再归组。

### 5.2 后续新建的校验结果

假定 group-a 已被上一节整批草稿占用，用**新幂等键**再 POST 空批次：

```json
{
  "code": "conflict",
  "message": "该产品批在本工序已有整批检验单，请补填原单",
  "details": {
    "reason": "whole_submission_exists", "operation_group_id": "group-a",
    "existing_submission_ids": ["sub-whole-a"], "existing_scope": "WHOLE",
    "allowed_action": "edit_existing"
  }
}
```

HTTP 409。下列判断均包含初始草稿和正式单，取消单不占用：

| 已存在 | 新批次 | details.reason |
|---|---|---|
| WHOLE | 空 | `whole_submission_exists` |
| WHOLE | a | `batch_scope_conflict` |
| SPLIT a/b | 空 | `batch_scope_conflict` |
| SPLIT a/b | a | `batch_no_exists` |
| SPLIT a/b | c | 允许 201 |
| CLOSED 工序组 | 任意新增/草稿归组 | `operation_group_closed` |

按顺序先判断组是否开放，再判断整批/分批互斥、范围内重复；以事务内状态为准。不同工序组的同名批号合法。同键原文重试直接回放先前 201。409 不自动把此次新建改为更新，也不生成随机批号绕过校验。

### 5.3 补填/修订草稿

`PATCH /api/v2/submissions/sub-whole-a/draft`：

```json
{
  "expected_version": 1,
  "patch": {"remark": "补记原因", "paper_inspector": "张三", "counts": {"defect_qty": 4}},
  "reason": "补填"
}
```

200：data 同新建结构，version=2、draft_revision_id 更新、revision_no=2；current_revision_id 仍不变。未提交的纸面栏目、计数和 arrays 保持；整个 patch 为空且无归组操作报 400。revision 内容不可覆盖。

未归组单允许在该请求额外传顶层 `operation_group_id` 与可选 batch_no 绑定；只传 batch_no 可修改未归组草稿的范围。已归组单不允许再传这两个身份字段，返回 400。绑定时若重复返回 §5.2 冲突，原未归组草稿内容与版本均不变。

对 PUBLISHED 单调用相同接口自动创建/保存修订草稿，正式版持续参与报表。对 CANCELLED 单保存 409 `submission_cancelled`。

### 5.4 发布指定草稿

`POST /api/v2/submissions/{id}/publish`：

```json
{"expected_version": 2, "draft_revision_id": "rev-2", "reason": "本批检验结果登记结束"}
```

200 返回完整 Submission 与新 current_revision；version 推进，state=PUBLISHED，draft_revision_id=null。首次发布 reason 可空；已有正式版的修订发布 reason 必填。必须指定当前草稿 ID，不能通过此接口修改内容或发任意历史版本；不匹配 409 `draft_revision_conflict`，无草稿为 `no_draft`。

未归组、规范数据非法返回 422；普通纸面缺项或数量未知允许发布但带 issues，统计标明不完整，不用发布动作伪造真实计数。明确发布本批不代表审批，也不会关闭整工序或整产品批。

### 5.5 取消

`POST /api/v2/submissions/{id}/cancel` 请求：

```json
{"expected_version": 3, "reason": "整批单录错，改按真实检验分批填报"}
```

200 返回 CANCELLED 单据，保留全部版本、原因和用户时间，同事务释放 slot，正式统计排除本单。最后一张占用单取消后组模式为 EMPTY，组仍关闭则先 reopen 才能新建。取消不可恢复普通编辑；明确新建生成新 ID，同一旧取消批号可重新使用并显示历史提示。相同幂等键重试回放；新键重复取消 409 `submission_cancelled`。

### 5.6 批量新建

`POST /api/v2/submissions/batch` 请求 `{"items":[<§5.1 单个请求>, ...]}`，建议上限 100，空数组/超限 400。每项仅支持新建草稿，不混入修改/发布指令；201 data.items 按输入顺序返回保存结果。第 index 项失败时顶层使用对应错误 HTTP/code，details 加零起始 `index`，其余项、slot、版本及日志全部回滚；组内重复、批量整批/分批混用同样拒绝。后续如需批量发布，另定契约。

### 5.7 查询与历史

GET submissions 筛选：`product_lot_id, operation_group_id, operation_id, state, scope, batch_no, machine_no, paper_inspector, created_by, unassigned, has_draft, incomplete, date_basis, from, to`。batch_no 只查具名分批，整批用 scope=WHOLE；state 为空不隐藏取消单，调用方明确筛选。设备/人员/业务日取当前正式版，未发布则取当前草稿；同一列表不把一单列成两行。

GET detail 返回 `{submission,current_revision,draft_revision,issues,is_complete,missing_fields}`，缺失版本为 null。历史列表包含已废弃草稿与各正式版，版本内可查其发布时间及保存人。diff 的 from/to 必须为本单 revision ID，返回字段路径的 old/new 与 lines/quantity_changes 差异；不再使用不稳定的 current 别名代替精确 ID。

## 6. 报告、日期筛选与统计

### 6.1 共同口径

报告及统计默认只取未取消单的当前正式版本，草稿/旧版本不参与。每个响应附 `as_of`、`included_revision_ids` 和口径说明，正式集合在一个一致读快照内确定。暂报数据独立展示，不与正式结果相加。

日期筛选：

- `date_basis=inspection_day`（默认）：from/to 是 `YYYY-MM-DD`，均包含边界，取按业务时区算出的 paper_inspected_day；不明日期不自动用创建日补齐，返回 unknown_day_count。
- `date_basis=created_at`：from/to 是有时区的秒精度时点，规范为 UTC，区间 `[from,to)`。
- 起点大于终点或类型不符 422；可仅给一端。inspection_day 的 from=to 表示一天；created_at 的相等区间为空。
- 列表时间口径与统计使用同一约定；按月只能截取规范业务日，不能截取旧纸面原文。

### 6.2 产品批报告

`GET /api/v2/reports/product-lots/{id}` 返回产品批、按工序排序的 groups；每组包含组状态/模式、具名批次或整批单、当前正式版、小计、初始草稿数、修订草稿数、published_count 和 registered_count。registered_count 仅计未取消已归组单；cancelled_count 单列，未归组单不计入。工序顺序只用于展示。

空批次的范围明确显示“整产品批检验”，不显示“批次待填”。跨工序数量不压成产品总量，人员/设备保留各单值。工序开放时提示仍可能新增，未记录不能推断未生产。

### 6.3 汇总统计

`GET /api/v2/stats/summary?group_by=operation_id,machine_no&product_lot_id=lot-1`。

维度白名单：`product_lot_id, shared_form_no, sample_identity, operation_id, batch_no, scope, machine_no, paper_unit_text, paper_inspector, paper_submitter, paper_machine_operator, paper_handover_by, sample_project, sample_model, sample_spec, sample_size, sample_side, created_by, inspection_day, inspection_month`。过滤复用 §5.7 与 §6.1 中适用项；state 固定有效正式结果，不接收更改统计资格的 state 参数。非法维度 400 并返回 supported。

计数每项返回值示例（一个分组内有 2 张单，其中 1 张未填检验量）：

```json
{
  "key": {"operation_id": "op-a", "machine_no": "M01"},
  "submission_count": 2,
  "counts": {
    "total_qty": {"value": null, "known_sum": 100, "unknown_count": 1, "complete": false},
    "qualified_qty": {"value": 190, "known_sum": 190, "unknown_count": 0, "complete": true},
    "defect_qty": {"value": 10, "known_sum": 10, "unknown_count": 0, "complete": true}
  },
  "pass_rate": null,
  "pass_rate_reason": "incomplete_denominator"
}
```

无记录时 value/known_sum=0、unknown_count=0、complete=true，submission_count=0，pass_rate=null。已知范围内可能出现数量矛盾，按单提示；不能把已知部分合计伪装完整结果。两项完整且可比、分母>0、分子不超过分母才计算合格率（百分数，两位小数），不平均单张百分比。

summary data 包含 `{group_by,groups,total,as_of,included_revision_ids,unknown_day_count,notes}`。跨工序 total 必须说明是重复经过工序的检验工作量，非唯一产品数。

`/stats/defects` 返回类别×表面、known_sum/unknown_count，不输出唯一不良件数；整版没有某格为未知，一版多行中任一格未知则该版该格不完整，unknown_count 按缺该格的单数计（不按行数）。`/stats/quantity-changes` 按 kind＋direction 汇总当前版声明的已知量及未知条数；均可接受产品批/工序/业务日期等相同范围筛选，不推导库存或自动扣减结果。

## 7. 调用方迁移注意

旧 `/api/forms` 的 form_no 是全局唯一单据号，v2 的 shared_form_no 是产品批共享身份之一；不能把旧 POST 直接改路径继续按号补填。新的调用顺序为：查找/创建产品批 → 查找/创建工序组 → 新建或按单据 ID 补填 → 明确发布；身份未知可先存未归组草稿。

旧 paper_*_at 接受原文，v2 对应标准时点字段只接受规范输入，日期级改用 *_date，原始文字放 *_raw。旧缺陷 null 清零、默认零计数、显式表头覆盖明细及可选乐观锁均不沿用。v2 的模板/字段目录需下发这些差别，不能直接拿旧字段目录推断。

切换与旧数据待核对见[迁移说明](migration.md)。本文件没有宣称实现、运行或通过任何 v2 接口测试。

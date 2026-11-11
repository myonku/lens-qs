# 无纸化检验台账 v2 — API 契约

修订日期：2026-11-05。本文描述目标接口，不代表现有程序已实现。依据[需求](requirements.md)、[系统设计](design.md)、[公共设计](common-design.md)及[字段目录](fields.md)，无需读取归档。三项正式数量与良率统一使用表头 counts，明细计数仅作核对。

## 1. 协议与通用约定

### 1.1 路径、认证和包装

所有下文相对路径均加 `/api/v2`。`POST /auth/login` 公开；其余相对路径需登录。`GET /healthz` 与 `GET /` 公开且不加前缀。未定义的路径返回 404，无未版本化业务路径别名。

JSON 使用 UTF-8、snake_case。请求 `Content-Type: application/json`。成功响应：

```json
{"code":"ok","message":"","data":{}}
```

失败响应不包含成功 data：

```json
{"code":"conflict","message":"数据已被修改，请刷新核对","details":{"reason":"version_conflict","entity_id":"sub-a","current_version":3}}
```

| HTTP | code                    | 使用范围                                                          |
| ---- | ----------------------- | ----------------------------------------------------------------- |
| 200  | ok                      | 查询、修改、动作、共享目录按键维护                                |
| 201  | ok                      | 新建生产批、组、单据、用户，批量新建                              |
| 400  | bad_request             | 非法 JSON/类型、未知字段、未知/重复查询参数、缺控制字段、非法分页 |
| 401  | unauthorized            | 未登录、会话过期/撤销、停用账户、登录失败                         |
| 403  | forbidden               | 非管理员管理账户、跨检验类型写入                                  |
| 404  | not_found               | 资源不存在、版本不属于指定单据、未定义路径                        |
| 409  | conflict                | 版本、身份、占用、状态或幂等冲突                                  |
| 422  | validation_failed       | 非负整数、规范日期、计量单位、抽检 LOSS 等值域/业务约束不满足     |
| 500  | internal_error          | 未分类服务异常，不返回 SQL/堆栈/凭据                              |
| 503  | temporarily_unavailable | 存储不可用或有界锁等待耗尽                                        |

Cookie 默认 sid、HttpOnly、Path=/、SameSite=Lax，HTTPS 使用 Secure；也可提交 `Authorization: Bearer <token>`。两种凭据都存在且不同返回 400 credentials_conflict，相同视为一个凭据；无效 Bearer 不回退其他 Cookie。Cookie 浏览器请求携带 credentials，写请求 Origin 必须在允许来源内。Bearer 调用不依赖 Cookie。

响应头 `X-Request-Id` 用于排障，不代替幂等键。错误按 code 和 details.reason 判断，不解析中文 message。布尔查询参数只接受 true/false；对象可空字段明确返回 null，数组空返回 []，不得把数组编码成 JSON 字符串。

### 1.2 分页、参数与排序

所有声明分页的接口使用 page（默认1，正整数）、page_size（默认20，1–100）。非法/越界返回400，不静默回退。响应 data 含 items、total、page、page_size；total 为完整筛选范围条数。目录查询为非分页 items/total，除非端点另有说明。

参数不得重复；group_by 使用单个逗号分隔字符串，不能重复参数名。未知字段/查询参数400并指出 path/parameter。GET 不接受请求正文。字段表中的省略表示默认或保持，null 只在明确可空处允许。

列表按 created_at DESC,id DESC；内容版本按 revision_no DESC；审计按 event_id DESC；目录按 sort_no ASC,code ASC。统计分组按维度值稳定升序，null 排最后。客户端不能传任意 SQL sort 表达式。台账与参考图号的 keyword 是按原大小写的文字包含匹配，不解释为通配表达式，两库语义一致。

### 1.3 写入幂等与并发

生产批、工序组、检验单的所有写请求必须携带 `X-Idempotency-Key`，由调用方生成非空唯一字符串；不接受 client_request_id 正文别名。作用域为系统用户，默认成功结果保留24h。同用户键不能用于另一方法、路径或请求体。

指纹绑定方法、实际路径、原始 UTF-8 正文和当前账户类型。同键同指纹回放首次成功 HTTP/正文并返回 `X-Idempotent-Replay: true`；同键不同请求409 idempotency_key_reused。并发原键等待后回放或返回409 request_in_progress；503/断线可原键原文重试。非2xx不缓存成功结果，修改请求内容使用新键。

回放前重新认证并核对当前账户权限；回放成功结果先于 expected_version 检查。类型变更撤销会话、清幂等结果，不能用旧响应越权。幂等 TTL 到期后按新请求校验，仍须业务防重，不能把 TTL 当永久防重。

修改已有组/单必须传正文 expected_version（正整数），不匹配409 version_conflict。version 是对象令牌，保存和取消都会推进；revision_no 只在内容保存时推进。单据从1开始，新建即内容版本1，没有发布步骤。

用户、字典及样品缓存维护不适用本节领域幂等/对象版本协议；PATCH 的输入语义由各端点明示。批量单据新建整批一个事务，键属于整个请求。

### 1.4 日期与数值

| 类型        | 输入                           | 输出与规则                                              |
| ----------- | ------------------------------ | ------------------------------------------------------- |
| date        | 2026-11-05                     | 真实公历 YYYY-MM-DD，年份0001–9999；只有日期不补造时间 |
| instant     | 2026-11-05T09:30:00+08:00 或 Z | 规范 UTC 秒精度，如2026-11-05T01:30:00Z                 |
| quantity    | 非负 JSON 整数或 null          | null 未知，0 明确零；不收字符串/小数/负数               |
| version/seq | 正整数                         | 系统版本令牌/行号；不得使用小数                         |

quantity 最大9007199254740991，单值和聚合必须精确；聚合超出该安全整数范围返回422 quantity_overflow，不截断或转浮点。所有数量按“片”计。

同事件 *_date 与 *_at 至多一个非空，均空表示未知；PATCH 切换精度须显式清旧字段。非法日期、自由日期文字、无时区时点、小数秒均422。后端独立验证，不保存/返回日期原文。

业务日从 paper_inspected_date 取得，或由 paper_inspected_at 按 Asia/Shanghai 归日。date_basis=inspection_day 的 from/to 为包含首尾的日期；created_at 的 from/to 为时点区间[from,to)。系统时间不代替缺失业务日。起止逆序422；系统相等边界为空范围，日期相等表示一天。

### 1.5 常见 reason

| reason                                       | HTTP | 含义/关键 details                                 |
| -------------------------------------------- | ---- | ------------------------------------------------- |
| inspection_kind_forbidden                    | 403  | 目标类型与当前账户不同；expected_kind/target_kind |
| product_lot_exists                           | 409  | product_lot_id，调用方明确选现有批                |
| operation_group_exists                       | 409  | operation_group_id                                |
| whole_submission_exists                      | 409  | existing_submission_ids；补填已有整批单           |
| batch_scope_conflict                         | 409  | existing_scope，整批/分批互斥                     |
| batch_no_exists                              | 409  | batch_no、existing_submission_ids                 |
| operation_group_closed                       | 409  | operation_group_id                                |
| group_state_conflict                         | 409  | 已处于目标状态                                    |
| version_conflict                             | 409  | entity_id、current_version                        |
| submission_cancelled                         | 409  | 取消单不可修改/重复取消                           |
| idempotency_key_reused / request_in_progress | 409  | 幂等键重用/处理中                                 |
| invalid_measurement_unit                     | 422  | path；只允许“片”或其固定ID                      |
| sampling_loss_forbidden                      | 422  | path；抽检不能填写LOSS                            |
| history_unavailable                          | 422  | history_started_at                                |
| invalid_history_boundary                     | 422  | 非完整事务边界、上界不属于截止视图                |

## 2. 对象与内容结构

### 2.1 核心响应对象

| 名称           | 字段                                                                                                                                                                             |
| -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| User           | id,username,display_name,employee_no,department,operation_id,operation_ids,is_admin,account_type,status,created_at,updated_at                                                     |
| ProductLot     | id,shared_form_no,sample_identity,sample_identity_key,sample_raw,sample_project,sample_model,sample_spec,sample_size,sample_side,version,created_by,created_at                   |
| OperationGroup | id,product_lot_id,operation_id,operation_code,operation_label,inspection_kind,template_version,reporting_state,batch_mode,version,created_by,created_at                          |
| Submission     | id,operation_group_id,batch_no,scope,inspection_kind,state,version,current_revision_id,created_by,created_at,last_saved_by,last_saved_at,cancelled_by,cancelled_at,cancel_reason |
| Revision       | id,submission_id,revision_no,base_revision_id,template_version,content_schema_version,content,saved_by,saved_actor_name,saved_at,reason                                          |

User 返回兼容字段 `operation_id` 及可访问工序列表 `operation_ids`。普通账户按 `operation_ids` 隔离；管理员默认可访问全部启用工序。登录、`/auth/me` 和用户管理接口均返回该列表。employee_no/department 可空；account_type=NORMAL/SAMPLING，status=active/disabled。密码、密码哈希、token 哈希不返回。department 是用户部门，不能放“片”的计量单位ID。

Submission.state=ACTIVE/CANCELLED；operation_group_id 可空；取消元信息可空；没有 draft/current 两套内容。Revision 在创建时生效且不可原地修改，base_revision_id 首版null。未归组版使用通用 template_version；绑定保存后采用目标组模板，旧版解释不变。

### 2.2 Revision.content

| 字段                                                                           | 类型              | 省略/空值语义                                                                      |
| ------------------------------------------------------------------------------ | ----------------- | ---------------------------------------------------------------------------------- |
| paper_unit_id / paper_unit_text                                                | string/null       | 单位ID/文字，合法值仅固定“片”项，详见2.3                                         |
| paper_submitter / paper_inspector / paper_machine_operator / paper_handover_by | string/null       | 纸面人员，不是系统用户ID                                                           |
| paper_submitted_date / paper_submitted_at                                      | date/instant/null | 送检事件精度互斥                                                                   |
| paper_inspected_date / paper_inspected_at                                      | date/instant/null | 检验事件精度互斥                                                                   |
| machine_no                                                                     | string/null       | 当前检验批次的机台                                                                 |
| sample_raw                                                                     | string/null       | 原始样品文字，不自动规范化                                                         |
| sample_project / sample_model / sample_spec / sample_size / sample_side        | string/null       | 核心结构化字段，不改生产批身份                                                     |
| counts                                                                         | object            | 表头total_qty/qualified_qty/defect_qty，每项quantity；三项正式数量及良率的唯一来源 |
| lines                                                                          | array             | 每行seq、total_qty、qualified_qty、defect_qty、defects、remark                     |
| quantity_changes                                                               | array             | 每项kind、direction、quantity、reason、occurred_date、occurred_at                  |
| remark                                                                         | string/null       | 单据备注                                                                           |

新建省略可空字段视为null，省略数组为[]，省略counts为三项null。响应返回所有可空字段和全部35×3缺陷格，不把省略解释成0。绑定不从 ProductLot 自动补写/覆盖样品字段；需要复制由调用方明确填写。

PATCH 的 patch 对象递归合并：不传保持；标量null清未知；counts:null清三项，counts:{}不改；lines/quantity_changes不传保持、[]清空、null报400。数组元素作为完整新行，不继承旧seq或条目ID。defects为嵌套类别/表面对象；行内省略格、类别或defects为未知格集合，不是零；defects:null视为全未知。只读条目ID不能写入。

只读派生：pass_rate、pass_rate_reason、line_totals、count_differences、is_complete、missing_fields、paper_submitted_day、paper_inspected_day。只读派生值放详情 derived，不能写入 content；客户端传只读字段400。不提供 declared_counts 或纸面数量声明的独立原始值字段；请求在任何位置携带未定义的声明字段，按未知字段400，不保存或回显。

counts按表头显式填报，lines中的三项计数仅用于核对。新建只填lines而省略counts时，表头三项仍为null；PATCH只改lines或清空明细时，counts保持原值；显式counts:null清为空后，不得从明细补回。单据详情、通用统计、工作量及两种报告的三项数量和良率都遵守同一来源。明细缺陷统计仍直接使用defects格子，数量变化独立统计。

derived.line_totals固定含total_qty/qualified_qty/defect_qty。每项为 `{value,known_sum,unknown_count,complete}`，unknown_count按未填该项的明细行数计；存在至少一行且该项全已知时complete=true、value=known_sum，否则complete=false、value=null。无明细时known_sum=0、unknown_count=0、complete=false、value=null，不能把空明细解释为已核对的零。这是单据内行合计，区别于8.2按单据计未知数的统计单元。

derived.count_differences同样固定含三项键，每项 `{header_value,line_value,difference,comparable}`。只有表头该项非null且行合计完整时comparable=true，difference=header_value-line_value；否则comparable=false、difference=null，已知的header_value/line_value仍各自保留。可比且difference非零时返回line_counts_differ警告，保存和表头统计值保持不变。

lines[].seq必须正整数，版本内唯一；重复422 duplicate_line_seq。完整缺陷编码与顺序见[字段目录](fields.md#5-缺陷编码与顺序)，例如 `{"prick":{"concave":2,"convex":0,"both":null}}`。

数量变化kind=LOSS/ADJUSTMENT/OTHER，direction=DECREASE/INCREASE/UNKNOWN；LOSS只能DECREASE且SAMPLING禁止LOSS。quantity可null，reason非空白必填；日期互斥，可均未知。修订数组完整替换，服务端生成条目id，只属于该版本。响应中的quantity_changes每项额外含只读id；它来自该版本的存储条目，不是可编辑内容，不写入content_json或改变已保存schema定义。请求携带id按未知字段400拒绝，原键重试保留原条目id。

### 2.3 计量单位

`GET /units` 返回固定一项，code=PIECE、name=片，带稳定ID。前端固定传“片”或该ID，服务端仍独立校验。未传、显式null可以保存并提示缺项；其他字符串（含部门、个、片外空格）、无效ID或不匹配组合422 invalid_measurement_unit。

新建只给合法ID时可解析出paper_unit_text=片；只给文字片不要求反查ID。PATCH更新任一单位字段后，以合并结果检查；清为未知需同时清掉仍有值的另一字段，避免仍由另一字段满足单位。单位显示文字不得来自用户department。单位目录不提供新增其他计量单位入口。

### 2.4 完整度与提示

`derived.is_complete` 等于 missing_fields 是否为空。missing_fields 为有序字符串数组：operation_id（未归组）、paper_unit（ID和文字均无合法单位）、paper_inspector（空白）、sample_raw（空白）、lines（无行），只含缺失项。

issues元素为 `{code,severity,path,message}`，severity固定warning。资料完整、可计算良率、数量平衡互相独立。下列提示不阻断保存：

| code                   | 条件                                                                 |
| ---------------------- | -------------------------------------------------------------------- |
| missing_information    | 上述资料缺项，每项一个path                                           |
| counts_unbalanced      | 三项已知且qualified+defect不等于total                                |
| quantity_exceeds_total | 已知单项/缺陷格超过可比的检验量                                      |
| defect_counts_overlap  | 缺陷子项合计与不合格量不同；可能重复计入，不能强制相等               |
| line_counts_differ     | 表头和完整可比行合计不同，path定位counts对应项，表头仍为正式统计来源 |
| sample_project_missing | 项目缺失，但不加入五项missing_fields                                 |
| paper_time_in_future   | 纸面日期/时点在业务当前日/时点之后                                   |
| paper_time_order       | 送检与检验时间在同精度可比较且顺序可疑                               |

参考不匹配及token未知由解析结果返回，不强制每次保存重复解析。无 enforce=warn/strict 模式切换，非法值不能降为提示。

### 2.5 详情结果 Detail

所有单据详情与保存成功data统一结构：

```json
{
  "submission": {
    "id": "sub-a",
    "operation_group_id": "group-a",
    "batch_no": "a",
    "scope": "SPLIT",
    "inspection_kind": "NORMAL",
    "state": "ACTIVE",
    "version": 1,
    "current_revision_id": "rev-a1",
    "created_by": "user-a",
    "created_at": "2026-11-05T01:00:00Z",
    "last_saved_by": "user-a",
    "last_saved_at": "2026-11-05T01:00:00Z",
    "cancelled_by": null,
    "cancelled_at": null,
    "cancel_reason": null
  },
  "current_revision": {
    "id": "rev-a1",
    "submission_id": "sub-a",
    "revision_no": 1,
    "base_revision_id": null,
    "template_version": "general-1",
    "content_schema_version": "2",
    "content": {
      "paper_unit_id": null,
      "paper_unit_text": null,
      "paper_submitter": null,
      "paper_inspector": null,
      "paper_machine_operator": null,
      "paper_handover_by": null,
      "paper_submitted_date": null,
      "paper_submitted_at": null,
      "paper_inspected_date": null,
      "paper_inspected_at": null,
      "machine_no": null,
      "sample_raw": null,
      "sample_project": "M2",
      "sample_model": null,
      "sample_spec": null,
      "sample_size": null,
      "sample_side": null,
      "remark": null,
      "counts": {
        "total_qty": 100,
        "qualified_qty": 96,
        "defect_qty": 4
      },
      "lines": [],
      "quantity_changes": []
    },
    "saved_by": "user-a",
    "saved_actor_name": "录入员",
    "saved_at": "2026-11-05T01:00:00Z",
    "reason": null
  },
  "derived": {
    "pass_rate": 96.0,
    "pass_rate_reason": null,
    "line_totals": {
      "total_qty": {
        "value": null,
        "known_sum": 0,
        "unknown_count": 0,
        "complete": false
      },
      "qualified_qty": {
        "value": null,
        "known_sum": 0,
        "unknown_count": 0,
        "complete": false
      },
      "defect_qty": {
        "value": null,
        "known_sum": 0,
        "unknown_count": 0,
        "complete": false
      }
    },
    "count_differences": {
      "total_qty": {
        "header_value": 100,
        "line_value": null,
        "difference": null,
        "comparable": false
      },
      "qualified_qty": {
        "header_value": 96,
        "line_value": null,
        "difference": null,
        "comparable": false
      },
      "defect_qty": {
        "header_value": 4,
        "line_value": null,
        "difference": null,
        "comparable": false
      }
    },
    "is_complete": false,
    "missing_fields": [
      "paper_unit",
      "paper_inspector",
      "sample_raw",
      "lines"
    ],
    "paper_submitted_day": null,
    "paper_inspected_day": null
  },
  "issues": [
    {
      "code": "missing_information",
      "severity": "warning",
      "path": "paper_unit",
      "message": "请补充计量单位"
    },
    {
      "code": "missing_information",
      "severity": "warning",
      "path": "paper_inspector",
      "message": "请补充检验人"
    },
    {
      "code": "missing_information",
      "severity": "warning",
      "path": "sample_raw",
      "message": "请补充样品原文"
    },
    {
      "code": "missing_information",
      "severity": "warning",
      "path": "lines",
      "message": "请补充明细"
    }
  ]
}
```

上例表头计数完整而明细为空，良率仍按表头计算；行合计及差异不可比较。issues按2.4返回实际缺项提示。取消单仍返回最后内容，统计资格为无效。

## 3. 认证与用户

### 3.1 POST /auth/login

正文username/password为非空string。200 data为 `{token,expires_at,user,is_admin,account_type,submission_inspection_kind}`，同时设置会话Cookie。后两个类型值相同，填报端固定使用submission_inspection_kind，不提供切换填报类型控件。

密码不trim；错误凭据、停用或锁定均401，message不泄露账户是否存在。默认5次失败锁10分钟，会话12h绝对/2h空闲。密码强度作为账户创建/改密校验，技术默认8–128字符，已有哈希验证不能因新建策略改变而拒绝旧密码登录。不设置发布或首次改密业务关卡。

### 3.2 GET /auth/me、POST /auth/logout、POST /auth/change-password

me无参数，200 data为 `{user,is_admin,account_type,submission_inspection_kind}`。logout无正文，200 `{logged_out:true}`，撤销当前会话并清Cookie。

change-password正文 `{old_password,new_password}`，均非空；旧密码错401，新密码不符合长度422。成功200 `{changed:true}`，撤销本人全部会话并清Cookie，重新登录。该接口不能修改管理员标记或账户类型。

### 3.3 用户管理

仅管理员；非管理员403。用户管理无用户物理删除入口。

| 方法/路径         | 请求                                                                                                                   | 成功data |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------- | -------- |
| GET /users        | page,page_size,keyword；keyword匹配登录名/显示名/工号                                                                  | 分页User |
| POST /users       | username,password必填；display_name默认空串，employee_no/department默认null，is_admin默认false，account_type默认NORMAL，operation_ids默认空列表 | 201 User |
| PATCH /users/{id} | display_name,employee_no,department,operation_ids,is_admin,account_type,status,password；至少一项                        | 200 User |

POST 和 PATCH 接受 `operation_ids`。列表中的每个值必须是已启用的 `inspection_operation` 字典项 ID，非法值返回 422（`details.reason=invalid_operation`）。创建省略时默认为空列表；PATCH 省略时保留，传 null 或空列表整体清空。提交工序列表更新会撤销目标账户的会话并清除其填报幂等缓存。旧 `operation_id` 仍接受并映射为单元素列表。

username非空trim后唯一，不能PATCH改名；employee_no非空值唯一，空白归null。department为组织文字。PATCH不传保持；employee_no/department可null清空；display_name可空串但不接受null；is_admin/account_type/status/password不接受null；password是重置操作，不返回。status仅active/disabled；非法枚举422。

重复登录名/工号409 user_exists；无效密码422；不存在404。不能停用自己或取消自己的管理员标记，409 self_admin_change_forbidden；不能让系统失去最后一个启用管理员。账户类型变化允许但当前会话将失效，不隐式修改旧单据类型。

停用、重置密码、账户类型变化同事务撤销会话；类型变化另清该用户领域幂等结果。is_admin更新由当前账户状态立即生效。任何操作不修改已存单据的纸面人名、计量单位或历史操作者姓名快照。

## 4. 字典、参考资料与辅助解析

### 4.1 目录模型与接口

DictItem为 `{id,category,code,label,sort_no,active,meta,created_at,updated_at}`。meta为object/null。类别只采用[参考资料](reference-data.md#1-基础维度)列出的inspection_operation与样品维度，不另保留同义工序类别。代码是稳定标识，标签是显示文字。

| 方法/路径              | 请求                                                                         | 响应data                                          |
| ---------------------- | ---------------------------------------------------------------------------- | ------------------------------------------------- |
| GET /dict/categories   | 无                                                                           | categories：类别字符串数组                        |
| GET /dict/items        | category可选，include_inactive默认false                                      | items,total；按category/sort_no/code排序          |
| POST /dict/items       | category,code必填；label默认code，sort_no默认0，active默认true，meta默认null | 200 DictItem，按category+code存在则维护，否则新增 |
| PATCH /dict/items/{id} | label,sort_no,active,meta；至少一项                                          | 200 DictItem                                      |
| GET /units             | 无                                                                           | items,total；固定PIECE/片目录项含id/code/name     |

以上所有登录用户可用，与填报类型无关。PATCH省略保持，meta:null清除，其他字段不接受null；空label回落code。category/code不能PATCH更名。停用后历史保留，新建组不得引用停用工序；字典标签变化不覆盖历史快照。单位目录只读，新增其他单位路径不提供。

### 4.2 参考部件号

Drawing为 `{id,seq_no,drawing_no,project_code,model_code,spec_code,size_code,side_code,class_code,is_rx,active,note,created_at,updated_at}`，除id/drawing_no/active/系统时间外均可null，is_rx可未知。部件号和序号的初始完整列表见[参考资料](reference-data.md#2-参考部件号)。

`GET /dict/drawings`：分页；可筛project/model/spec/size/side（精确）、keyword（图号包含）、include_inactive=false。返回分页Drawing，按seq_no、drawing_no、id升序；不能静默只返回前500项。

`POST /dict/drawings/import`：正文 `{items:[DrawingInput,...]}`，1–1000项，完整事务。DrawingInput不含id/系统时间；drawing_no非空必填，active默认true，其他字段默认null。按drawing_no精确命中更新；同请求重复图号或序号冲突422并给零起始index，全部回滚。成功200 `{inserted,updated,total}`。不接受自动猜分隔符的rows文本，不静默跳过坏行。停用可通过同图号维护active=false。这是参考目录维护，不是历史检验台账导入。

### 4.3 样品解析与确认

`POST /sample/parse` 正文 `{sample_raw}`，可省略/null/空串；原文原样返回。200 data：

```json
{
  "parsed":{"raw":"M2 S2 ND2 SMR","project":"M2","model":null,"spec":"S2","size":null,"side":null,"class":null,"is_rx":null,"tokens":["M2","S2","ND2","SMR"],"unknown_tokens":["ND2","SMR"],"confidence":0.4},
  "candidates":[],"drawing":null,"matched":false,"from_cache":false,
  "verified":false,"verified_by":null,"verified_at":null,"unmatched_reason":"partial","note":"部分字段未识别，请按原文核对"
}
```

示例假设无ND2别名及SMR组合规则；confidence数值只示意，不作为固定算法承诺。核心五项齐全但无参考项为no_drawing，无核心项为raw_only；缺五项为partial，即使命中参考图号也不补造项目。唯一可靠图号命中才matched=true并返回drawing，多候选放candidates不强选。失败匹配仍200。

`POST /sample/confirm` 正文sample_raw非空必填，project/model/spec/size/side/class/is_rx可空；这是完整确认映射，省略项按null，不与旧映射局部合并。class/is_rx含义仅辅助，不用来阻断检验保存。200返回同parse结构并标verified=true、verified_by/at。确认只更新缓存，不改任何业务记录。

`GET /sample/cache?sample_raw=...` 参数必填（可以空串），200返回 `{normalized_key,from_cache,parsed,verified,verified_by,verified_at}`。无缓存为from_cache=false，其余对象/确认值null，不临时创建业务记录。parse可以更新辅助缓存，GET只读。归一化只用于缓存键，不能覆盖原文或生产批身份。

## 5. 字段目录与模板

`GET /form-fields` 无参数，200 data为 `{schema_version,fields,surfaces,defect_categories}`。fields每项 `{path,label,type,nullable,read_only,order}`，覆盖身份、纸面、样品、机台、计数、备注、变化及派生字段。数组路径用lines[].defects.prick.concave等，不混淆数据库扁平列名。

surfaces为`{code,label}`数组，固定按concave/convex/both排列，中文名为凹面/凸面/双面；defect_categories为35项code/label/order，order从1开始，顺序见字段目录。工序字段是身份引用，pass_rate是只读计算值，不能统称117个可写字段。

`GET /form-templates` 无参数，返回 `{items,total}`，每项 `{operation_id,operation_code,operation_label,template_version,schema_version,layout}`，列出启用工序。`GET /form-templates/{operation_id}` 返回单项，工序不存在404；存在但无专用布局使用通用布局。layout含字段分组顺序与完整字段路径，纸面栏目不能从模板遗漏。当前不通过模板强设业务必填，不建立类型不同的两套字段。历史版本取自身template_version解释，不按今天模板改写内容。

## 6. 生产批与工序组

### 6.1 GET/POST /product-lots、GET /product-lots/

GET列表分页。`shared_form_no` 仅对表单号做包含匹配，支持输入部分或完整表单号，空白值不参与筛选。`sample_identity` 支持精确匹配，`keyword` 对共享号、完整型号和参考 `sample_raw` 做包含匹配。多个筛选参数同时传入时取交集。响应分页ProductLot。详情无查询参数，返回ProductLot，不把所有工序数量加为产品数。

POST正文：

```json
{"shared_form_no":"QS-001","sample_identity":"M2 S2 ND2 SMR","sample_raw":"M2 S2 ND2 SMR","sample_project":"M2","sample_model":null,"sample_spec":"S2","sample_size":null,"sample_side":null}
```

shared_form_no/sample_identity必须非空白；其余可空，省略null。仅身份去首尾空白；sample_raw逐字保存。不传inspection_kind，生产批共享。201 data=ProductLot；重复409 product_lot_exists，带product_lot_id。不能自动覆写已有参考样品资料。无PATCH、删除或合并接口。

### 6.2 GET/POST /product-lots//operation-groups

普通账户只能创建和管理所属工序的工序组，也只能在所属工序下新建（含批量）、绑定、更正或取消单据。未归组时，普通账户须已分配启用工序，且只能操作自己创建的记录。工序校验失败返回 403（`details.reason=operation_forbidden`）。管理员可操作所有工序，仍须满足 `account_type` 对应的 NORMAL 或 SAMPLING 检验类型。列表和详情可供已登录账户查询。

GET可选inspection_kind=NORMAL/SAMPLING/ALL，默认ALL；返回items/total，每项含group及progress。progress为registered_count、active_count、cancelled_count、incomplete_count，统计本组全部单据当前状态；registered含取消，incomplete只计ACTIVE缺项单。没有发布数、草稿数或生产完成数。

POST正文 `{operation_id,inspection_kind?}`，operation_id为启用工序ID；省略类型取当前账户，null/非法枚举400，相反类型403。201返回OperationGroup，初始OPEN/EMPTY/version=1。同批同工序同类型已存在409 operation_group_exists，不能作为新单自动合并。

### 6.3 GET /operation-groups/、POST /operation-groups//close|reopen

GET无参数，200 `{group,progress}`。close/reopen正文 `{expected_version,reason}`，reason非空白必填，200返回同结构。关闭只限制新增及未归组单绑定；已有ACTIVE单补填、更正、取消不受影响。关闭不检查missing_fields，允许空组关闭，不联动其他工序/类型。

组占用改变及close/reopen推进version，纯内容保存不推进。原键重试回放，目标已处于指定状态的新键请求409 group_state_conflict。类型相反403，版本不符409，原因缺失400。

## 7. 检验单新建、保存与取消

### 7.1 POST /submissions

正文 `{operation_group_id?,inspection_kind?,batch_no?,content}`，content对象必填，可{}。缺/null组为未归组记录；类型省略从账户补齐，显式相反类型403。指定组时类型必须与组及账户一致。

batch_no省略/null/空串/全空白归整批，响应batch_no=null、scope=WHOLE；具名批号trim后保留大小写，scope=SPLIT。scope/batch_mode不允许客户端写入。指定组且OPEN时立即检查占用，未归组不占组、不同未归组记录可同批号。

```json
{
  "operation_group_id":"group-a","batch_no":"a",
  "content":{
    "machine_no":"M01","sample_raw":"M2 S2 ND2 SMR","sample_project":"M2",
    "paper_unit_text":"片","paper_submitter":"王五","paper_submitted_date":"2026-11-04",
    "paper_inspector":"张三","paper_inspected_date":"2026-11-05",
    "paper_machine_operator":"李四","paper_handover_by":"钱七",
    "counts":{"total_qty":400,"qualified_qty":390,"defect_qty":10},
    "lines":[{"seq":1,"total_qty":400,"qualified_qty":390,"defect_qty":10,"defects":{"prick":{"concave":2,"convex":0,"both":null}},"remark":null}],
    "quantity_changes":[{"kind":"LOSS","direction":"DECREASE","quantity":null,"reason":"搬运破损，数量待核对","occurred_date":null,"occurred_at":null}],
    "remark":null
  }
}
```

此例为NORMAL；若SAMPLING包含同样LOSS条目，422 sampling_loss_forbidden，整笔不保存。201返回Detail，state=ACTIVE、version=1、revision_no=1；已归组立即入统计。未归组同样保存生效，但不入分组统计，列表可查unassigned=true，完整度缺operation_id。

### 7.2 范围冲突

同一组（已隔离类型）内只有一张有效整批单，或多张有效具名分批单。取消单不占用。

| 当前占用 | 新请求       | 结果                        |
| -------- | ------------ | --------------------------- |
| 整批     | 整批         | 409 whole_submission_exists |
| 整批     | 任意具名分批 | 409 batch_scope_conflict    |
| 分批a/b  | 整批         | 409 batch_scope_conflict    |
| 分批a/b  | a            | 409 batch_no_exists         |
| 分批a/b  | c            | 201                         |
| 组CLOSED | 新建或绑定   | 409 operation_group_closed  |

details可含operation_group_id、existing_submission_ids、existing_scope、batch_no、allowed_action=edit_existing。先校验权限、组状态，再查模式/重复。不同组同名批号合法。同号冲突不能偷偷变成更新、生成随机批号或重建组绕过。

### 7.3 PATCH /submissions/

正文 `{expected_version,patch,reason?,operation_group_id?,batch_no?}`。patch是content补丁，绑定/改未归组批号时可省略，其他保存必须非空。reason可空，记录本次更正说明。

```json
{"expected_version":1,"patch":{"counts":{"qualified_qty":380,"defect_qty":20},"remark":"核对纸单后更正"},"reason":"纠正计数"}
```

200返回Detail，单据version+1、新内容revision_no+1/current_revision_id更新，保存立即生效。未提字段保持，旧内容可查；不存在修订草稿和publish动作。

未归组单可额外传非空operation_group_id绑定，及可选batch_no；只传batch_no可改未归组批号，不传保持，null改整批。绑定检查目标OPEN、类型、防重，失败时原内容、归属和版本均不变。成功绑定和content修改为同次保存/一个版本，并开始统计。

已归组后，即使传相同值也拒绝operation_group_id/batch_no顶层字段，400 immutable_identity。inspection_kind永不允许PATCH。组关闭仍允许已有单内容保存；取消单409 submission_cancelled。归组错误需取消重建，不支持普通移组。

### 7.4 POST /submissions//cancel

正文 `{expected_version,reason}`，reason非空白。200返回取消后的Detail；单据version+1，content/revision_no不变，state=CANCELLED，设置取消证据，立即退出当前统计并释放占用；旧版仍可读。

最后一单取消后组batch_mode=EMPTY，不自动reopen。其他分批仍占用时不能建整批。取消不可恢复；新建用新ID，可复用已释放的批号。原键重试回放；新键重复取消409 submission_cancelled。

### 7.5 POST /submissions/batch

正文 `{items:[<7.1请求>,...]}`，1–100项；仅批量新建，不混合修改/取消。201 `{items:[Detail,...]}`，按输入顺序。任一项失败顶层返回该HTTP/code，details附零起始index；其他单据、占用、内容版本、审计和成功缓存全部回滚。内部同组重复或整批/分批竞争同样拒绝。

### 7.6 GET /submissions、GET /submissions/

GET列表分页，返回每单一个Detail；取消单保留最后版。支持以下参数，均使用当前版字段：

| 参数                                                                                | 语义                                                       |
| ----------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| product_lot_id,operation_group_id,operation_id                                      | 精确归属；多个条件必须一致，否则无结果                     |
| inspection_kind                                                                     | NORMAL/SAMPLING/ALL，默认ALL                               |
| state                                                                               | ACTIVE/CANCELLED，默认两者均列                             |
| scope                                                                               | WHOLE/SPLIT                                                |
| batch_no                                                                            | 精确具名批号；整批使用scope=WHOLE                          |
| machine_no,paper_inspector,paper_submitter,paper_machine_operator,paper_handover_by | 文字精确                                                   |
| paper_unit_text                                                                     | 仅片；其他值422                                            |
| sample_project,sample_model,sample_spec,sample_size,sample_side                     | 结构化值精确                                               |
| keyword                                                                             | 当前sample_raw、共享号或完整型号包含匹配                   |
| created_by,last_saved_by                                                            | 系统用户ID                                                 |
| unassigned                                                                          | true只未归组，false只已归组，省略两者                      |
| incomplete                                                                          | true只缺项，false只完整，省略两者                          |
| sample_project_missing                                                              | true项目未知/空白，false项目非空，省略两者；用于待拆解清单 |
| date_basis                                                                          | inspection_day默认；或created_at                           |
| from,to                                                                             | 可仅一端，按1.4；未知业务日不落入区间                      |

data除分页字段外含unknown_day_count，指满足其他非时间条件但业务日未知的单数，忽略日期区间；不将这些单算进items/total的已知日期区间。未给from/to则未知日单正常列入列表。GET详情无查询参数，200 Detail，不受默认类型筛选限制。

### 7.7 历史与差异

| 方法/路径                                     | 参数                             | 响应data                                     |
| --------------------------------------------- | -------------------------------- | -------------------------------------------- |
| GET /submissions/{id}/revisions               | page,page_size                   | 分页Revision，完整content；按revision_no倒序 |
| GET /submissions/{id}/revisions/{revision_id} | 无                               | {revision,derived,issues}                    |
| GET /submissions/{id}/diff                    | from,to，两个明确Revision ID必填 | from_revision_id,to_revision_id,changes      |

changes为 `{path,old,new}` 数组，路径对应content字段；lines按seq比较，变化条目整数组比较，新增/删除以null表达。历史版本必须属于指定单；跨单或不存在404。from/to相同允许changes=[]。不提供current等会漂移的别名。历史中派生提示使用该版schema和模板，不让新模板改变旧版解释。

## 8. 统计

### 8.1 筛选与统计资格

所有统计只纳入ACTIVE且已归组单据当前版本；未归组、取消及历史版不计。筛选可用7.6的归属、类型、批号、机台、纸面人员、样品字段、keyword、创建/保存人、日期范围，不能传state/unassigned/incomplete/sample_project_missing改变统计资格；若要完整度清单使用submissions列表。

inspection_kind默认NORMAL，可SAMPLING/ALL。选组与类型显式不一致422 inspection_kind_mismatch。没有类型筛选的读取全部可见，不等于统计默认ALL。

所有统计响应包含as_of、through_event_id、included_revision_ids、unknown_day_count、notes。as_of/事件上界表示本次一致读快照，统计端点不接受历史as_of查询；历史查询使用生产批报告。结果无数据仍给空集合与0值，不返回伪造良率。

### 8.2 数值结构及良率

AggregateNumber统一 `{value,known_sum,unknown_count,complete}`，unknown_count按缺该项的单数，complete=unknown_count==0，完整时value=known_sum否则null。空集合value/known_sum=0，unknown_count=0，complete=true。

```json
{"total_qty":{"value":null,"known_sum":100,"unknown_count":1,"complete":false},"qualified_qty":{"value":190,"known_sum":190,"unknown_count":0,"complete":true},"defect_qty":{"value":10,"known_sum":10,"unknown_count":0,"complete":true}}
```

pass_rate为百分数两位小数；不能计算时null，reason按以下优先级：no_records、incomplete_denominator、incomplete_numerator、incomparable_counts（任一单或合计分子超过分母）、zero_denominator。有效时reason=null。分母/分子完整可比才计算，不平均单据百分比。

### 8.3 GET /stats/dimensions、GET /stats/summary

维度目录无参数，返回 `{dimensions:[{code,label,source},...]}`。summary的group_by可省略（只总计），或逗号分隔维度，重复维度/非法维度400并附supported。可使用page/page_size对分组分页，total_groups为全部分组数；统计total不受分组分页影响。

维度白名单：product_lot_id、shared_form_no、sample_identity、operation_id、inspection_kind、batch_no、scope、machine_no、paper_unit_text、paper_inspector、paper_submitter、paper_machine_operator、paper_handover_by、sample_project、sample_model、sample_spec、sample_size、sample_side、sample_raw、created_by、last_saved_by、inspection_day、inspection_month。生产批维度来自身份，内容维度来自各单当前版，不使用生产批参考样品字段覆盖单据值。

分组单元为 `{key,submission_count,counts,pass_rate,pass_rate_reason}`。key为维度值对象，未知值null，不能用“(未填)”替代真实值或用竖线拼接键。NORMAL/SAMPLING单类型data：

```json
{"group_by":["operation_id"],"groups":[],"total_groups":0,"page":1,"page_size":20,"total":{"submission_count":0,"counts":{"total_qty":{"value":0,"known_sum":0,"unknown_count":0,"complete":true},"qualified_qty":{"value":0,"known_sum":0,"unknown_count":0,"complete":true},"defect_qty":{"value":0,"known_sum":0,"unknown_count":0,"complete":true}},"pass_rate":null,"pass_rate_reason":"no_records"},"as_of":"2026-11-05T01:00:00Z","through_event_id":0,"included_revision_ids":[],"unknown_day_count":0,"notes":[]}
```

ALL强制将inspection_kind加入group_by，total改为totals（分别含NORMAL、SAMPLING的分组总计），不提供混合total。跨工序total的notes明确“检验工作量，可能重复计入同件产品”，不称产品产量或全流程良率。

### 8.4 GET /stats/defects

使用8.1范围筛选，无group_by/分页；返回 `{defects,as_of,through_event_id,included_revision_ids,unknown_day_count,notes}`。defects为全部35项，每项 `{code,label,surfaces}`，surfaces按concave/convex/both各返回AggregateNumber。没有填写的格子保留unknown，不能默认为0。

先在每单选定版合并多行，再跨单：单内任一行该格未知或无行则本单该格unknown_count贡献1，known_sum保留已填部分。合计代表缺陷镜片计次，不输出唯一不良件数。ALL返回partitions数组，每项带inspection_kind及上述独立数据，不给跨类型合计。

### 8.5 GET /stats/quantity-changes

使用8.1范围，无group_by/分页。单类型data为 `{groups,as_of,through_event_id,included_revision_ids,unknown_day_count,notes}`。每组 `{kind,direction,item_count,quantity}`，quantity使用AggregateNumber但unknown_count按未知数量条目数计，而不是单数。仅出现过的类型/方向有组；空集合groups=[]。UNKNOWN方向单列，不强行净额相减。

SAMPLING不可能通过本契约新建LOSS；历史异常资料若存在须明确报告data_quality_issues，不能静默视为合法或混入正常损耗。ALL按partitions类型分区。变化声明不自动扣改counts，也不代表准确库存流水。

### 8.6 GET /stats/workload

需要区分两类工作量：metric=inspection（默认）时按by=paper_inspector/created_by/last_saved_by（默认paper_inspector）汇总当前有效已归组单；使用8.1筛选和分组分页，返回 `{metric,by,groups,total_groups,page,page_size,...快照字段}`，groups为8.3分组单元。系统账号维度表示这些单的创建/最近保存归属，不称全部操作次数。ALL强制分类型。

metric=save_actions时by只能saved_by，from/to必须系统秒精度时点且两端必填，范围[saved_at from,to)。可筛product_lot_id、operation_id、inspection_kind（默认NORMAL）和saved_by；不能传内容/业务日筛选。统计成功内容保存事件，含后来取消单与未归组单的保存，后续取消不会抹掉动作；归组条件取该次保存后的状态，未归组不满足生产批/工序筛选。返回分页 `{metric,by,items:[{saved_by,inspection_kind,save_count,distinct_submission_count}],total,page,page_size,as_of,through_event_id}`，不返回检验量。保存动作不因一个事务有多条审计事件而重复计数。

## 9. 报告

### 9.1 GET /reports/product-lots/

参数inspection_kind=NORMAL默认/SAMPLING/ALL；as_of可省略为当前系统快照，若给必须带时区秒精度且不晚于现在；through_event_id可选，但给时必须同时给as_of，且上界须为该截止视图的完整已提交事务末事件。不能传from/to/date_basis。

200 data：

| 字段                                      | 含义                                                    |
| ----------------------------------------- | ------------------------------------------------------- |
| product_lot                               | 截止视图的生产批身份快照                                |
| groups                                    | 当时已存在的组，按operation_code/inspection_kind/id排序 |
| as_of,through_event_id,history_started_at | 系统截止时点、原子事件上界、历史起点                    |
| included_revision_ids                     | 实际参与有效小计的内容版本ID集合                        |
| notes                                     | 类型、口径和限制说明                                    |

每个groups元素 `{group,submissions,progress,subtotal}`；submissions为当时各单Detail（含取消），progress为6.2的登记/有效/取消/缺项数，subtotal为8.3分组统计单元，只计当时ACTIVE且已归组版本。ALL逐组分类型，没有产品总计。未归组单不属于某生产批，不能猜测纳入。

历史按系统保存/状态事件回放，不按纸面日期筛今天版本。后续更正/绑定/取消/关闭和目录改名均不能改变旧时点。as_of早于history_started_at422 history_unavailable；该时点生产批不存在404。返回的as_of与through_event_id原样回传可复验同秒视图。无持久报告快照写入口，不假定后序曾阅读某版本。

### 9.2 GET /reports/operations/

from/to两端必填；date_basis默认inspection_day，也可created_at；可选product_lot_id、inspection_kind=NORMAL默认/SAMPLING/ALL、page/page_size。操作工序ID存在但无记录200空结果，不存在404。格式/逆序422，缺端点400。

200 data为 `{operation,items,total,page,page_size,summary,active_count,cancelled_count,incomplete_count,unknown_day_count,date_basis,from,to,as_of,through_event_id}`。items为Detail，另附product_lot_id/shared_form_no/sample_identity/listing_revision_id；每单当前版筛选。取消资料可列，incomplete_count仅有效缺项单。

summary为整个筛选范围的有效结果，不限当前页，含按product_lot_id及inspection_kind的groups、单类型total或ALL的totals、included_revision_ids和notes；结构复用8.3。unknown_day_count为满足工序/生产批/类型等非时间条件但业务日未知的单数，按状态分别显示在notes或明细计数说明，不假装区间内记录。

报告取当前版本在业务/创建日期范围的查询，可因后来业务日期更正而改变。需要过去系统状态用9.1；翻页时若发生并发写入，应重新查询/保留快照标识核对，不承诺不同HTTP请求自动锁同一快照。

## 10. 审计与公开信息

`GET /audit-logs` 登录可读，分页；可筛entity、entity_id、actor_id、from/to（系统时点[from,to)，可单端）。entity白名单product_lot/operation_group/submission/user/dict_item/sample_cache/auth；返回items/total/page/page_size，元素为 `{event_id,transaction_id,actor_id,actor_name,action,entity,entity_id,before,after,reason,created_at}`。敏感字段脱敏，认证事件不返回完整凭据。按event_id倒序。业务历史边界仅用可回放领域事件的完整事务标识，不能任意拿共享日志行号当业务快照上界。

`GET /healthz` 无包装返回 `{status:"ok",time:<UTC时点>}`；依赖不可用时503返回status=unavailable。`GET /` 无包装返回name/version，版本信息不表示文档功能已全部通过验收。

## 11. 低优先级计划能力

Excel/CSV导出、纸单打印/PDF、历史台账导入是计划实现项，当前不把未定协议列成可调用端点。导出须复用明确筛选/版本集合/未知量和类型分区；打印包含全部纸面栏目；历史导入须有预览、逐行错误、明确身份映射与防重，不得自动覆盖已有单或伪造跨工序来源。格式、规模、映射与独立接口需在实施前另行定稿。

## 12. 调用流程

1. 登录，读取账户固定填报类型；需要时获取字段目录、工序、固定计量单位和模板。
2. 用共享号＋完整型号选/建生产批，在本类型下选/建工序组；身份暂缺可先保存未归组记录。
3. 明确新建单据或按单ID PATCH，写请求带幂等键；已有对象动作带expected_version。
4. 保存成功立即以当前版统计，资料缺项按提示补填；不再调用发布接口。
5. 需要时取消错误单据、结束/重新开放本组填报；这些操作均留痕，不联动别的工序或类型。
6. 查询统计时区分null和0；历史报告保留as_of/through_event_id；样品原文与纸面检验人员不从账号或解析结果自动替代。

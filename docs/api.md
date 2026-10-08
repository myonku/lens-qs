# lens-qs-api 接口文档（前端对接版）

> 模块：`github.com/myonku/lens-qs-api`（Go + Gin，SQLite / PostgreSQL）
> 本文按**后端代码实际行为**整理，JSON 键名一律 **snake_case**。
> 模型已按"**每道工序填自己那张表单**"重构（`forms` 一次提交 + `form_lines` 明细行 + `form_history` 快照）。
> 旧模型（一张表单 + 多笔工序记录 + 15 个不良子项）已废弃，**不要**再按旧模型对接。

---

## 目录

1. [总览](#1-总览)
2. [字段目录与渲染指引](#2-字段目录与渲染指引前端最重要的一节)
3. [错误与校验语义](#3-错误与校验语义)
4. [接口清单表](#4-接口清单表)
5. [逐接口详情](#5-逐接口详情)
6. [数据模型速查](#6-数据模型速查)
7. [汇总统计](#7-汇总统计)
8. [附录](#8-附录)

---

## 1. 总览

### 1.1 Base URL 与请求约定

| 项 | 值 |
|---|---|
| 监听地址 | 环境变量 `APP_ADDR`，默认 `:8080` → `http://127.0.0.1:8080` |
| API 前缀 | `/api`；`GET /`、`GET /healthz` 在根路径 |
| 请求体 | `Content-Type: application/json`（全部为 JSON，无表单/文件上传） |
| 请求体上限 | **4 MB**（幂等中间件用 `http.MaxBytesReader` 读请求体）。超过时读体失败，该次写请求拿不到请求体 → 后续 JSON 解析失败 → **400** |
| 请求 ID | 请求头 `X-Request-Id`（其次 `X-Request-ID`）可选，存在则透传；响应头一定回写 `X-Request-Id` |
| 需登录范围 | **仅** `GET /healthz`、`GET /`、`POST /api/auth/login` 公开；其余 `api` 路由全部需要登录 |
| 权限级别 | 只有两级：普通用户 / 管理员（`is_admin`）。**仅 `/api/users`、`/api/users/{id}` 需要管理员** |
| CORS | 由 `CORS_ORIGINS`（逗号分隔，支持 `*`）控制。**为空时不加任何 CORS 头**；命中白名单时回 `Access-Control-Allow-Origin: <origin>`（回显具体 origin，不是 `*`）、`Access-Control-Allow-Credentials: true`、`Access-Control-Allow-Methods: GET, POST, PATCH, PUT, DELETE, OPTIONS`、`Access-Control-Allow-Headers: Authorization, Content-Type, X-Auth-Token, X-Request-Id`、`Access-Control-Max-Age: 600`；`OPTIONS` 预检直接返回 **204** 空响应 |
| 时间格式 | `store.FormatTime`：`YYYY-MM-DDTHH:MM:SSZ`（UTC，秒级）；部分入参也兼容 `2006-01-02 15:04:05` / `2006-01-02T15:04` / `2006-01-02` |

### 1.2 认证

**三种取令牌方式**（`internal/middleware.TokenFromRequest`，按**固定优先级**取第一个非空值，命中即返回、不回退）：

| 顺序 | 来源 | 说明 |
|---|---|---|
| 1 | **Cookie** | Cookie 名 = 环境变量 `COOKIE_NAME`，**默认 `sid`**；值 `TrimSpace` 后非空才采用 |
| 2 | **`Authorization: Bearer <token>`** | 前缀大小写不敏感（`bearer` / `Bearer` 均可）；必须是"空格 + token"形式 |
| 3 | **`X-Auth-Token: <token>`** | 无前缀 |

> ⚠️ **只取第一个非空来源**：若请求里同时带了 Cookie 与 `Authorization`，而 Cookie 值已失效，**不会回退**到 Bearer，直接 401。
> ⚠️ Cookie 名可通过 `COOKIE_NAME` 改；若改了名，用 Cookie 认证的前端要同步（用 `Authorization` 或 `X-Auth-Token` 则不受影响）。

**登录下发的 Cookie 属性**：`Path=/`、`Max-Age=43200`（12 小时）、`HttpOnly=true`、`SameSite=Lax`、`Secure=COOKIE_SECURE`（默认 `false`）。
会话令牌：32 字节随机数的 base64url；服务端只存 SHA-256 哈希。

**会话生命周期（环境变量）**：

| 机制 | 环境变量 | 默认值 |
|---|---|---|
| 会话绝对有效期 | `SESSION_TTL` | `12h` |
| 空闲超时 | `SESSION_IDLE_TTL` | `2h` |
| 连续登录失败上限 | `MAX_LOGIN_FAIL` | `5` |
| 达到上限后的锁定时长 | `LOCK_DURATION` | `10m` |

未认证/令牌失效统一返回 **401** `{"code":"unauthorized","message":"未登录或会话已失效"}`；缺少令牌时 `message` 为 `"缺少会话信息，请先登录"`。
非管理员访问需管理员接口返回 **403** `{"code":"forbidden","message":"该操作仅管理员可执行"}`。

### 1.3 统一响应格式

成功（`internal/pkg/httpx`）：

```json
{ "code": "ok", "message": "", "data": { } }
```

失败（**没有** `data` 字段；`details` 仅在非空时出现）：

```json
{ "code": "bad_request", "message": "请求体格式不正确: ...", "details": { } }
```

- 成功状态码为 **200**，创建为 **201**（`httpx.Created`）；两者响应体格式完全一致，`code` 都是 `"ok"`。
- 未识别的内部错误统一为 **500** `{"code":"internal_error","message":"服务器内部错误"}`（不暴露内部细节）。
- 唯一确认的例外：`GET /healthz` 与 `GET /` 不走统一包装（见 §5.1）。

### 1.4 错误码表

| `code` | HTTP 状态 | 含义 | 典型触发 |
|---|---|---|---|
| `ok` | 200 / 201 | 成功 | — |
| `bad_request` | 400 | 请求不合法 | JSON 解析失败、未知字段、非法缺陷列/表面维度、不支持的分组维度、批量为空 |
| `unauthorized` | 401 | 未登录或会话失效 | 缺少/无效令牌、空闲超时 |
| `forbidden` | 403 | 无权限 | 非管理员访问 `/api/users*` |
| `not_found` | 404 | 资源不存在 | 表单 ID/表单号不存在、历史快照 ID 不存在、字典项不存在 |
| `conflict` | 409 | 冲突 | 幂等键重复且请求体不同、`expected_save_count` 不匹配、唯一约束（表单号/明细行序号） |
| `validation_failed` | 422 | 校验未通过 | `enforce=strict` 且产生 error 级 issue |
| `internal_error` | 500 | 服务器内部错误 | 未归类错误 |

### 1.5 分页约定与上限

**通用分页结构**（`httpx.Page[T]`，`items` 为 `null` 时统一输出 `[]`）：

```json
{ "items": [], "total": 0, "page": 1, "page_size": 20 }
```

| 接口 | 查询参数 | 默认 | 上限 / 说明 |
|---|---|---|---|
| `GET /api/forms` | `page`、`page_size` | `page=1`、`page_size=20` | `page_size` 仅接受 `1..200`，超出或非法则回落 20；`ListSubmissions` 内部 `limit<=0 或 >200` 也回落 20 |
| `GET /api/users` | `page`、`page_size` | 同上 | 同上 |
| `GET /api/audit-logs` | `limit` | `100` | `1..500`，非法/越界回落 100 |
| `GET /api/stats/incomplete-forms` | `limit` | `100` | `1..500`，非法/越界回落 100 |
| `GET /api/stats/summary` | — | — | 分组结果最多 **500** 行（SQL `LIMIT 500`），按 `form_count DESC` 排序 |
| `GET /api/stats/workload` | — | — | 最多 **200** 行（SQL `LIMIT 200`），按 `form_count DESC` 排序 |
| `GET /api/dict/drawings` | — | `500` | handler 固定传 500；Store 层 `1..1000` 有效 |
| `GET /api/dict/items`、`GET /api/units` | — | 全部 | **非分页**，返回 `{items,total}` |
| `GET /api/forms/{id}/history` | — | 全部 | 非分页 |
| `GET /api/sample/pending` | — | `100` | 固定取 100 条 |

`page` / `page_size` 解析方式是 `strconv.Atoi`，仅当 `>0` 时生效（`page_size` 还需 `<=200`），否则用默认值，**不报错**。

### 1.6 幂等与并发约定

**幂等中间件**（`internal/idempotency/http`）作用于**所有需登录路由**上的写方法（`POST` / `PATCH` / `PUT` / `DELETE`）。

**幂等键来源**（按顺序取第一个非空）：

1. 请求头 `X-Idempotency-Key`
2. 请求头 `Idempotency-Key`
3. 请求体字段 `client_request_id`（字符串）
4. 请求体字段 `clientRequestId`
5. 请求体字段 `idempotency_key`
6. 请求体字段 `idempotencyKey`

> ⚠️ **`X-Request-Id` 不是幂等键**：服务端会为每个请求自动生成该头，用它当幂等键会导致永不命中。

**行为**：

| 场景 | 结果 |
|---|---|
| 未带幂等键 | 直接正常处理，无任何幂等记录 |
| 键命中 + 请求体指纹（`SHA-256(方法 + 路径 + 请求体)`）**一致** | **直接回放首次响应**（原状态码 + 原响应体），并加响应头 `X-Idempotent-Replay: true` |
| 键命中 + 请求体**不同** | **409** `conflict`，`details = {"idempotency_key": "<key>"}` |
| 首次处理但响应状态码 `>= 400` | **不缓存**（客户端可用同一个键安全重试） |
| 幂等表写入故障 | 不阻断业务（记日志后继续正常处理） |

- 作用域：`(user_id, idempotency_key)` 唯一，不同用户互不影响；路由维度用 gin 的 `FullPath()`（即路由模板，如 `/api/forms/:id`），所以同键打不同端点不会互相命中。
- TTL：`IDEMPOTENCY_TTL`，默认 **24h**（`idempotency.DefaultTTL`）；查询时用 `expires_at > now` 过滤，过期记录在每次 `Begin` 前顺带清理。
- 是否启用：`IDEMPOTENCY_ENABLED`（默认启用）；store 为 nil 时中间件直接放行。

**并发 / 乐观锁**：

- 更新既有表单时可在请求体传 `expected_save_count`（整数）。与当前 `save_count` 不一致时返回 **409**，`details = {"current": {...完整表单...}, "current_save_count": <int>}`，message 形如 `"该表单已被他人修改（期望保存次数 N，实际 M）"`。不传则不校验。
- 服务端在事务内按主键/表单号 `FOR UPDATE`（PostgreSQL；SQLite 依赖事务串行）加锁读取，避免并发丢更新。

### 1.7 批量原子性

- `POST /api/forms/batch` 在**同一事务**内依次保存 `forms` 数组里的每一张表单。
- **整批原子**：任意一张失败 → 返回该张的错误，**整批回滚**，已成功的也不会落库。
- 空数组或缺失 `forms` → **400** `"forms 不能为空"`（在 handler 层先拦）。
- 批量内每张表单的解析错误（未知字段等）也会在真正写库前被拦下，返回 **400** `"第 N 张表单: <原因>"`。
- 成功响应体带 `"atomic": true` 与 `count`。

---

## 2. 字段目录与渲染指引（前端最重要的一节）

字段目录的**唯一事实来源**是 `internal/formfields`（后端用它同时做：前端渲染、校验白名单、统计映射、数据库列生成）。
完整目录可直接取：`GET /api/form-fields`（全局）或 `GET /api/form-templates`（按工序带 layout）。

### 2.1 顶层栏目（5 个）

| 顺序 | 提交用字段名 | 字段目录里的 `code` | 中文名 | `kind` | 服务端计算 |
|---|---|---|---|---|---|
| 1 | `operation_code` | `operation` | 检验工序 | `text` | 否 |
| 2 | `total_qty` | `total_qty` | 检验总数 | `int` | 否 |
| 3 | `qualified_qty` | `qualified_qty` | 合格品 | `int` | 否 |
| 4 | `defect_qty` | `defect_qty` | 不合格品 | `int` | 否 |
| 5 | `pass_rate` | `pass_rate` | 合格率 | `number` | **是（`computed: true`）** |

> ⚠️ **命名不一致提示（务必注意）**：`GET /api/form-fields` 的 `header_columns[].code` 里"检验工序"用的是 `operation`（与纸质表头一致的缩写名）。
> 而**提交表单时用的字段名**、以及 `GET /api/forms/{id}` 响应里的字段名是 `operation_code`。渲染时可用 `operation` 定位列，提交时请写 `operation_code`（兼容写法 `operation` 也接受，见 §5.6）。
> `pass_rate` 标记 `computed: true`（服务端可由 合格品/检验总数 推导）。**提交 `pass_rate` 会覆盖服务端计算值**，除非该次请求显式给出它。

### 2.2 不良缺陷明细：35 列 × 3 表面 = 105 个子列

**每一项不良缺陷列都包含 凹面 / 凸面 / 双面 三个计数子列**，表面维度顺序固定为 `concave` → `convex` → `both`（与纸质表单列顺序一致）。
子列的扁平字段名规则：`<缺陷code>_<表面>`，例如 `prick_concave`。
这 105 个子列同时也是数据库 `form_lines` 表的真实列名（均为 `INTEGER NOT NULL DEFAULT 0`）。

| 序号 | 列名（中文） | 编码 `code` | `concave`（凹面） | `convex`（凸面） | `both`（双面） |
|---|---|---|---|---|---|
| 1 | 点伤 | `prick` | `prick_concave` | `prick_convex` | `prick_both` |
| 2 | 划伤 | `scratch` | `scratch_concave` | `scratch_convex` | `scratch_both` |
| 3 | 法兰边点伤 | `flange_prick` | `flange_prick_concave` | `flange_prick_convex` | `flange_prick_both` |
| 4 | 法兰边划伤 | `flange_scratch` | `flange_scratch_concave` | `flange_scratch_convex` | `flange_scratch_both` |
| 5 | 崩边 | `collapse_edge` | `collapse_edge_concave` | `collapse_edge_convex` | `collapse_edge_both` |
| 6 | 涂层瑕疵 | `coating_particle` | `coating_particle_concave` | `coating_particle_convex` | `coating_particle_both` |
| 7 | 亮点 | `bright_spot` | `bright_spot_concave` | `bright_spot_convex` | `bright_spot_both` |
| 8 | 裂 | `crack` | `crack_concave` | `crack_convex` | `crack_both` |
| 9 | 溢胶 | `overflow_glue` | `overflow_glue_concave` | `overflow_glue_convex` | `overflow_glue_both` |
| 10 | 变色不良颗粒 | `discolor_particle` | `discolor_particle_concave` | `discolor_particle_convex` | `discolor_particle_both` |
| 11 | 保护不良颗粒 | `protective_particle` | `protective_particle_concave` | `protective_particle_convex` | `protective_particle_both` |
| 12 | 旋涂颗粒 | `spin_particle` | `spin_particle_concave` | `spin_particle_convex` | `spin_particle_both` |
| 13 | 纤维 | `fiber` | `fiber_concave` | `fiber_convex` | `fiber_both` |
| 14 | 法兰边颗粒 | `flange_particle` | `flange_particle_concave` | `flange_particle_convex` | `flange_particle_both` |
| 15 | 加硬气泡 | `hardening_bubble` | `hardening_bubble_concave` | `hardening_bubble_convex` | `hardening_bubble_both` |
| 16 | 发丝裂 | `hairline_crack` | `hairline_crack_concave` | `hairline_crack_convex` | `hairline_crack_both` |
| 17 | 脏污 | `dirt` | `dirt_concave` | `dirt_convex` | `dirt_both` |
| 18 | 旋痕 | `spin_mark` | `spin_mark_concave` | `spin_mark_convex` | `spin_mark_both` |
| 19 | 凸点 | `raised_point` | `raised_point_concave` | `raised_point_convex` | `raised_point_both` |
| 20 | 凹坑 | `recess` | `recess_concave` | `recess_convex` | `recess_both` |
| 21 | 气泡 | `bubble` | `bubble_concave` | `bubble_convex` | `bubble_both` |
| 22 | 面形 | `shape` | `shape_concave` | `shape_convex` | `shape_both` |
| 23 | 水印 | `watermark` | `watermark_concave` | `watermark_convex` | `watermark_both` |
| 24 | 加硬点 | `hard_spot` | `hard_spot_concave` | `hard_spot_convex` | `hard_spot_both` |
| 25 | 法兰边圆点 | `flange_round_point` | `flange_round_point_concave` | `flange_round_point_convex` | `flange_round_point_both` |
| 26 | 严重法兰边伤 | `severe_flange_damage` | `severe_flange_damage_concave` | `severe_flange_damage_convex` | `severe_flange_damage_both` |
| 27 | 垃圾 | `garbage` | `garbage_concave` | `garbage_convex` | `garbage_both` |
| 28 | 法兰边伤 | `flange_damage` | `flange_damage_concave` | `flange_damage_convex` | `flange_damage_both` |
| 29 | 法兰边印痕 | `flange_imprint` | `flange_imprint_concave` | `flange_imprint_convex` | `flange_imprint_both` |
| 30 | 压痕/法兰边压痕 | `press_imprint` | `press_imprint_concave` | `press_imprint_convex` | `press_imprint_both` |
| 31 | 流痕 | `flow_mark` | `flow_mark_concave` | `flow_mark_convex` | `flow_mark_both` |
| 32 | 镜内褶皱 | `inner_fold` | `inner_fold_concave` | `inner_fold_convex` | `inner_fold_both` |
| 33 | 褶皱/法兰边褶皱 | `flange_fold` | `flange_fold_concave` | `flange_fold_convex` | `flange_fold_both` |
| 34 | 内挂流/挂流 | `inner_flow_glue` | `inner_flow_glue_concave` | `inner_flow_glue_convex` | `inner_flow_glue_both` |
| 35 | 法兰边凹痕 | `flange_recess` | `flange_recess_concave` | `flange_recess_convex` | `flange_recess_both` |

合计：顶层栏目 **5** + 缺陷列 **35 × 3 = 105** = **110** 个可提交字段。

### 2.3 如何用 `GET /api/form-templates` 直接渲染

`GET /api/form-templates` 返回**每个已登记工序**一条记录，每条记录里已经内嵌了可直接渲染的 `layout`：

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "operation": { "code": "GENERAL", "label": "一般检验", "sort_no": 0, "note": "通用模板：开放全部列，用于工序未确定或临时录入" },
        "layout": {
          "operation": { "code": "GENERAL", "label": "一般检验", "sort_no": 0, "note": "..." },
          "header_columns": [ { "code": "operation", "label": "检验工序", "kind": "text", "computed": false } ],
          "defect_group": {
            "title": "不良缺陷明细",
            "columns": [ { "code": "prick", "label": "点伤" } ]
          },
          "surfaces": ["concave", "convex", "both"],
          "surface_labels": { "concave": "凹面", "convex": "凸面", "both": "双面" },
          "defect_columns": [
            { "field": "prick_concave", "defect": "prick", "label": "点伤", "surface": "concave", "header": "点伤-凹面" }
          ]
        },
        "field_count": 110
      }
    ],
    "total": 3,
    "note": "每个工序对应一张表单；不同工序所填内容不同，前端可按工序渲染对应列。未登记的工序按通用模板处理（开放全部列，不校验必填）"
  }
}
```

**推荐的渲染流程**：

1. 页面加载时拉一次 `GET /api/form-templates`，用 `items[].operation.code` 生成"工序"下拉框（`label` 为显示名，`sort_no` 排序）。
2. 用户选中某工序后：
   - 若该工序在 `items` 里 → 直接用它的 `layout`；
   - 若不在（**未登记工序**，见 §8.2）→ 再调 `GET /api/form-templates/{code}`，服务端会返回该编码的回退模板（`label` = 编码本身、`sort_no` = 100），`layout` 仍是完整全列。
3. 渲染规则：
   - 先渲染 `layout.header_columns`（5 个顶层栏目；`computed: true` 的 `pass_rate` 建议只读展示）。
   - 再渲染 `layout.defect_group.title`（"不良缺陷明细"）作为分组标题，用 `layout.defect_columns`（105 项，**已按"列 → 凹/凸/双"展开且顺序固定**）直接顺序渲染输入格；每格的 `header` 就是现成的表头文案（如 `点伤-凹面`），`field` 就是提交时的字段名。
   - 或按纸质表头渲染：用 `layout.defect_group.columns`（35 项）做列头，每列下用 `layout.surfaces` + `layout.surface_labels` 展开 3 个子格，字段名 = `<code>_<surface>`。
4. 提交时字段名照抄 `field` / 顶层 `code`（`operation` 请映射为 `operation_code`，见 §2.1 的提示）。
5. 也支持"只提交本次改动的格子"（三态合并，见 §3.5）。

> 说明：`layout` 里的 `operation` 键出现在 `GET /api/form-templates`、`GET /api/form-templates/{code}` 与 `GET /api/forms/{id}` 的 `layout` 中；`GET /api/form-fields` 的返回**不含** `operation`。

---

## 3. 错误与校验语义

### 3.1 `enforce` 参数

| 值 | 说明 |
|---|---|
| `warn` | **默认**。所有校验只产出 `level: "warning"` 的 issue，**不阻断保存**，数据照常落库 |
| `strict` | 把"自相矛盾"的校验升级为 `level: "error"`，**有任意 error 即整体失败**：返回 **422** `validation_failed`，`details` 是 error 级 issue 数组，该次保存（含批量整批）全部回滚 |

- 解析规则（`ParseEnforce`）：`TrimSpace` 后 `EqualFold("strict")` 才算 strict；**缺省、空串、任何非法值一律回落 `warn`**（不报错）。
- 校验结果无论如何都会在成功响应里回传：`issues`（全部）、`warnings`、`errors`、`missing_fields`、`is_complete`。
- issue 结构（`httpx.Issue`）：

```json
{ "rule": "V-3", "level": "warning", "field": "lines[0].defect_qty", "message": "第 1 行：「点伤」合计(5) 超过不合格品数(2)" }
```

`field` 为 `omitempty`（可能缺失）。**明细行的 `field` 前缀是 0 基索引**：第 1 行不加前缀（如 `total_qty`），第 2 行是 `lines[1].total_qty`。

### 3.2 校验规则逐条（V-1 ~ V-10）

| 规则 | 含义 | 默认级别 | `enforce=strict` 是否阻断 | 备注 |
|---|---|---|---|---|
| **V-1** | 合格品 + 不合格品 = 检验总数（**行级**：`qualified_qty + defect_qty == total_qty`） | `warning` | **是**（升级为 error） | 三者都为 0 时**不提示**（避免空行噪声） |
| **V-2** | 缺陷子项合计 = 不合格品 | `warning` | **否，任何模式都不阻断** | 已确认同一片镜片可计入多个缺陷类别；`defect_qty == 0 且 合计 == 0` 时不提示 |
| **V-3** | 缺陷子项边界（**行级**，两种）：① 某缺陷列凹+凸+双之和 > 不合格品数；② 某子格计数 > 检验总数 | `warning` | **是**（升级为 error） | ① 仅在 `defect_qty > 0` 时判定；② 仅在 `total_qty > 0` 时判定；**若该行存在任何负值（V-4 命中）则整块跳过**，避免重复报错 |
| **V-4** | 非负整数（`total_qty` / `qualified_qty` / `defect_qty` 及 105 个缺陷子格） | `warning` | **是**（升级为 error） | 只取第一个负值字段报一条 issue |
| **V-5** | 合格率派生 | 仅计算，**不产生 issue** | 否 | 由服务端按 `qualified_qty / total_qty * 100` 计算并保留 2 位小数，`total_qty <= 0` 时为 `null` |
| **V-6** | 时间顺序（`submitted_at` 不得晚于 `last_saved_at`，也不得晚于当前时间） | `warning` | **否，任何模式都不阻断** | 属表头级校验，永远只警告 |
| **V-7** | 唯一性（`form_no` 唯一；`form_lines(form_id, seq)` 唯一） | **硬阻断** | **不适用 `enforce`，永远阻断** | 由数据库唯一索引保证；冲突时由 handler 映射为 **409** `conflict`，message 形如 `"表单号已存在: <form_no>（V-7：表单号唯一）"` 或 `"明细行序号重复（V-7：(form_id, seq) 唯一）"` |
| **V-8** | 样品信息与参考部件号清单不匹配 | `warning`（语义） | **否，任何模式都不阻断** | ⚠️ **当前实现不会产生 issue**：不匹配只在 `POST /api/sample/parse` 的 `matched` / `unmatched_reason` / `note` 里体现，保存接口不做该判定，也不影响落库 |
| **V-9** | 样品结构化项缺失（当前判据：`sample_project` 为空） | `warning` | **否，任何模式都不阻断** | 属表头级校验；message 为 `"样品信息的「项目」（M1/M2 等）尚未填写"` |
| **V-10** | 表单栏目缺失 | `warning` | **否，任何模式都不阻断** | 仅当"已经开始填了"（`operation_code` / `total_qty` / `qualified_qty` / `defect_qty` 非 0、或 `sample_raw` 非空、或已有明细行）才提示，避免空表单噪声 |

### 3.3 「任何模式都不阻断」的规则汇总

**V-2、V-6、V-8、V-9、V-10 在任何 `enforce` 取值下都不会阻断保存**（V-5 只是计算，不产生 issue）。
只有 **V-1、V-3、V-4** 会在 `enforce=strict` 时阻断；**V-7** 是唯一与 `enforce` 无关的硬阻断（409）。

### 3.4 `missing_fields` / `is_complete` 的判定口径

`MissingFields` 依次检查 4 项，缺失即加入数组：

| 顺序 | 字段 | 判定为空的条件 |
|---|---|---|
| 1 | `operation_code` | `TrimSpace` 后为空 |
| 2 | `unit_id` | `unit_id` 与 `unit_text` **都**为空（这个键代表"单位"整体，缺失时字段名仍报 `unit_id`） |
| 3 | `sample_raw` | `TrimSpace` 后为空 |
| 4 | `lines` | `len(lines) == 0` |

`is_complete = (len(missing_fields) == 0)`。

### 3.5 字段级合并的三态语义

保存接口的 `form` 对象是**字段级补丁**，不是整体覆盖。前端只需提交"这次要改的字段"。

| 情况 | 字符串类字段（`sample_raw` / `sample_project` / `unit_id` / `operation_text` / `remark` …） | 整数类字段（`total_qty` / `qualified_qty` / `defect_qty`） | 浮点字段（`pass_rate`） | 缺陷子格（`defects` 里的值） |
|---|---|---|---|---|
| **键不存在** | 不改，保持原值 | 不改，保持原值 | 不改，保持原值 | 不改，保持原值 |
| **显式 `null`** | **清空**（指针对字段置 `null`；`sample_raw` 这类非空字段置空串） | **置 0**（整数没有"空"的表示，服务端把 `null` 当 0 写入） | **清空**（置 `null`，之后若本次未显式给 `pass_rate` 会按 合格品/检验总数 重算） | **置 0**（等价于删除该格；若整列三格都被清空，该列键从结果里移除） |
| **有值** | 写入原值（服务端不做枚举校验） | 写入该整数 | 写入该数值，**并阻止本轮的自动重算** | 写入该整数 |

**`defects` 的特殊规则**：

- 只改**给出的格子**，未给出的格子保持原值（`defects` 是"编码 → 表面 → 值"的三层结构）。
- 显式给**空对象 `{}`** → **清空该行全部缺陷计数**（等价于所有格子归零）。
- `defects` 里出现**未登记的缺陷编码**或**非法表面维度**（非 `concave` / `convex` / `both`）→ **400** `"无法识别的不良缺陷列: xxx（合法列见 GET /api/form-fields）"` 或 `"缺陷列 xxx 的表面维度不合法: yyy（合法值: concave/convex/both）"`。
- 也支持**扁平写法**：直接在 `form` 顶层（或 `lines[i]` 里）写 `prick_concave: 1` 这类扁平字段名，服务端会自动归并进 `defects`。
- 扁平字段的值必须是整数或 `null`，否则 **400** `"缺陷列 xxx 必须是整数或 null"`。

**`lines` 的特殊规则**：

- `lines` **一旦给出（即使给 `[]`）即整笔替换**该表单的全部明细行 —— 不做行级合并、不按 `id`/`seq` 匹配。
- `lines` **键不存在或显式 `null`** → 明细保持不变（新建表单则为空明细）。
- 替换时服务端会 **DELETE 旧行并逐行 INSERT**，因此明细行的 `id` 每次替换都会重新生成；`seq` 缺省时按数组顺序自动编号 `1, 2, 3...`。
- 行内每个字段本身也是三态语义（同上表），行内 `defects` 规则与上述 `defects` 规则一致。
- `lines[i]` 出现未知键 → **400** `"第 N 行存在无法识别的字段: [xxx]"`（行号 **1 基**）。

**表头兼容写法**：`form` 可以省略，直接把栏目写在请求体顶层（除 `form_no` / `operation_code` / `mode` / `enforce` / `reason` / `expected_save_count` / `client_request_id` / `forms` 这些控制字段外），服务端会把整个请求体当作 `form` 解析。

---

## 4. 接口清单表

共 **40** 个端点（含根路径 2 个非 `/api` 端点）。

### 4.1 根路径（无需登录）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/healthz` | 健康检查（**不走统一响应包装**） | 否 | 否 |
| GET | `/` | 服务信息与文档索引（**不走统一响应包装**） | 否 | 否 |

### 4.2 认证与用户

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| POST | `/api/auth/login` | 登录，下发 Cookie + 返回 token | 否 | 否 |
| POST | `/api/auth/logout` | 注销当前会话并清 Cookie | 是 | 否 |
| GET | `/api/auth/me` | 当前用户信息 | 是 | 否 |
| POST | `/api/auth/change-password` | 修改自己的密码（旧会话全部失效） | 是 | 否 |
| GET | `/api/users` | 用户列表（分页） | 是 | **是** |
| POST | `/api/users` | 新建用户 | 是 | **是** |
| PATCH | `/api/users/{id}` | 更新用户（显示名/工号/单位/管理员/状态/密码） | 是 | **是** |

### 4.3 字段目录与工序表单模板

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/form-templates` | 工序表单模板列表 + 每个工序的完整 layout | 是 | 否 |
| GET | `/api/form-templates/{code}` | 单个工序的模板与 layout（未登记工序走回退） | 是 | 否 |
| GET | `/api/form-fields` | 字段目录（5 顶层栏目 + 35 缺陷列 × 3 表面） | 是 | 否 |

### 4.4 表单提交（核心）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/forms` | 表单列表（分页 + 多条件筛选） | 是 | 否 |
| POST | `/api/forms` | **新建或按 `form_no` 合并保存**（表单号不存在即创建）→ **201** | 是 | 否 |
| POST | `/api/forms/batch` | 批量保存多张表单（**整批原子**）→ **200** | 是 | 否 |
| GET | `/api/forms/{id}` | 取详情（`{id}` 可为提交 ID **或** 表单号） | 是 | 否 |
| PATCH | `/api/forms/{id}` | 按提交 ID 做字段级合并更新 → **200**（ID 不存在 → 404） | 是 | 否 |
| POST | `/api/forms/{id}/void` | 逻辑作废（数据与历史保留） | 是 | 否 |
| GET | `/api/forms/{id}/history` | 历史快照列表（按 `save_count` 倒序） | 是 | 否 |
| GET | `/api/forms/{id}/history/diff` | 两个版本之间的字段级差异 | 是 | 否 |

### 4.5 样品信息

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| POST | `/api/sample/parse` | 解析原始串（能命中缓存） | 是 | 否 |
| POST | `/api/sample/confirm` | 人工确认/修正结构化字段（不改写原始串） | 是 | 否 |
| GET | `/api/sample/cache` | 查看某原始串的规范化键、缓存命中与 token | 是 | 否 |
| GET | `/api/sample/pending` | 待核对清单（样品未拆解出「项目」的 ACTIVE 表单，固定 100 条） | 是 | 否 |

### 4.6 字典 / 单位 / 参考部件号

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/dict/items` | 字典项查询（按 `category` 过滤） | 是 | 否 |
| POST | `/api/dict/items` | 新增或更新字典项（按 `category` + `code`） | 是 | 否 |
| PATCH | `/api/dict/items/{id}` | 按 ID 更新字典项（改标签/排序/停用） | 是 | 否 |
| GET | `/api/dict/categories` | 字典类别清单 | 是 | 否 |
| GET | `/api/units` | 单位清单 | 是 | 否 |
| POST | `/api/units` | 新增或更新单位（按 `code`） | 是 | 否 |
| GET | `/api/dict/drawings` | 参考部件号清单（仅供参考） | 是 | 否 |
| POST | `/api/dict/drawings/import` | 批量导入参考部件号 | 是 | 否 |

### 4.7 汇总统计

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/stats/dimensions` | 可用的 `group_by` 维度清单 | 是 | 否 |
| GET | `/api/stats/summary` | 按维度分组汇总（含总计） | 是 | 否 |
| GET | `/api/stats/defects` | 不良缺陷分布（35 列 × 3 表面） | 是 | 否 |
| GET | `/api/stats/workload` | 按录入人（`submitted_by` / `last_saved_by`）统计工作量 | 是 | 否 |
| GET | `/api/stats/incomplete-forms` | 未填完整清单 | 是 | 否 |

### 4.8 操作日志

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/audit-logs` | 操作日志查询（按 `entity` / `entity_id` 过滤） | 是 | 否 |

### 4.9 尚未实现的接口（不要按已实现对接）

| 能力 | 状态 |
|---|---|
| **数据导出（Excel / CSV / PDF 下载）** | **未实现**。后端没有任何 export / download 路由；唯一的 CSV 相关代码是 `cmd/seed` 启动时读取 `data/reference_drawings.csv` 的种子脚本。前端如需导出，请取 `GET /api/forms` / `GET /api/stats/*` 的数据自行在前端生成文件。 |
| **删除表单（DELETE）** | **未实现**。只有"逻辑作废" `POST /api/forms/{id}/void`；历史与数据一律保留。 |
| **明细行的独立增删改端点** | **未实现**。明细只能通过保存接口的 `lines` 整体替换（见 §3.5）。 |
| **审批流 / 状态流转** | **未实现**。提交即生效，只有 `ACTIVE` / `VOID` 两种状态。 |
| **管理员删除用户** | **未实现**。只能通过 `PATCH /api/users/{id}` 把 `status` 置为 `disabled`。 |

---

## 5. 逐接口详情

> 所有示例中的 `code` / `message` / `data` 包装、字段名均与代码一致。为节省篇幅，部分示例用 `"..."` 占位长数组/长对象。

### 5.1 根路径

#### `GET /healthz`（无需登录，**不走统一包装**）

```json
{ "status": "ok", "time": "2026-02-06T08:15:00Z" }
```

#### `GET /`（无需登录，**不走统一包装**）

```json
{
  "name": "lens-qs-api",
  "version": "0.1.0",
  "docs": {
    "requirements": "docs/requirements.md",
    "design": "docs/design.md",
    "features": "docs/features.md",
    "api": "docs/api.md"
  }
}
```

### 5.2 认证与用户

#### `POST /api/auth/login`（公开）

请求体：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `username` | string | **是** | 登录名（`binding:"required"`） |
| `password` | string | **是** | 密码（`binding:"required"`） |

```json
{ "username": "admin", "password": "admin12345" }
```

响应 200（同时 `Set-Cookie: <COOKIE_NAME>=<token>; Path=/; Max-Age=43200; HttpOnly; SameSite=Lax`）：

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "token": "Vq0m1n...base64url...",
    "expires_at": "2026-02-06T20:15:00Z",
    "user": {
      "id": "u_01H...",
      "username": "admin",
      "display_name": "管理员",
      "employee_no": "E001",
      "unit_id": "u_01Hunit...",
      "is_admin": true,
      "must_change_pwd": false,
      "status": "active",
      "failed_attempts": 0,
      "locked_until": null,
      "last_login_at": "2026-02-06T08:15:00Z",
      "created_at": "2026-01-01T00:00:00Z",
      "updated_at": "2026-02-06T08:15:00Z"
    },
    "note": "浏览器可用 Cookie；其它调用方可使用 Authorization: Bearer <token>"
  }
}
```

> `user` 里**不含** `password_hash`（json tag 为 `json:"-"`）。

典型错误：
- `400 bad_request`：缺少 `username` / `password`，或 JSON 不合法（message：`"请求体格式不正确: ..."`）。
- `401 unauthorized`：用户名或密码错误；账号被锁定期间也会被拒（响应里可见 `locked_until`）。

#### `POST /api/auth/logout`（需登录）

请求体：无（可为空）。响应 200：

```json
{ "code": "ok", "message": "", "data": { "logged_out": true } }
```

同时下发过期 Cookie（`Max-Age=-1`）清除会话。

#### `GET /api/auth/me`（需登录）

响应 200：

```json
{ "code": "ok", "message": "", "data": { "user": { "id": "u_01H...", "username": "admin", "display_name": "管理员", "is_admin": true, "status": "active", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z" }, "is_admin": true } }
```

典型错误：`404 not_found` `"用户不存在"`（会话存在但用户已被删）。

#### `POST /api/auth/change-password`（需登录）

| 字段 | 类型 | 必填 |
|---|---|---|
| `old_password` | string | **是** |
| `new_password` | string | **是** |

```json
{ "old_password": "admin12345", "new_password": "newSecret987" }
```

响应 200（**原会话全部失效并清 Cookie**）：

```json
{ "code": "ok", "message": "", "data": { "changed": true, "note": "密码已修改，原会话已全部失效，请重新登录" } }
```

#### `GET /api/users`（需登录 + **管理员**）

查询参数：`keyword`（模糊）、`page`、`page_size`（默认 1 / 20，`page_size` 上限 200）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [ { "id": "u_01H...", "username": "op1", "display_name": "张三", "is_admin": false, "status": "active", "created_at": "2026-01-02T01:00:00Z", "updated_at": "2026-01-02T01:00:00Z" } ],
    "total": 1,
    "page": 1,
    "page_size": 20
  }
}
```

典型错误：`403 forbidden` `"该操作仅管理员可执行"`。

#### `POST /api/users`（需登录 + **管理员**）

| 字段 | 类型 | 必填 | 默认 |
|---|---|---|---|
| `username` | string | **是** | — |
| `password` | string | **是** | — |
| `display_name` | string | 否 | `""` |
| `employee_no` | string | 否 | 空串视为不设置（`null`） |
| `unit_id` | string | 否 | 空串视为不设置（`null`） |
| `is_admin` | bool | 否 | `false` |

响应 **201** `created`，`data` 为新建的 user 对象（结构同 `GET /api/auth/me` 里的 `user`）。`must_change_pwd` 固定写 `false`。

典型错误：`400`（如用户名已存在，由服务层判定）、`403`（非管理员）。

#### `PATCH /api/users/{id}`（需登录 + **管理员**）

所有字段可选，**只有出现的字段会被更新**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `display_name` | string\|null | 显示名 |
| `employee_no` | string\|null | 工号 |
| `unit_id` | string\|null | 所属单位 |
| `is_admin` | bool\|null | 管理员标记 |
| `status` | string\|null | 例如 `active` / `disabled` |
| `password` | string\|null | 重置密码 |

```json
{ "status": "disabled" }
```

响应 200，`data` 为更新后的 user 对象。

副作用与限制：
- `status=disabled`（大小写不敏感）或提供 `password` 时，会 **撤销该用户的全部会话**（`RevokeAll`）。
- 不能取消**自己**的管理员权限 → `400` `"不能取消自己的管理员权限"`。
- 不能停用**自己**的账号 → `400` `"不能停用自己的账号"`。

### 5.3 字段目录与工序表单模板

#### `GET /api/form-fields`（需登录）

无参数。响应 200：

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "header_columns": [
      { "code": "operation", "label": "检验工序", "kind": "text", "computed": false },
      { "code": "total_qty", "label": "检验总数", "kind": "int", "computed": false },
      { "code": "qualified_qty", "label": "合格品", "kind": "int", "computed": false },
      { "code": "defect_qty", "label": "不合格品", "kind": "int", "computed": false },
      { "code": "pass_rate", "label": "合格率", "kind": "number", "computed": true }
    ],
    "surfaces": ["concave", "convex", "both"],
    "surface_labels": { "concave": "凹面", "convex": "凸面", "both": "双面" },
    "defect_group": {
      "title": "不良缺陷明细",
      "columns": [
        { "code": "prick", "label": "点伤" },
        { "code": "scratch", "label": "划伤" }
      ]
    },
    "defect_columns": [
      { "field": "prick_concave", "defect": "prick", "label": "点伤", "surface": "concave", "header": "点伤-凹面" },
      { "field": "prick_convex", "defect": "prick", "label": "点伤", "surface": "convex", "header": "点伤-凸面" },
      { "field": "prick_both", "defect": "prick", "label": "点伤", "surface": "both", "header": "点伤-双面" }
    ],
    "stats": { "header_count": 5, "defect_count": 35, "total_columns": 110 },
    "note": "不良缺陷明细下的每一项都包含凹面/凸面/双面三个子列"
  }
}
```

> 实际返回的 `defect_group.columns` 是完整 **35** 项、`defect_columns` 是完整 **105** 项（上例只截了前几项示意）。

#### `GET /api/form-templates`（需登录）

无参数。返回 `data.items`（每个已登记工序一项）、`data.total`（项数）、`data.note`。每项结构：

| 字段 | 类型 | 说明 |
|---|---|---|
| `operation` | object | 模板：`{code, label, allowed_fields?, sort_no, note?}` |
| `layout` | object | 完整列布局（含 `operation` / `header_columns` / `defect_group` / `surfaces` / `surface_labels` / `defect_columns`） |
| `field_count` | int | `5 + 105 = 110` |

`operation.allowed_fields` **只在非空时出现**（当前内置模板全部为空，即"开放全部列"）。

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [
      { "operation": { "code": "GENERAL", "label": "一般检验", "sort_no": 0, "note": "通用模板：开放全部列，用于工序未确定或临时录入" },
        "layout": { "operation": { "code": "GENERAL", "label": "一般检验", "sort_no": 0, "note": "..." },
                    "header_columns": [ "..." ], "defect_group": { "title": "不良缺陷明细", "columns": [ "..." ] },
                    "surfaces": ["concave", "convex", "both"],
                    "surface_labels": { "concave": "凹面", "convex": "凸面", "both": "双面" },
                    "defect_columns": [ "..." ] },
        "field_count": 110 },
      { "operation": { "code": "一次加硬", "label": "一次加硬", "sort_no": 10 }, "layout": { "..." : "..." }, "field_count": 110 },
      { "operation": { "code": "二次加硬", "label": "二次加硬", "sort_no": 20 }, "layout": { "..." : "..." }, "field_count": 110 }
    ],
    "total": 3,
    "note": "每个工序对应一张表单；不同工序所填内容不同，前端可按工序渲染对应列。未登记的工序按通用模板处理（开放全部列，不校验必填）"
  }
}
```

#### `GET /api/form-templates/{code}`（需登录）

路径参数 `code` 为**工序编码**（中文也可，如 `一次加硬`；URL 需百分号编码）。**永不 404**：未登记的编码按回退模板返回。响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "operation": { "code": "三次加硬", "label": "三次加硬", "sort_no": 100, "note": "未登记的工序：按通用模板处理（开放全部列）" },
    "layout": { "operation": { "code": "三次加硬", "label": "三次加硬", "sort_no": 100, "note": "未登记的工序：按通用模板处理（开放全部列）" },
                "header_columns": [ "..." ], "defect_group": { "title": "不良缺陷明细", "columns": [ "..." ] },
                "surfaces": ["concave", "convex", "both"],
                "surface_labels": { "concave": "凹面", "convex": "凸面", "both": "双面" },
                "defect_columns": [ "..." ] }
  }
}
```

### 5.4 `GET /api/forms` —— 表单列表

**查询参数**（全部可选）：

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `page` | int | `1` | 页码，`>0` 生效 |
| `page_size` | int | `20` | 每页条数，`1..200` 生效 |
| `state` | string | `"ACTIVE"` | `ACTIVE` / `VOID`（服务端 `ToUpper`）；传空串则**仍按默认 ACTIVE** |
| `keyword` | string | — | 模糊匹配 **`form_no` 或 `sample_raw`** |
| `operation_code` | string | — | 精确匹配 |
| `sample_project` / `sample_model` / `sample_spec` / `sample_size` / `sample_side` | string | — | 精确匹配 |
| `unit` | string | — | 精确匹配 `COALESCE(unit_text, unit_id)` |
| `from` | string | — | `created_at >= from`（**注意用的是 `created_at`，不是 `submitted_at`**） |
| `to` | string | — | `created_at <= to + "T23:59:59Z"`（服务端自动拼时间后缀） |
| `incomplete` | string | — | 仅当**恰好等于 `"true"`** 时只看 `is_complete = 0` |

**排序**：`created_at DESC`。**响应**：

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [
      {
        "id": "f_01HZZ...",
        "form_no": "260206-001",
        "operation_code": "一次加硬",
        "operation_text": null,
        "sample_raw": "S1-MD-R-VID2",
        "sample_project": "M1",
        "sample_model": "VID2",
        "sample_spec": "S1",
        "sample_size": "MD",
        "sample_side": "R",
        "unit_id": null,
        "unit_text": "片",
        "submitted_by": "u_01H...",
        "submitted_at": "2026-02-06T08:20:00Z",
        "last_saved_by": "u_01H...",
        "last_saved_at": "2026-02-06T08:20:00Z",
        "save_count": 1,
        "total_qty": 100,
        "qualified_qty": 95,
        "defect_qty": 5,
        "pass_rate": 95,
        "is_complete": true,
        "missing_fields": [],
        "state": "ACTIVE",
        "created_at": "2026-02-06T08:20:00Z",
        "updated_at": "2026-02-06T08:20:00Z",
        "lines": []
      }
    ],
    "total": 1, "page": 1, "page_size": 20
  }
}
```

> ⚠️ 列表接口返回的每一项 **`lines` 恒为空数组 `[]`**（列表查询只读 `forms` 表，不联查明细）。**要拿明细必须调 `GET /api/forms/{id}`**。

典型错误：`401`（未登录）。

### 5.5 `POST /api/forms` —— 新建或按 `form_no` 合并保存（核心）

**成功状态码**：**201 Created**（无论实际是新建还是合并更新）。

**请求体字段**：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `form_no` | string | 否 | 表单号。**为空时服务端自动生成**；非空时"找到即合并、找不到即创建" |
| `operation_code` | string | 否 | 工序编码。**只在"创建新表单"时用于初始化** `operation_code`；对已存在的表单无效（要改工序请写在 `form.operation_code` 里） |
| `mode` | string | 否 | 请求结构里有该字段，但**服务端从不读取它**（明细替换只看 `lines` 是否出现）。请传 `"merge"` 或省略，**不要指望 `"replace"` 改变行为** |
| `enforce` | string | 否 | `warn`（默认）/ `strict`；非 `strict` 的值一律按 `warn` 处理 |
| `reason` | string | 否 | 变更原因，写入历史快照与操作日志 |
| `expected_save_count` | int\|null | 否 | 乐观锁；不匹配 → 409（新建时忽略） |
| `client_request_id` | string | 否 | **幂等键**（也可用请求头 `X-Idempotency-Key`） |
| `form` | object\|null | 否 | **字段级补丁**（见 §3.5）。省略或为 `null` 时，服务端把**整个请求体**当作补丁解析（即顶层直接写栏目） |
| `forms` | array | 否 | 仅批量接口使用（单笔接口传了会被忽略） |

**`form` 内允许的键**（服务端白名单，出现其它键 → 400）：

```
operation_code, operation, operation_text,
total_qty, qualified_qty, defect_qty, pass_rate,
sample_raw, sample_project, sample_model, sample_spec, sample_size, sample_side,
unit_id, unit_text, remark, defects, lines
```

外加 **105 个扁平缺陷字段名**（如 `prick_concave`）也合法。
`lines[i]` 内允许的键：`seq, total_qty, qualified_qty, defect_qty, defects, remark`（外加 105 个扁平缺陷字段名）。

**服务端保存语义（按执行顺序）**：

1. **解析补丁**：未知键 → **400** `"存在无法识别的字段: xxx, yyy"`，`details = {"unknown_fields": [...], "hint": "列清单见 GET /api/form-fields"}`。
2. **定位或创建**：`form_no` 空白 → 自动生成 `{YYMMDD}-{3位当日流水}`（如 `260206-001`；取当日已有 `form_no` 最大值 +1）；否则按 `form_no` 查——**不存在则创建**（`save_count = 1`，`state = ACTIVE`），**存在则复用并合并**。
3. **乐观锁**：非新建且传了 `expected_save_count` 且与当前 `save_count` 不等 → **409**。
4. **应用补丁**（字段级合并），`save_count` 自增（新建为 1）。
5. **留痕**：`submitted_by` / `submitted_at` 仅在为空时写入（首次录入人）；`last_saved_by` / `last_saved_at` 每次覆盖为当前用户/时间。
6. **明细**：`lines` 出现 → 整笔替换；否则沿用原明细。
7. **汇总**：
   - 若结果有明细 → 表头 `total_qty` / `qualified_qty` / `defect_qty` 默认按明细行**求和重算**；但本次补丁里**显式给出**的那一项**优先**（不覆盖）。三者独立判定。
   - `pass_rate`：本次补丁**未显式给出** `pass_rate` 时，按 `qualified_qty / total_qty * 100`（2 位小数）重算；`total_qty <= 0` 时置 `null`。
8. **完成度与校验**：算 `missing_fields` / `is_complete`；跑 V-1..V-10；有 error 级 issue → **422**（整笔回滚）。
9. **落库与留痕**：新建走 INSERT；更新则先写 `form_history` 快照（保存前的表头 + 明细），再 UPDATE，再（若给了 `lines`）替换明细；最后写 `audit_logs`（`action` = `create_form` 或 `save_form`，`entity` = `form`，`after` = 原始请求体）。

**响应 201**：

```json
{
  "code": "ok", "message": "",
  "data": {
    "action": "create_form",
    "submission": {
      "id": "f_01HZZ...", "form_no": "260206-001", "operation_code": "一次加硬", "operation_text": null,
      "sample_raw": "S1-MD-R-VID2", "sample_project": "M1", "sample_model": "VID2", "sample_spec": "S1",
      "sample_size": "MD", "sample_side": "R", "unit_id": null, "unit_text": "片",
      "submitted_by": "u_01H...", "submitted_at": "2026-02-06T08:20:00Z",
      "last_saved_by": "u_01H...", "last_saved_at": "2026-02-06T08:20:00Z", "save_count": 1,
      "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95,
      "is_complete": true, "missing_fields": [],
      "state": "ACTIVE", "created_at": "2026-02-06T08:20:00Z", "updated_at": "2026-02-06T08:20:00Z",
      "lines": [ { "id": "l_01HZZ...", "form_id": "f_01HZZ...", "seq": 1, "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "defects": { "prick": { "concave": 1 } }, "created_at": "2026-02-06T08:20:00Z", "updated_at": "2026-02-06T08:20:00Z" } ]
    },
    "issues": [],
    "warnings": [],
    "errors": [],
    "missing_fields": [],
    "is_complete": true
  }
}
```

- `action` 取值：**`create_form`**（新建）/ **`save_form`**（合并到已有表单）。
- `issues` / `warnings` / `errors` 为 `httpx.Issue` 数组；成功时通常为空数组（warn 模式下可能带 warning）。

**典型错误**：

| 场景 | 状态码 | `code` | message / details |
|---|---|---|---|
| 未知字段 | 400 | `bad_request` | `"存在无法识别的字段: foo"`，`details.unknown_fields` |
| 非法缺陷编码/表面 | 400 | `bad_request` | `"无法识别的不良缺陷列: xxx（合法列见 GET /api/form-fields）"` |
| 扁平缺陷值不是整数 | 400 | `bad_request` | `"缺陷列 prick_concave 必须是整数或 null"` |
| `lines` 元素含未知键 | 400 | `bad_request` | `"第 2 行存在无法识别的字段: [foo]"` |
| 表单号唯一约束（并发抢建同名） | 409 | `conflict` | `"表单号已存在: <form_no>（V-7：表单号唯一）"` |
| `expected_save_count` 不匹配 | 409 | `conflict` | `details = {"current": {...}, "current_save_count": 3}` |
| `strict` 下有 error 级 issue | 422 | `validation_failed` | `details` 为 error 级 issue 数组 |

### 5.6 `PATCH /api/forms/{id}` —— 按 ID 字段级合并更新

**请求体与语义和 `POST /api/forms` 完全一致**，区别如下：

| 差别 | POST `/api/forms` | PATCH `/api/forms/{id}` |
|---|---|---|
| 定位方式 | 按 `form_no` 找/建（`form_no` 空则自动生成） | **按路径里的提交 ID** 直接定位 |
| 成功状态码 | **201** | **200** |
| ID 不存在 | — | **404** `not_found` `"表单提交不存在"` |
| 请求体里的 `form_no` | 用于定位/命名 | **仍会被应用**（可借此改表单号，需保证唯一） |
| `expected_save_count` | 有效 | 有效 |

```json
{ "enforce": "warn", "reason": "补填合格品", "form": { "qualified_qty": 96 } }
```

响应 200：结构与 `POST /api/forms` 的 `data` 相同（`action` 为 `save_form`）。

**兼容写法**：`form` 里既接受 `operation_code`，也接受 `operation`（后者是兼容别名）。若两者都出现，**`operation_code` 优先**。

**为什么 `{id}` 里也能填表单号？** `GET /api/forms/{id}` 与 `GET /api/forms/{id}/history*` 在按 ID 查不到时会**回退按表单号查**；但 `PATCH` / `void` **只按 ID**，传表单号会 404。

### 5.7 `POST /api/forms/batch` —— 批量保存（整批原子）

请求体：与单笔相同的控制字段（`enforce` / `reason` / `expected_save_count` / `client_request_id` 会作为**该批次的公共控制字段复制到每一张**），外加：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `forms` | array | **是** | 数组元素就是单笔的"栏目对象"（即单笔请求里的 `form` 内容），元素内可自带 `form_no` |

```json
{
  "enforce": "warn",
  "reason": "早班集中录入",
  "forms": [
    { "form_no": "260206-001", "operation_code": "一次加硬", "sample_raw": "S1-MD-R-VID2", "sample_project": "M1",
      "lines": [ { "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "defects": { "prick": { "concave": 1 } } } ] },
    { "form_no": "260206-002", "operation_code": "二次加硬", "sample_raw": "S2-LG-L-VID1",
      "lines": [ { "total_qty": 50, "qualified_qty": 50, "defect_qty": 0 } ] }
  ]
}
```

响应 **200**：

```json
{
  "code": "ok", "message": "",
  "data": {
    "results": [
      { "action": "create_form", "submission": { "id": "f_01H...", "form_no": "260206-001", "save_count": 1, "lines": [ "..." ] }, "issues": [], "warnings": [], "errors": [], "missing_fields": [], "is_complete": true },
      { "action": "create_form", "submission": { "id": "f_01H...", "form_no": "260206-002", "save_count": 1, "lines": [ "..." ] }, "issues": [], "warnings": [], "errors": [], "missing_fields": [], "is_complete": true }
    ],
    "count": 2,
    "atomic": true
  }
}
```

**典型错误**：
- `400` `"forms 不能为空"`（`forms` 缺失或 `[]`）。
- `400` `"第 2 张表单: <解析原因>"`（某张表的栏目内容不合法）。
- `409` / `422`：任一失败则**整批回滚**；`409` 的 message 前缀形如 `"第 2 张表单保存失败: ..."`。

### 5.8 `GET /api/forms/{id}` —— 取详情

路径参数 `{id}`：**提交 ID 优先，找不到再按表单号查**。

响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "submission": {
      "id": "f_01HZZ...", "form_no": "260206-001", "operation_code": "一次加硬", "operation_text": null,
      "sample_raw": "S1-MD-R-VID2", "sample_project": "M1", "sample_model": "VID2", "sample_spec": "S1",
      "sample_size": "MD", "sample_side": "R", "unit_id": null, "unit_text": "片",
      "submitted_by": "u_01H...", "submitted_at": "2026-02-06T08:20:00Z",
      "last_saved_by": "u_01H...", "last_saved_at": "2026-02-06T09:05:00Z", "save_count": 2,
      "total_qty": 100, "qualified_qty": 96, "defect_qty": 4, "pass_rate": 96,
      "is_complete": true, "missing_fields": [],
      "state": "ACTIVE",
      "created_at": "2026-02-06T08:20:00Z", "updated_at": "2026-02-06T09:05:00Z",
      "lines": [
        { "id": "l_01HZZ...", "form_id": "f_01HZZ...", "seq": 1,
          "total_qty": 100, "qualified_qty": 96, "defect_qty": 4,
          "defects": { "prick": { "concave": 1, "both": 2 }, "scratch": { "convex": 1 } },
          "remark": "首件复检", "created_at": "2026-02-06T08:20:00Z", "updated_at": "2026-02-06T08:20:00Z" }
      ]
    },
    "template": { "code": "一次加硬", "label": "一次加硬", "sort_no": 10 },
    "layout": { "operation": { "..." : "..." }, "header_columns": [ "..." ], "defect_group": { "..." : "..." }, "surfaces": ["concave", "convex", "both"], "surface_labels": { "concave": "凹面", "convex": "凸面", "both": "双面" }, "defect_columns": [ "..." ] }
  }
}
```

> 明细里 `defects` 只包含**非 0** 的格子（数据库里 0 与"未填"都表现为缺少键）；整行都没有非 0 缺陷时 `defects` 键**整个不出现**（`omitempty`）。

典型错误：`404 not_found` `"表单提交不存在: <id>"`。

### 5.9 `POST /api/forms/{id}/void` —— 逻辑作废

路径参数 `{id}` 为**提交 ID**。请求体可选：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `reason` | string | 否 | 作废原因（非空才写入 `void_reason`） |

```json
{ "reason": "重复录入" }
```

响应 200（`data` 就是更新后的 `submission`）：

```json
{
  "code": "ok", "message": "",
  "data": {
    "id": "f_01HZZ...", "form_no": "260206-001", "state": "VOID",
    "voided_at": "2026-02-06T10:00:00Z", "void_reason": "重复录入",
    "save_count": 2, "lines": [ "..." ]
  }
}
```

说明：作废是**逻辑标记**，`save_count` 不变、明细与历史全部保留；会写一条 `action = "void_form"` 的操作日志。
典型错误：`404 not_found` `"表单提交不存在"`。

### 5.10 `GET /api/forms/{id}/history` —— 历史快照

路径参数 `{id}`：提交 ID 优先，回退表单号。无查询参数。按 `save_count DESC` 排序（最新的一版在最前）。

**关键语义**：每次"更新既有表单"都会在更新前把**保存前的那一版**写入快照。因此：

- **新建**时**不写**快照（`save_count = 1` 的那次不留历史）；
- 快照里的 `save_count` 是**被取代的那一版的保存次数**（即"改之前是第 N 版"），不是新版本号。

响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [
      {
        "id": "h_01HZZ...",
        "form_id": "f_01HZZ...",
        "save_count": 1,
        "submission_snapshot": "{\"id\":\"f_01HZZ...\",\"form_no\":\"260206-001\",\"save_count\":1,\"total_qty\":100,\"qualified_qty\":95,\"defect_qty\":5,\"lines\":[]}",
        "lines_snapshot": "[{\"seq\":1,\"total_qty\":100,\"qualified_qty\":95,\"defect_qty\":5,\"defects\":{\"prick\":{\"concave\":1}}}]",
        "saved_by": "u_01H...",
        "saved_at": "2026-02-06T08:20:00Z",
        "changed_by": "u_01H...",
        "changed_at": "2026-02-06T09:05:00Z",
        "reason": "补填合格品"
      }
    ],
    "total": 1
  }
}
```

> ⚠️ `submission_snapshot` 与 `lines_snapshot` 是 **JSON 字符串**（需要前端 `JSON.parse` 一次），不是嵌套对象。

### 5.11 `GET /api/forms/{id}/history/diff` —— 版本差异

路径参数 `{id}`：提交 ID 优先，回退表单号。

| 查询参数 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `from` | **是** | — | 起始版本的**历史快照 ID**（`history.items[].id`，不是 `save_count`）。缺失/不存在 → **404** `"历史快照不存在: "` / `"历史快照不存在: <id>"` |
| `to` | 否 | `current` | 目标版本：省略或传 `"current"` 表示当前版本；否则为历史快照 ID |

```http
GET /api/forms/f_01HZZ.../history/diff?from=h_01HZZ...&to=current
```

响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "from_save_count": 1,
    "to_save_count": 2,
    "changes": [
      { "scope": "submission", "field": "qualified_qty", "label": "合格品", "from": 95, "to": 96 },
      { "scope": "submission", "field": "pass_rate", "label": "合格率", "from": 95, "to": 96 },
      { "scope": "submission", "field": "save_count", "label": "save_count", "from": 1, "to": 2 },
      { "scope": "line", "field": "prick_concave", "label": "点伤-凹面", "index": 1, "from": 1, "to": 3 }
    ],
    "total": 4
  }
}
```

**`changes[]` 字段**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `scope` | string | `submission`（表头级）/ `line`（明细行级） |
| `field` | string | 字段名：表头栏目名、`lines`（整行增删）、或扁平缺陷列名 |
| `label` | string | 中文名（`omitempty`）；顶层栏目或缺陷列会给出，未识别时可能缺失 |
| `index` | int | **仅 `scope = "line"`** 时有效，**1 基**行号（`omitempty`） |
| `from` / `to` | any | 旧值 / 新值，可为 `null`（表示该侧不存在） |

**比较口径**：

- 表头级比较这 20 个字段：`operation_code`、`operation_text`、`total_qty`、`qualified_qty`、`defect_qty`、`pass_rate`、`sample_raw`、`sample_project`、`sample_model`、`sample_spec`、`sample_size`、`sample_side`、`unit_id`、`unit_text`、`remark`、`is_complete`、`missing_fields`、`save_count`、`last_saved_by`、`last_saved_at`。
- 明细按**数组位置**配对比较（不是按 `id`/`seq`）；一侧多出行时该行报一条 `field = "lines"`、`from`/`to` 中一侧为 `null` 的变更，摘要文案形如 `"检验总数 100 / 合格品 95 / 不合格品 5"`。
- 缺陷列逐格（105 格）比较；"未填"与 `0` 都按 0 处理，所以**不会**因为"从 null 变成 0"报变更。
- 注意 `last_saved_at` / `save_count` / `is_complete` 等**服务端维护字段也会进入差异**，前端展示时建议按需过滤。

典型错误：`400`（历史快照内容解析失败）、`404`（表单或快照不存在）。

### 5.12 `GET /api/audit-logs` —— 操作日志

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `entity` | string | — | 实体名，目前写入的固定为 `"form"` |
| `entity_id` | string | — | 实体 ID（表单提交 ID） |
| `limit` | int | `100` | `1..500`，越界/非法回落 100 |

响应 200（按 `id DESC`，即写入顺序倒序）：

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [
      {
        "id": 137,
        "actor_id": "u_01H...",
        "actor_name": "张三",
        "action": "save_form",
        "entity": "form",
        "entity_id": "f_01HZZ...",
        "before": "{\"submission\":{...},\"lines\":[...]}",
        "after": "{\"form_no\":\"260206-001\",\"form\":{\"qualified_qty\":96}}",
        "ip": "10.0.0.8",
        "user_agent": "Mozilla/5.0 ...",
        "created_at": "2026-02-06T09:05:00Z"
      }
    ],
    "total": 1
  }
}
```

`action` 取值：`create_form` / `save_form` / `void_form`。`before` / `after` 是 JSON 字符串（`omitempty`；新建时无 `before`，作废时两者皆可能无）。

### 5.13 样品信息

#### `POST /api/sample/parse`（需登录）

| 字段 | 类型 | 必填 |
|---|---|---|
| `sample_raw` | string | 否（可空） |

```json
{ "sample_raw": "S1-MD-R-VID2" }
```

响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "parsed": {
      "raw": "S1-MD-R-VID2",
      "project": "M1",
      "model": "VID2",
      "spec": "S1",
      "size": "MD",
      "side": "R",
      "class": "MONOC",
      "is_rx": false,
      "tokens": ["S1", "MD", "R", "VID2"],
      "unknown_tokens": [],
      "confidence": 1
    },
    "drawing": {
      "id": "d_01H...", "seq_no": "05", "drawing_no": "S1 - MD - R - VID2 - MONOC & BINOC",
      "model_code": "VID2", "spec_code": "S1", "size_code": "MD", "side_code": "R",
      "class_code": "MONOC_AND_BINOC", "is_rx": false, "active": true,
      "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"
    },
    "matched": true,
    "from_cache": false,
    "note": "解析成功并匹配到参考部件号"
  }
}
```

`unmatched_reason`（`omitempty`，为空表示五个核心字段齐全）取值：`raw_only`（拆不出任何结构化字段）/ `partial`（字段不完整）/ `no_drawing`（清单中没有匹配项）。
`note` 文案：`"未能从原始串拆解出结构化字段；已保留原始串（不影响保存）"` / `"结构化字段不完整；已保留原始串与已识别字段（不影响保存）"` / `"未在参考部件号清单中找到匹配项；这属于正常情况，原始串仍然有效（不影响保存）"` / `"解析成功并匹配到参考部件号"`；空串输入时为 `"样品信息为空；可直接保存，稍后补填（系统不做强制）"`。

> **匹配失败不是错误**：不影响保存，仅作提示。

#### `POST /api/sample/confirm`（需登录）

| 字段 | 类型 | 必填 |
|---|---|---|
| `sample_raw` | string | **是** |
| `project` / `model` / `spec` / `size` / `side` / `class` | string | 否 |
| `is_rx` | bool | 否 |

```json
{ "sample_raw": "S1-MD-R-VID2", "project": "M1", "model": "VID2", "spec": "S1", "size": "MD", "side": "R", "class": "MONOC_AND_BINOC", "is_rx": false }
```

响应 200：

```json
{ "code": "ok", "message": "", "data": { "parsed": { "raw": "S1-MD-R-VID2", "project": "M1", "model": "VID2", "spec": "S1", "size": "MD", "side": "R", "class": "MONOC_AND_BINOC", "is_rx": false, "tokens": [], "confidence": 0 }, "note": "已记录人工确认结果；原始串未被改写" } }
```

说明：确认结果写入解析缓存（带人工标记），**不改写** `sample_raw`。
典型错误：`400 bad_request` `"原始串不能为空"`。

#### `GET /api/sample/cache`（需登录）

| 查询参数 | 必填 | 说明 |
|---|---|---|
| `sample_raw` | **是** | 缺失 → **400** `"缺少 sample_raw 参数"` |

响应 200：

```json
{ "code": "ok", "message": "", "data": { "normalized_key": "s1 md r vid2 monoc", "from_cache": true, "tokens": ["S1", "MD", "R", "VID2"], "unknown_tokens": [] } }
```

#### `GET /api/sample/pending`（需登录）

无参数（固定取 `state = ACTIVE` 且 `sample_project` 为空的表单，**最多 100 条**）。

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [ { "id": "f_01H...", "form_no": "260206-003", "operation_code": "一次加硬", "sample_raw": "某段无法拆解的原文", "is_complete": false, "missing_fields": ["unit_id"], "state": "ACTIVE", "created_at": "2026-02-06T09:00:00Z", "updated_at": "2026-02-06T09:00:00Z", "lines": [] } ],
    "total": 1,
    "note": "这些表单的样品信息尚未拆解出「项目」等结构化项；不影响保存，仅供人工核对补录"
  }
}
```

> `total` 是查询命中的总数（不受 100 条限制影响），`items` 最多 100 条；每项 `lines` 同样为 `[]`。

### 5.14 字典 / 单位 / 参考部件号

#### `GET /api/dict/items`（需登录）

| 查询参数 | 默认 | 说明 |
|---|---|---|
| `category` | — | 字典类别；留空返回**全部类别** |
| `include_inactive` | `false` | 仅当**恰好等于 `"true"`** 时包含停用项 |

排序：`category, sort_no, code`。响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [
      { "id": "di_01H...", "category": "sample_project", "code": "M1", "label": "M1", "sort_no": 10, "active": true, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z" }
    ],
    "total": 1
  }
}
```

`meta` 仅在有值时出现（字符串，通常是 JSON 文本，可含 `aliases` 数组）。

#### `POST /api/dict/items`（需登录）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `category` | string | **是** | 见 §8.3 类别清单 |
| `code` | string | **是** | 去空格后不能为空 |
| `label` | string | 否 | 为空时服务端回落为 `code` |
| `sort_no` | int | 否 | 默认 `0` |
| `active` | bool | 否 | 默认 `true` |
| `meta` | any | 否 | 非 `null` 时按原文存为字符串 |

```json
{ "category": "sample_project", "code": "M3", "label": "M3", "sort_no": 30, "active": true }
```

响应 200，`data` 为该字典项（**按 `category` + `code` 命中已有则更新，否则新建**；不是 201）。

典型错误：`400 bad_request` `"字典项的 category 与 code 不能为空"`。

#### `PATCH /api/dict/items/{id}`（需登录）

| 字段 | 类型 | 说明 |
|---|---|---|
| `label` | string\|null | 新标签 |
| `sort_no` | int\|null | 新排序 |
| `active` | bool\|null | 启停 |
| `meta` | any\|null | 新附加数据 |

响应 200，`data` 为更新后的字典项。典型错误：`404 not_found` `"字典项不存在"`。

#### `GET /api/dict/categories`（需登录）

```json
{
  "code": "ok", "message": "",
  "data": { "categories": ["process", "inspection_operation", "sample_project", "sample_model", "sample_spec", "sample_size", "sample_side", "sample_class", "unit"] }
}
```

> 共 **9** 类。检验工序的可选值请优先用 `inspection_operation`（`process` 为兼容保留的同义类别，两者种子值相同）。

#### `GET /api/units`（需登录）

| 查询参数 | 默认 | 说明 |
|---|---|---|
| `include_inactive` | `false` | 仅当**恰好等于 `"true"`** 时包含停用项 |

排序：`sort_no, code`。响应 200：

```json
{ "code": "ok", "message": "", "data": { "items": [ { "id": "u_01H...", "code": "片", "name": "片", "sort_no": 10, "active": true, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z" } ], "total": 1 } }
```

#### `POST /api/units`（需登录）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `code` | string | **是** | 去空格后不能为空 |
| `name` | string | 否 | 为空时回落为 `code` |
| `sort_no` | int | 否 | 默认 `0` |
| `active` | bool | 否 | 默认 `true` |

响应 200，`data` 为单位对象（按 `code` 命中则更新，否则新建）。典型错误：`400 bad_request` `"单位编码（code）不能为空"`。

#### `GET /api/dict/drawings`（需登录）

| 查询参数 | 说明 |
|---|---|
| `project` / `model` / `spec` / `size` / `side` | 精确匹配对应的 `*_code` 列 |
| `keyword` | 模糊匹配 `drawing_no` |

固定只返回 `active = 1` 的记录，`ORDER BY seq_no, drawing_no`，`LIMIT 500`。响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [ { "id": "d_01H...", "seq_no": "05", "drawing_no": "S1 - MD - R - VID2 - MONOC & BINOC", "model_code": "VID2", "spec_code": "S1", "size_code": "MD", "side_code": "R", "class_code": "MONOC_AND_BINOC", "is_rx": false, "active": true, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z" } ],
    "total": 1,
    "note": "参考清单仅用于辅助选择；输入与清单不一致不影响保存"
  }
}
```

#### `POST /api/dict/drawings/import`（需登录）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `items` | array | 二者至少一个 | 结构化对象数组，每个元素同 `dictionary.Drawing` |
| `rows` | array of string | 二者至少一个 | 整行文本，按 `\t`（其次 `,`）拆成"序号 + 绘图号" |

```json
{
  "rows": [
    "05\tS1 - MD - R - VID2 - MONOC & BINOC",
    "06\tS2 - LG - L - VID1 - MONOC"
  ]
}
```

响应 200：

```json
{ "code": "ok", "message": "", "data": { "inserted": 2, "updated": 0, "total": 2 } }
```

说明：按 `drawing_no` 命中则更新、否则新增；`rows` 里解析不出的行被**静默跳过**（不计入 `total`）。
典型错误：`400 bad_request` `"没有可导入的数据（items 或 rows 至少提供一个）"`。

### 5.15 汇总统计接口

见 §7。

---

## 5A. 两个完整场景示例

### 场景一：某工序现场录入一张表单（含若干缺陷列）

现场：一次加硬工序，样品原文 `S1-MD-R-VID2`，检验 100 片，合格 95 片，不合格 5 片，其中点伤凹面 1、点伤双面 2、划伤凸面 1、脏污凹面 1。

**第 1 步（可选）：解析样品信息**

```http
POST /api/sample/parse
Content-Type: application/json
```

```json
{ "sample_raw": "S1-MD-R-VID2" }
```

从响应里取 `data.parsed.project` / `model` / `spec` / `size` / `side`，回填到表单。

**第 2 步：提交表单（不指定 `form_no`，让服务端生成）**

```http
POST /api/forms
Content-Type: application/json
X-Idempotency-Key: 7f3c1e0a-9b2d-4c11-8f0e-1a2b3c4d5e6f
```

```json
{
  "operation_code": "一次加硬",
  "enforce": "warn",
  "reason": "现场首件录入",
  "form": {
    "operation_text": "一次加硬",
    "sample_raw": "S1-MD-R-VID2",
    "sample_project": "M1",
    "sample_model": "VID2",
    "sample_spec": "S1",
    "sample_size": "MD",
    "sample_side": "R",
    "unit_text": "片",
    "lines": [
      {
        "seq": 1,
        "total_qty": 100,
        "qualified_qty": 95,
        "defect_qty": 5,
        "defects": {
          "prick": { "concave": 1, "both": 2 },
          "scratch": { "convex": 1 },
          "dirt": { "concave": 1 }
        },
        "remark": "首件"
      }
    ]
  }
}
```

> 也可以把 `defects` 写成扁平形式：`"prick_concave": 1, "prick_both": 2, "scratch_convex": 1, "dirt_concave": 1`，效果相同。

**响应 201（节选）**：

```json
{
  "code": "ok", "message": "",
  "data": {
    "action": "create_form",
    "submission": {
      "id": "f_01J2M4X8QK...",
      "form_no": "260206-001",
      "operation_code": "一次加硬",
      "save_count": 1,
      "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95,
      "is_complete": true, "missing_fields": [],
      "state": "ACTIVE",
      "lines": [ { "id": "l_01J2M4X8QK...", "seq": 1, "total_qty": 100, "qualified_qty": 95, "defect_qty": 5,
                   "defects": { "prick": { "concave": 1, "both": 2 }, "scratch": { "convex": 1 }, "dirt": { "concave": 1 } } } ]
    },
    "issues": [],
    "warnings": [],
    "errors": [],
    "missing_fields": [],
    "is_complete": true
  }
}
```

> 本例中 `1+2+1+1 = 5` 恰好等于 `defect_qty`，且每列合计都不超过不合格品数，因此 V-1/V-2/V-3/V-4 全部不命中，`issues` 为 `[]`。
> 若把 `defects` 改成 `{ "prick": { "concave": 3, "both": 2 } }` 而 `defect_qty` 仍是 5，则「点伤」合计 5 = 不合格品数 5，仍不触发 V-3（V-3 只在**超过**时命中）；改成 6 就会得到一条 `level: "warning"` 的 V-3 issue。
> 服务端会把 `total_qty` / `qualified_qty` / `defect_qty` 按明细行重算，但本例显式给了一致的值，结果相同。

**第 3 步：核对通过后前端可跳转详情**

```http
GET /api/forms/f_01J2M4X8QK...
```

（或直接用表单号：`GET /api/forms/260206-001`）

### 场景二：分次补填（第二次只提交部分栏目，观察三态语义）

接场景一。早上先录了检验数，中午补缺陷，下午补样品信息 —— 每次都只提交改动的部分。

**第一次：只录表头计数（不提交 `lines`）**

```http
POST /api/forms
```

```json
{
  "form_no": "260206-001",
  "form": { "operation_code": "一次加硬", "total_qty": 100, "qualified_qty": 95, "defect_qty": 5 }
}
```

- 因为 `form_no` 已存在 → `action = "save_form"`，`save_count` 变为 **2**。
- 请求里**没有** `lines` → 原有明细**保持不变**（此处为新建后的空明细）。
- 结果：`pass_rate` = 95（自动算），`missing_fields` 含 `unit_id`、`sample_raw`、`lines` → `is_complete = false`。

**第二次：补缺陷明细（用 `lines` 整笔替换 —— 这是唯一能写缺陷计数的途径）**

```http
PATCH /api/forms/f_01J2M4X8QK...
```

```json
{
  "reason": "补填缺陷明细",
  "form": {
    "lines": [
      { "seq": 1, "total_qty": 100, "qualified_qty": 95, "defect_qty": 5,
        "defects": { "prick": { "concave": 1, "both": 2 }, "scratch": { "convex": 1 }, "dirt": { "concave": 1 } },
        "remark": "首件" }
    ]
  }
}
```

- `lines` 出现 → **整笔替换**原明细（旧的 `id` 作废，生成新的 `id`）。
- 表头 `total_qty` / `qualified_qty` / `defect_qty`：本次补丁**没有**显式给出 → 按明细行重算（仍为 100 / 95 / 5）。

> ⚠️ **不要试图用"顶层 `form.defects`"补缺陷**：`form` 里直接写 `defects` 是**合法的**（不会 400），请求里未提及的表头栏目也确实"不改"，但**代码从不把顶层 `defects` 写进任何明细行**（`Defects` 只存在于 `Line` 上，顶层补丁只有 `ApplyToLine` 会消费它）。因此补缺陷**必须**通过 `lines` 传入（在行对象里写 `defects`，或在行对象里用扁平字段名如 `prick_concave`）。
- `pass_rate` 未显式给出 → 重算为 95。

**第三次：只把合格品改成 96（且不改明细）**

```http
PATCH /api/forms/f_01J2M4X8QK...
```

```json
{
  "expected_save_count": 3,
  "form": { "qualified_qty": 96 }
}
```

- `qualified_qty` 显式给出 → **表头优先**，即使明细行相加仍是 95，**也保留 96**（纸质表单"表头是合计值、与明细不全对应"的填法被允许）。
- `pass_rate` 未给出 → 按新的 `96 / 100` 重算为 **96**。
- `expected_save_count: 3` 与实际 `save_count` 不一致时 → **409**（前端应提示"该表单已被他人修改"并重新拉取）。

**第四次：显式清空备注（`null`）**

```json
{ "form": { "remark": null } }
```

- `remark` 显式 `null` → **清空**（响应里 `remark` 为 `null`）。
- 对比：**不传** `remark` 键 → 保持原值。

**查看改了什么**

```http
GET /api/forms/f_01J2M4X8QK.../history
```

```http
GET /api/forms/f_01J2M4X8QK.../history/diff?from=<上面 items[0].id>&to=current
```

---

## 6. 数据模型速查

### 6.1 `Submission`（一次表单提交 = 某工序填的那张表单）

| 字段 | 类型 | 只读/服务端维护 | 说明 |
|---|---|---|---|
| `id` | string | **只读（服务端生成）** | 提交主键（不透明字符串） |
| `form_no` | string | 可写 | 表单号；新建时留空由服务端按 `{YYMMDD}-{3位流水}` 生成；唯一 |
| `operation_code` | string | 可写 | 检验工序编码（表单第一列，也是"找到自己工序所在表单"的依据） |
| `operation_text` | string\|null | 可写 | 工序原文（备查） |
| `sample_raw` | string | 可写 | 样品信息原始串（**事实来源，永不被系统改写**） |
| `sample_project` | string\|null | 可写 | 拆解：项目（如 `M1`/`M2`） |
| `sample_model` | string\|null | 可写 | 拆解：型号（如 `VID1`/`VID2`/`VID1 Rx`） |
| `sample_spec` | string\|null | 可写 | 拆解：规格（如 `S1`/`S2`） |
| `sample_size` | string\|null | 可写 | 拆解：大小（如 `MD`/`SM`/`LG`） |
| `sample_side` | string\|null | 可写 | 拆解：左右（如 `L`/`R`） |
| `unit_id` | string\|null | 可写 | 单位字典 ID |
| `unit_text` | string\|null | 可写 | 单位原文（与 `unit_id` 二者取一即可） |
| `submitted_by` | string\|null | **服务端维护** | 首次录入的系统用户 ID（**与纸面人名无关**）；仅在为空时写入 |
| `submitted_at` | string\|null | **服务端维护** | 首次提交时间；仅在为空时写入 |
| `last_saved_by` | string\|null | **服务端维护** | 最后一次保存的系统用户 ID；每次覆盖 |
| `last_saved_at` | string\|null | **服务端维护** | 最后一次保存时间；每次覆盖 |
| `save_count` | int | **只读（服务端维护）** | 保存次数；新建为 1，每次合并更新 +1；可作乐观锁 |
| `total_qty` | int | 可写（默认按明细重算） | 检验总数（表头合计值） |
| `qualified_qty` | int | 可写（默认按明细重算） | 合格品 |
| `defect_qty` | int | 可写（默认按明细重算） | 不合格品 |
| `pass_rate` | float\|null | **可写但通常服务端计算** | 合格率（%）；`total_qty <= 0` 时为 `null` |
| `is_complete` | bool | **只读（服务端维护）** | 是否已填完整（`missing_fields` 为空） |
| `missing_fields` | string[] | **只读（服务端维护）** | 缺失栏目清单（见 §3.4） |
| `state` | string | **服务端维护** | `ACTIVE` / `VOID` |
| `voided_at` | string\|null | **服务端维护** | 作废时间 |
| `void_reason` | string\|null | **服务端维护** | 作废原因 |
| `remark` | string\|null | 可写 | 备注 |
| `created_at` | string | **只读（服务端生成）** | 创建时间 |
| `updated_at` | string | **只读（服务端维护）** | 最后更新时间 |
| `lines` | Line[] | 见 §3.5 | 明细行；**只在详情接口里有内容**，列表接口恒为 `[]` |

### 6.2 `Line`（明细行）

| 字段 | 类型 | 只读/服务端维护 | 说明 |
|---|---|---|---|
| `id` | string | **只读（服务端生成）** | 明细行主键；**每次 `lines` 整笔替换都会重新生成** |
| `form_id` | string | **只读（服务端维护）** | 所属提交 ID |
| `seq` | int | 可写 | 行序号；`lines` 未给 `seq` 时按数组顺序自动 `1..N`；`(form_id, seq)` 唯一 |
| `total_qty` | int | 可写 | 该行检验总数 |
| `qualified_qty` | int | 可写 | 该行合格品 |
| `defect_qty` | int | 可写 | 该行不合格品 |
| `defects` | Defects | 可写 | 不良缺陷计数（见 §6.3）；**0 与"未填"都表现为键缺失**，整行为空时该键不出现 |
| `remark` | string\|null | 可写 | 行备注 |
| `created_at` | string | **只读（服务端生成）** | 创建时间（替换时重置为当前时间） |
| `updated_at` | string | **只读（服务端维护）** | 更新时间 |

### 6.3 `Defects`（不良缺陷计数）

嵌套形态（**对外的 JSON 形态**）：

```json
{
  "prick": { "concave": 1, "convex": 0, "both": 2 },
  "dirt":  { "convex": 3 }
}
```

- 第一层键：缺陷编码（35 个之一，见 §2.2）。
- 第二层键：表面维度，只能是 `concave` / `convex` / `both`。
- 值：整数计数。
- `defects` 里只出现的格子会被写入/修改（三态语义见 §3.5）；**空对象 `{}` 表示清空全部**。

**扁平等价形态**（同样被接受，也用于数据库列名与统计分组）：

```json
{ "prick_concave": 1, "prick_convex": 0, "prick_both": 2, "dirt_convex": 3 }
```

### 6.4 数据库列名对照（`forms` / `form_lines`）

`forms` 表列（`migrations/sqlite/0003_form_submission.sqlite.up.sql`）与 JSON 字段**一一同名**，类型：

| 列 | SQL 类型 | 约束 |
|---|---|---|
| `id` | TEXT | PK |
| `form_no` | TEXT | NOT NULL，**唯一索引** `ux_forms_form_no` |
| `operation_code` | TEXT | NOT NULL DEFAULT `''` |
| `operation_text` | TEXT | NULL |
| `sample_raw` | TEXT | NOT NULL DEFAULT `''` |
| `sample_project` / `sample_model` / `sample_spec` / `sample_size` / `sample_side` | TEXT | NULL |
| `unit_id` / `unit_text` | TEXT | NULL |
| `submitted_by` / `submitted_at` / `last_saved_by` / `last_saved_at` | TEXT | NULL |
| `save_count` | INTEGER | NOT NULL DEFAULT 1 |
| `total_qty` / `qualified_qty` / `defect_qty` | INTEGER | NOT NULL DEFAULT 0 |
| `pass_rate` | REAL | NULL |
| `is_complete` | INTEGER | NOT NULL DEFAULT 0（布尔存 0/1） |
| `missing_fields` | TEXT | NULL（JSON 数组文本） |
| `state` | TEXT | NOT NULL DEFAULT `'ACTIVE'` |
| `voided_at` / `void_reason` / `remark` | TEXT | NULL |
| `created_at` / `updated_at` | TEXT | NOT NULL DEFAULT `datetime('now')` |

关键索引：`ix_forms_state_created(state, created_at)`、`ix_forms_operation(operation_code, submitted_at)`、`ix_forms_sample_dim(sample_spec, sample_size, sample_side, sample_model)`、`ix_forms_project(sample_project)`、`ix_forms_complete(state, is_complete)`、`ix_forms_unit(unit_id)`、`ix_forms_submitted_by(submitted_by)`。

`form_lines` 表列 = `id, form_id, seq, total_qty, qualified_qty, defect_qty` + **105 个缺陷子列** + `remark, created_at, updated_at`；所有缺陷子列均为 `INTEGER NOT NULL DEFAULT 0`。唯一索引 `ux_form_lines_form_seq(form_id, seq)`。

### 6.5 其他模型

**`HistoryEntry`（`form_history`）**：`id`、`form_id`、`save_count`、`submission_snapshot`（JSON 字符串）、`lines_snapshot`（JSON 字符串）、`saved_by`、`saved_at`、`changed_by`、`changed_at`、`reason`。

**`AuditEntry`（`audit_logs`）**：`id`（int，自增）、`actor_id`、`actor_name`、`action`、`entity`（固定 `"form"`）、`entity_id`、`before`、`after`、`ip`、`user_agent`、`detail`、`created_at`。

**`User`**：`id`、`username`、`display_name`、`employee_no`、`unit_id`、`is_admin`、`must_change_pwd`、`status`、`failed_attempts`、`locked_until`、`last_login_at`、`created_at`、`updated_at`（**`password_hash` 不出现在任何响应里**）。

**幂等记录（`idempotency_requests`）**：`id`、`user_id`、`idempotency_key`、`request_method`、`request_path`、`request_hash`、`status_code`、`response_headers`、`response_body`、`created_at`、`expires_at`；唯一索引 `(user_id, idempotency_key)`。

---

## 7. 汇总统计

### 7.1 通用筛选参数（`/api/stats/summary`、`/api/stats/defects`、`/api/stats/workload` 共用）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `from` | string | — | `COALESCE(submitted_at, created_at) >= from`（**注意与 `GET /api/forms` 的 `from` 口径不同**：这里优先用 `submitted_at`） |
| `to` | string | — | `COALESCE(submitted_at, created_at) <= to + "T23:59:59Z"` |
| `operation_code` | string | — | 精确匹配 |
| `sample_project` | string | — | 精确匹配 |
| `sample_model` | string | — | 精确匹配 |
| `sample_spec` | string | — | 精确匹配 |
| `sample_size` | string | — | 精确匹配 |
| `sample_side` | string | — | 精确匹配 |
| `submitted_by` | string | — | 精确匹配录入人 ID |
| `unit` | string | — | 精确匹配 `COALESCE(unit_text, unit_id)` |
| `include_void` | string | — | 仅当**恰好等于 `"true"`** 时包含已作废表单；**默认只统计 `state = 'ACTIVE'`** |

### 7.2 `group_by` 维度白名单（14 个，从代码取全）

`GET /api/stats/dimensions` 返回的完整清单（按字母序）：

| 维度 | 含义 | 底层表达式 |
|---|---|---|
| `day` | 日期（`YYYY-MM-DD`） | `substr(COALESCE(submitted_at, created_at), 1, 10)` |
| `form_no` | 表单号 | `form_no` |
| `last_saved_by` | 最后保存人 ID | `last_saved_by` |
| `month` | 月份（`YYYY-MM`） | `substr(COALESCE(submitted_at, created_at), 1, 7)` |
| `operation_code` | 检验工序编码 | `operation_code` |
| `operation_text` | 检验工序原文 | `operation_text` |
| `sample_model` | 型号 | `sample_model` |
| `sample_project` | 项目 | `sample_project` |
| `sample_raw` | 样品原始串 | `sample_raw` |
| `sample_side` | 左右 | `sample_side` |
| `sample_size` | 大小 | `sample_size` |
| `sample_spec` | 规格 | `sample_spec` |
| `submitted_by` | 录入人 ID | `submitted_by` |
| `unit` | 单位 | `COALESCE(unit_text, unit_id)` |

> ⚠️ **`sample_class`（结构类别）不在白名单里**，用它分组会返回 400。
> 传入白名单外的维度 → **400** `bad_request`，message `"不支持的分组维度: xxx"`，`details = {"supported": [...14 个...]}`。

**写法**：`?group_by=sample_project,sample_size`（逗号分隔）或 `?group_by=sample_project&group_by=sample_size`（重复参数）——两种都支持，可混用；空白项被忽略。**不传 `group_by` 时只返回总计 `total`**，`groups` 为空数组。

`GET /api/stats/dimensions` 响应：

```json
{ "code": "ok", "message": "", "data": { "dimensions": ["day", "form_no", "last_saved_by", "month", "operation_code", "operation_text", "sample_model", "sample_project", "sample_raw", "sample_side", "sample_size", "sample_spec", "submitted_by", "unit"] } }
```

### 7.3 `GET /api/stats/summary`

响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "group_by": ["sample_project", "sample_size"],
    "groups": [
      { "key": "M1 | MD", "form_count": 12, "total_qty": 1200, "qualified_qty": 1180, "defect_qty": 20, "pass_rate": 98.33 },
      { "key": "M2 | SM", "form_count": 7,  "total_qty": 700,  "qualified_qty": 690,  "defect_qty": 10, "pass_rate": 98.57 }
    ],
    "total": { "key": "", "form_count": 19, "total_qty": 1900, "qualified_qty": 1870, "defect_qty": 30, "pass_rate": 98.42 },
    "note": "不良缺陷子项合计可能大于不合格品数（同一片镜片可计入多个类别，见需求 Q5）"
  }
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `group_by` | string[] | 回显实际使用的维度顺序 |
| `groups[].key` | string | 各维度值用 **`" | "`（空格-竖线-空格）**连接；单维度就是该值本身 |
| `groups[].form_count` | int | 该组的表单数（`COUNT(*)`） |
| `groups[].total_qty` | int | 该组 `SUM(total_qty)`（`COALESCE`，无数据为 0） |
| `groups[].qualified_qty` | int | 该组 `SUM(qualified_qty)` |
| `groups[].defect_qty` | int | 该组 `SUM(defect_qty)` |
| `groups[].pass_rate` | float | `qualified_qty / total_qty * 100`，保留 2 位；**`total_qty <= 0` 时为 `0`（不是 null）** |
| `total` | object | 同样结构的**总计**（`key` 为空串） |
| `note` | string | 固定口径提示 |

要点：指标全部取**表单表头**的 `total_qty` / `qualified_qty` / `defect_qty`，**不**从明细行求和；`groups` 最多 500 行、按 `form_count DESC` 排序，**可能有更多分组被截断**（需要完整分组请自行加筛选收窄）。

### 7.4 `GET /api/stats/defects` —— 不良缺陷分布

无维度参数，使用 §7.1 的通用筛选。按**明细行**逐列求和（`form_lines` JOIN `forms`）。响应 200：

```json
{
  "code": "ok", "message": "",
  "data": {
    "defects": {
      "prick": { "label": "点伤", "surfaces": { "concave": 12, "convex": 3, "both": 5, "total": 20 } },
      "scratch": { "label": "划伤", "surfaces": { "concave": 8, "convex": 0, "both": 1, "total": 9 } }
    },
    "grand_total": 29,
    "surfaces": ["concave", "convex", "both"],
    "note": "每列为 凹面/凸面/双面 三项；同一片镜片可能被计入多个缺陷类别（需求 Q5，登记待定）"
  }
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `defects` | object | 键为**全部 35 个缺陷编码**（即使全为 0 也会出现） |
| `defects.<code>.label` | string | 缺陷中文名 |
| `defects.<code>.surfaces` | object | `concave` / `convex` / `both` 三个合计，外加 `total` = 三者之和 |
| `grand_total` | int | 全部 35 列 × 3 表面之和 |
| `surfaces` | string[] | 固定 `["concave","convex","both"]` |
| `note` | string | 固定口径提示 |

### 7.5 `GET /api/stats/workload` —— 按人统计工作量

| 查询参数 | 默认 | 说明 |
|---|---|---|
| `by` | `submitted_by` | 只接受 **`submitted_by`** 或 **`last_saved_by`**；其它值 → **400** `"不支持的人员维度: xxx（可选 submitted_by | last_saved_by）"` |
| 其余 | — | 同 §7.1 通用筛选 |

```json
{
  "code": "ok", "message": "",
  "data": {
    "by": "submitted_by",
    "items": [ { "key": "u_01H...", "form_count": 12, "total_qty": 1200, "qualified_qty": 1180, "defect_qty": 20, "pass_rate": 98.33 } ],
    "total": 1
  }
}
```

按人分组，空值统一显示为 `"(未填)"`；最多 200 行，按 `form_count DESC`。

### 7.6 `GET /api/stats/incomplete-forms` —— 未填完整清单

| 查询参数 | 默认 | 说明 |
|---|---|---|
| `limit` | `100` | `1..500`，非法/越界回落 100 |

固定条件：`state = 'ACTIVE' AND is_complete = 0`，按 `created_at DESC`。

```json
{
  "code": "ok", "message": "",
  "data": {
    "items": [
      { "id": "f_01H...", "form_no": "260206-004", "operation_code": "一次加硬", "missing_fields": "[\"unit_id\",\"sample_raw\"]", "last_saved_at": "2026-02-06T09:30:00Z", "created_at": "2026-02-06T09:30:00Z" }
    ],
    "total": 1,
    "note": "这些笔尚未填完整（缺失表单既有栏目或计数明细），可供跟进"
  }
}
```

> ⚠️ 此接口的 `missing_fields` 是**数据库原始文本（JSON 字符串）**，需要前端 `JSON.parse`；且主键字段名用了 `id`（不是 `form_id`）。

### 7.7 口径提示（重要）

1. **明细缺陷子项合计可能大于不合格品数**：同一片镜片可同时计入多个缺陷类别，因此 `Σ(35 列 × 3 表面)` ≥ `defect_qty` 是正常情况（见 V-2 恒为警告）。前端做比率类展示时请勿假设两者相等。
2. **`pass_rate` 的口径差异**：表单详情/保存响应里的 `pass_rate` 由 `ComputePassRate` 计算，`total_qty <= 0` 时为 **`null`**；统计接口（`summary` / `workload`）的 `pass_rate` 在 `total_qty <= 0` 时为 **`0`**。
3. **统计默认排除已作废表单**：需要包含请加 `include_void=true`。
4. **`from` / `to` 的时间字段口径**：`GET /api/forms` 用 `created_at`，统计接口用 `COALESCE(submitted_at, created_at)`。
5. **统计表头指标取自 `forms` 表**（不是明细求和）；只有 `defects` 分布取自 `form_lines`。
6. **`groups` / `items` 有截断**：`summary` 最多 500 组、`workload` 最多 200 行、`incomplete-forms` 最多 500 行，前端应提示"结果已截断"。

---

## 8. 附录

### 8.1 字段中文名对照

| 字段名 | 中文名 |
|---|---|
| `operation_code` | 检验工序（提交用名；字段目录里显示为 `operation`） |
| `operation_text` | 检验工序（原文，备查） |
| `total_qty` | 检验总数 |
| `qualified_qty` | 合格品 |
| `defect_qty` | 不合格品 |
| `pass_rate` | 合格率 |
| `sample_raw` | 样品信息（原始串） |
| `sample_project` | 项目（M1 / M2） |
| `sample_model` | 型号（VID1 / VID1 Rx / VID2） |
| `sample_spec` | 规格（S1 / S2） |
| `sample_size` | 大小（MD / SM / LG） |
| `sample_side` | 左右（L / R） |
| `unit_id` / `unit_text` | 单位 |
| `save_count` | 保存次数 |
| `is_complete` | 是否填完整 |
| `missing_fields` | 缺失栏目 |
| `state` | 状态（`ACTIVE` / `VOID`） |
| `voided_at` | 作废时间 |
| `void_reason` | 作废原因 |
| `remark` | 备注 |
| `submitted_by` / `submitted_at` | 首次录入人 / 首次提交时间 |
| `last_saved_by` / `last_saved_at` | 最后保存人 / 最后保存时间 |
| `seq` | 明细行序号 |
| `defects` | 不良缺陷明细 |

35 个不良缺陷列的中文名见 §2.2 的完整表格；105 个子列的中文名规则为 `<列名>-<凹面|凸面|双面>`（如 `点伤-凹面`）。

### 8.2 工序表单模板列表与"未登记工序"的回退

**内置模板**（`internal/operations.Defaults`，`GET /api/form-templates` 返回的顺序）：

| 顺序 | `code` | `label` | `sort_no` | `note` |
|---|---|---|---|---|
| 1 | `GENERAL` | 一般检验 | 0 | 通用模板：开放全部列，用于工序未确定或临时录入 |
| 2 | `一次加硬` | 一次加硬 | 10 | （无） |
| 3 | `二次加硬` | 二次加硬 | 20 | （无） |

- 所有模板的 `allowed_fields` 均为空 → **不限制可填列**（全部 110 个字段都可提交，且**没有任何必填**）。
- `allowed_fields` 支持将来按工序收窄（"非空时只允许提交其中的字段"）——但**当前服务层尚未按 `allowed_fields` 过滤提交**，该字段目前只是模板元数据。
- **未登记工序的回退**（`operations.Find`）：传入任何未登记的编码（如 `三次加硬`）**不报错**，返回：

```json
{ "code": "三次加硬", "label": "三次加硬", "sort_no": 100, "note": "未登记的工序：按通用模板处理（开放全部列）" }
```

  即：`label` = 编码本身、`sort_no` = 100、说明"按通用模板处理（开放全部列）"，`layout` 仍是完整全列。**所以前端不需要在本地维护工序白名单**——没有的工序也能直接渲染并提交。

### 8.3 字典类别与初始种子值（`cmd/seed/main.go`）

**类别常量**（`internal/dictionary`）：

| 类别 `category` | 含义 |
|---|---|
| `process` | 工序（兼容旧命名） |
| `inspection_operation` | 检验工序（表单第一列的可选值） |
| `sample_project` | 项目 |
| `sample_model` | 型号 |
| `sample_spec` | 规格 |
| `sample_size` | 大小 |
| `sample_side` | 左右 |
| `sample_class` | 结构类别（含义未确认，冗余维度） |
| `unit` | 单位 |

> `GET /api/dict/categories` 返回全部 9 类（含 `inspection_operation`）。

**种子值**（`cmd/seed/main.go` 运行后写入 `dict_items`，共 28 条；命令：`go run ./cmd/seed`）：

| 类别 | code（→ label） | sort_no |
|---|---|---|
| `process` | `GENERAL`→一般检验；`一次加硬`；`二次加硬` | 0 / 10 / 20 |
| `inspection_operation` | `GENERAL`→一般检验；`一次加硬`；`二次加硬` | 0 / 10 / 20 |
| `sample_project` | `M1`；`M2` | 10 / 20 |
| `sample_model` | `VID1`；`VID1 Rx`；`VID2` | 10 / 11 / 20 |
| `sample_spec` | `S1`；`S2` | 10 / 20 |
| `sample_size` | `MD`；`SM`；`LG` | 10 / 20 / 30 |
| `sample_side` | `L`；`R` | 10 / 20 |
| `sample_class` | `MONOC`；`BINOC`；`MONOC_AND_BINOC`→`MONOC & BINOC` | 10 / 20 / 30 |

**单位（`units` 表）**：种子脚本**不写入任何单位**，单位需通过 `POST /api/units` 维护（`GET /api/units` 初始可能为空数组）。

**参考部件号（`sample_drawings` 表）**：由 `cmd/seed` 读取仓库内 `data/reference_drawings.csv` 导入（找不到文件时打印 warning 并跳过）。CSV 每行 `序号, 绘图号`（如 `05,S1 - MD - R - VID2 - MONOC & BINOC`），维度由脚本按"规格-大小-左右-型号-结构类别"的书写顺序**尽力拆解**（`S1`/`S2` → `spec_code`，`MD`/`SM`/`LG` → `size_code`，`L`/`R` → `side_code`，`VID1`/`VID2` → `model_code`，含 `RX` → `is_rx=true`，`MONOC`/`BINOC`/两者兼有 → `class_code`）。该清单**仅供参考**，与现场输入不一致不影响保存。

### 8.4 常见对接陷阱速查

| 陷阱 | 说明 |
|---|---|
| `POST` 返回 201 而非 200 | `POST /api/forms` 用 `201 Created`，`PATCH` 用 200；两者响应体格式相同 |
| `mode: "replace"` 无效 | 服务端从不读取 `mode`；明细是否替换只看 `lines` 是否出现 |
| 列表接口没有明细 | `GET /api/forms` 的 `lines` 恒为 `[]`，必须调详情接口 |
| 表单号 / 提交 ID 混用 | `GET` / `history` / `diff` 支持两者；`PATCH` / `void` **只认提交 ID** |
| `form_no` 重复不会报错 | 存在即**合并**（`action = "save_form"`），这是设计行为，不是冲突 |
| `int` 字段传 `null` 等于写 0 | 整数没有"清空"语义（见 §3.5） |
| `pass_rate` 要不要传 | 一般别传；传了就会覆盖服务端计算值 |
| `form.operation_code` vs 顶层 `operation_code` | 顶层只在**新建**时生效；更新既有表单必须写进 `form` |
| 未知字段会被拒绝 | 400 而不是忽略；`details.unknown_fields` 会列出拼错的键 |
| 缺陷字段的两种写法 | 嵌套 `defects.prick.concave` 与扁平 `prick_concave` 都支持 |
| 顶层 `defects` 不写明细 | 缺陷计数只存在于**明细行**；要写缺陷请提交 `lines` |
| 历史快照是字符串 | `submission_snapshot` / `lines_snapshot` 需要 `JSON.parse` |
| `X-Request-Id` 不能当幂等键 | 用 `X-Idempotency-Key` 或 `client_request_id` |
| Cookie 名默认 `sid` | 可通过 `COOKIE_NAME` 改；`Authorization` / `X-Auth-Token` 不受影响 |
| 未登记工序不用报错 | `GET /api/form-templates/{code}` 永不 404，返回 `sort_no=100` 的回退模板 |

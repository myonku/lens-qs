# lens-qs-api 接口文档（前端对接版）

> 模块：`github.com/myonku/lens-qs-api`（Go 1.24 + Gin，SQLite / PostgreSQL）
> 本文按**后端代码实际行为**整理，字段名一律 **snake_case**（JSON tag 与数据库列名一致）。
> 与 `docs/design.md` §6 的差异见 **§7.3**（前端请注意：字段名、`mode`/`enforce`、部分接口未实现等）。

---

## 1. 总览

### 1.1 Base URL 与请求约定

| 项 | 值 |
|---|---|
| 监听地址 | 环境变量 `APP_ADDR`，默认 `:8080` → `http://127.0.0.1:8080` |
| API 前缀 | `/api`（`GET /` 与 `GET /healthz` 在根路径） |
| 请求体 | `Content-Type: application/json`（写接口全部走 JSON，无表单/文件上传） |
| 请求 ID | 请求头 `X-Request-Id` 或 `X-Request-ID`（可选，存在则透传），响应头回写 `X-Request-Id` |
| 需登录范围 | **仅** `GET /healthz`、`GET /`、`POST /api/auth/login` 公开；其余全部需要登录 |
| 权限 | 只有两级：普通用户 / 管理员（`is_admin`）。**只有 `/api/users*` 三个接口要求管理员** |
| CORS | `CORS_ORIGINS`（逗号分隔，支持 `*`）。**为空时不加任何 CORS 头**；命中白名单时回 `Access-Control-Allow-Origin: <origin>`、`Allow-Credentials: true`、`Allow-Methods: GET, POST, PATCH, PUT, DELETE, OPTIONS`、`Allow-Headers: Authorization, Content-Type, X-Auth-Token, X-Request-Id`、`Max-Age: 600`；`OPTIONS` 预检直接返回 **204 空响应** |

### 1.2 认证

**三种取令牌方式**（`internal/middleware/middleware.go: TokenFromRequest`，按**固定优先级**只取第一个非空值）：

| 顺序 | 来源 | 说明 |
|---|---|---|
| 1 | Cookie | Cookie 名 = 环境变量 `COOKIE_NAME`，**默认 `sid`** |
| 2 | `Authorization: Bearer <token>` | 前缀大小写不敏感（`bearer` / `Bearer` 均可） |
| 3 | `X-Auth-Token: <token>` | 无前缀 |

> ⚠️ 只取第一个非空来源：若请求里既有 Cookie 又有 Bearer，而 Cookie 值已失效，**不会回退**到 Bearer，直接 401。

**登录下发的 Cookie 属性**：`Path=/`、`Max-Age=43200`（12 小时）、`HttpOnly`、`SameSite=Lax`、`Secure=COOKIE_SECURE`（默认 `false`）。
令牌是 32 字节随机数的 base64url（43 字符），服务端只存 SHA-256 哈希。

**会话生命周期**：

| 机制 | 环境变量 | 默认 |
|---|---|---|
| 绝对有效期 | `SESSION_TTL` | 12h |
| 空闲超时（每次成功请求滑动续期 `last_seen_at`） | `SESSION_IDLE_TTL` | 2h |
| 连续登录失败锁定阈值 | `MAX_LOGIN_FAIL` | 5 次 |
| 锁定时长 | `LOCK_DURATION` | 10 分钟 |

失效/改密：修改自己的密码、管理员重置密码、管理员停用账号 → **该用户全部会话立即失效**，前端需跳登录页。

**未登录/会话失效**：HTTP `401` + `{"code":"unauthorized","message":"..."}`。

### 1.3 统一响应格式

```json
// 成功
{ "code": "ok", "message": "", "data": { } }
// 失败（details 仅在存在时出现；not_found/forbidden/unauthorized 无 details）
{ "code": "not_found", "message": "表单不存在: QS-0001" }
{ "code": "bad_request", "message": "存在无法识别的表单字段", "details": ["sample_project2"] }
```

* 成功统一 HTTP `200`；`POST /api/forms` 与 `POST /api/users` 返回 **`201`**（`code` 仍为 `ok`）。
* **例外**：`GET /healthz` → `{"status":"ok","time":"..."}`；`GET /` → `{"name":"lens-qs-api","version":"0.1.0","docs":"..."}`，二者**不套**统一信封。
* 未识别的内部错误统一 `500 {"code":"internal_error","message":"服务器内部错误"}`（不暴露内部细节）。

### 1.4 分页与各类上限

**分页响应结构**（`httpx.Page[T]`）：

```json
{ "items": [], "total": 0, "page": 1, "page_size": 20 }
```

`items` 为 `nil` 时返回 `[]`，**永不为 `null`**。

| 参数 | 默认 | 规则（代码实际行为） |
|---|---|---|
| `page` | 1 | `≤0` 或非数字 → 静默回落 1（**不报错**） |
| `page_size` | 20 | 非数字或 `≤0` 或 **`>200`** → 静默回落 20（**不报错**） |

**其它上限一览**：

| 位置 | 上限 / 默认 | 越界行为 |
|---|---|---|
| `GET /api/forms`（`page_size`） | ≤200，默认 20 | 静默回落 20 |
| `GET /api/users`（`page_size`） | ≤200，默认 20 | 静默回落 20 |
| `GET /api/dict/drawings` | **固定 LIMIT 500**（handler 硬编码；store 侧 `≤0 或 >1000` 回落 500） | 无 `limit` 参数可传 |
| `GET /api/audit-logs`（`limit`） | 默认 100，`>500` 或 `≤0` → 100 | 静默 |
| `GET /api/stats/summary`（分组结果） | **LIMIT 500**（按 `record_count` 倒序） | 无参数可调 |
| `GET /api/stats/workload` | **LIMIT 200** | 无参数可调 |
| `GET /api/stats/incomplete-forms`（`limit`） | 默认 100，`>500` 或 `≤0` → 100 | 静默 |
| `GET /api/sample/pending` | 固定 LIMIT 100 | 无参数可调 |

> 本工程**当前没有导出接口**（design.md 中的 `/forms/:formNo/export`、`/forms/export` 未实现）。

### 1.5 错误码表

| `code` | HTTP | 典型触发场景 |
|---|---|---|
| `ok` | 200 / 201 | 成功 |
| `bad_request` | 400 | JSON 格式错误、`form_no` 为空、补丁里出现无法识别的字段、`lines` 不是数组、非法的 `user.status`、不能停用/降级自己、`sample_raw` 为空、不支持的 `group_by` / `by` 维度、请求体缺少必填字段（gin binding 失败） |
| `unauthorized` | 401 | 缺少令牌、令牌无效、会话过期、空闲超时、账号不可用、登录名或密码不正确 |
| `forbidden` | 403 | 非管理员访问 `/api/users*`；账号被停用；账号被锁定（登录时） |
| `not_found` | 404 | 表单 / 工序记录 / 字典项 / 历史快照 / 用户不存在 |
| `conflict` | 409 | 乐观锁冲突（`expected_save_count` 不匹配）；创建用户时登录名已存在 |
| `validation_failed` | 422 | `enforce=strict` 时存在 `error` 级校验问题，`details` 为 error 级 `Issue` 数组 |
| `conflict` | 409 | 唯一性冲突（V-7）：明细行 `seq` 重复等；也用于乐观锁 `expected_save_count` 不匹配 |

### 1.6 幂等与并发约定

| 约定 | 说明 |
|---|---|
| **幂等键（已支持）** | 写方法（POST/PATCH/PUT/DELETE）支持幂等：请求头 `X-Idempotency-Key`（或 `Idempotency-Key`）优先，其次请求体 `client_request_id` / `clientRequestId` / `idempotency_key`。作用域 `(user_id, 键)`；命中且请求体指纹一致 → **回放首次响应**（响应头 `X-Idempotent-Replay: true`）；指纹不同 → `409`；**非 2xx 不缓存**（可用同键重试）；TTL 由 `IDEMPOTENCY_TTL` 控制（默认 24h）。见 §4.7 |
| 重复提交防护 | 更新已存在记录时用 `expected_save_count` 乐观锁；或先 `GET` 再提交 |
| `POST /api/forms/{formNo}/records` 的幂等性 | **带** `sequence_no` → 定位到该序号（不存在则新建），重复提交只更新同一笔；**不带** `sequence_no` → 每次调用**新建一笔**（重复提交会造出多笔） |
| 乐观锁 | 仅对**已存在**记录生效；`expected_save_count` 与库里 `save_count` 不等 → `409`，`details` 里带当前记录与 `current_save_count`。新建记录时该参数被忽略 |
| 事务与行锁 | 每次保存/作废在单事务内完成；PostgreSQL 下对 `forms` / `form_records` 目标行 `SELECT ... FOR UPDATE`，SQLite 下为库级写锁 |
| 批量写入 | `records` 数组在**同一个事务内**逐笔保存，**整批原子**：中途某笔报错则整批回滚（表单与已保存的笔都不落库），响应含 `"atomic": true`。错误消息会带"第 N 笔"定位 |
| 明细替换 | `lines` 一旦提交即**整笔替换**（先 `DELETE` 再 `INSERT`），行 `id` 每次重建，不做行级合并 |
| 唯一约束 | `forms.form_no`、`form_records(form_id, sequence_no)`、`record_lines(record_id, seq)`；`lines` 内重复 `seq` 会撞唯一约束 → **`409 conflict`** |
| 循环/重试建议 | 收到 `409` → 用 `details.current` 覆盖本地状态，让用户确认后重试；收到 `401` → 重新登录；收到 `422` → 展示 `details` 里的 `error` 问题并修正数据 |

---

## 2. 错误与校验语义

### 2.1 `enforce=warn|strict`

* 解析规则（`inspection.ParseEnforce`）：只有大小写不敏感地等于 `strict` 才是 strict；**缺省、空、任何其它值都回落为 `warn`**。
* `warn`（默认）：所有问题都是 `warning`，**照常保存**，响应里通过 `issues` / `warnings` 返回。
* `strict`：把 **V-1 / V-3 / V-4** 升级为 `error`；只要存在 error 级问题 → **整笔不落库**，返回 `422 validation_failed`，`details` 只含 error 级问题（`warnings` 此时不返回）。

### 2.2 V-1..V-10 规则表

| 规则 | 含义 | 默认级别 | `strict` 时是否阻断 | 实现位置 |
|---|---|---|---|---|
| **V-1** | 合格品 + 不良品 = 检验总数（三者全为 0 时不提示） | warning | **是**（升级为 error） | `validate.go ValidateLines` |
| **V-2** | 五类不良 15 个子项之和 = 不良品数（同一片镜片可计入多个类别） | warning | **否，任何模式都不阻断**（硬编码 `Level: "warning"`） | 同上 |
| **V-3** | 子项边界：某类不良三子项合计 ≤ 不良品数；单个子项 ≤ 检验总数 | warning | **是**（升级为 error） | 同上 |
| **V-4** | 所有计数为非负整数 | warning | **是**（升级为 error） | 同上 |
| **V-5** | 良率派生（`qualified_qty / total_qty * 100`，四舍五入 2 位，`total_qty=0` 时 `pass_rate` 为 `null`） | **仅计算，从不产生 Issue** | 否 | `ComputeTotals` |
| **V-6** | 时间顺序：送检时间 ≤ 检验时间；检验时间 ≤ 当前时间 | warning | **否，恒为 warning** | `ValidateRecord` |
| **V-7** | 唯一性（`form_no` 唯一、`(form_id, sequence_no)` 唯一、`(record_id, seq)` 唯一） | **数据库唯一索引保证**，不产生 Issue | 否（但冲突返回 **`409 conflict`**，见 §1.5） | migrations + store |
| **V-8** | 样品信息与参考部件号清单不匹配（`needs_review=true`） | warning | **否，恒为 warning** | `ValidateForm` |
| **V-9** | 结构化项缺失（`sample_project` 为空，M1/M2） | warning | **否，恒为 warning** | `ValidateRecord` |
| **V-10** | 表单既有栏目缺失（`submitted_by` / `submitted_at` / `inspected_by` / `inspected_at` / `machine_operator` / `handover_by`，以及表单级 `sample_raw`、`unit_id`+`unit_text` 全空） | warning | **否，恒为 warning** | `ValidateRecord` / `ValidateForm` |

> **在任何模式下都不阻断的规则：V-2、V-5、V-6、V-7（不产生 Issue）、V-8、V-9、V-10。**
> `strict` 只影响 **V-1 / V-3 / V-4**，且只影响 `lines` 的明细校验；V-6/V-9/V-10 即使在 strict 下也只是 warning。
> V-10 的笔级提示仅在该笔"已经开始填"（任一纸面栏目非空）时才产生，避免噪声。

### 2.3 `422` 响应结构

```json
{
  "code": "validation_failed",
  "message": "校验未通过（enforce=strict）",
  "details": [
    {
      "rule": "V-1",
      "level": "error",
      "field": "total_qty",
      "message": "第 1 行：合格品(95) + 不良品(5) 与检验总数(90) 不一致"
    }
  ]
}
```

### 2.4 `Issue` 对象

| 字段 | 类型 | 说明 |
|---|---|---|
| `rule` | string | 规则编号 `V-1` … `V-10` |
| `level` | string | `warning`（不阻断）\| `error`（阻断，strict 下由 V-1/V-3/V-4 升级） |
| `field` | string，可省略 | 相关字段名；多行明细时为 `lines[i].<field>`（第 1 行不带下标） |
| `message` | string | 中文可读说明 |

**真实风格的 `issues` 数组示例**（`enforce=warn`，第 1 行子项合计 7 > 不良品 5，且缺"操机员"）：

```json
"issues": [
  {
    "rule": "V-10",
    "level": "warning",
    "field": "machine_operator",
    "message": "表单既有栏目「操机员」尚未填写"
  },
  {
    "rule": "V-9",
    "level": "warning",
    "field": "sample_project",
    "message": "样品信息的「项目」（M1/M2 等）尚未填写"
  },
  {
    "rule": "V-2",
    "level": "warning",
    "field": "defect_qty",
    "message": "第 1 行：五类不良子项合计(7) 与不良品数(5) 不一致（可能同一片镜片被计入多个类别）"
  }
]
```

> `issues` 的生成顺序固定为：表单级（V-8 → V-10）→ 笔级（V-6 → V-9 → V-10）→ 明细级（按行、每行内 V-4 → V-1 → V-3 → V-2）。
> 保存响应同时给出 `issues`（全部）、`warnings`（level=warning）、`errors`（level=error）。

---

## 3. 接口清单表

### 3.1 认证与用户（`internal/auth/http/http.go`）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| POST | `/api/auth/login` | 登录，返回 token + 用户信息，并下发 Cookie | 否 | 否 |
| POST | `/api/auth/logout` | 注销当前令牌对应的会话 | 是 | 否 |
| GET | `/api/auth/me` | 当前用户信息 | 是 | 否 |
| POST | `/api/auth/change-password` | 修改自己的密码（需原密码），改后全部会话失效 | 是 | 否 |
| GET | `/api/users` | 用户列表（`keyword` 模糊匹配 username / display_name / employee_no） | 是 | **是** |
| POST | `/api/users` | 创建用户 | 是 | **是** |
| PATCH | `/api/users/:id` | 修改用户（显示名 / 工号 / 单位 / 管理员 / 状态 / 重置密码） | 是 | **是** |

### 3.2 字典、单位、参考部件号（`internal/dictionary/http/http.go`）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/dict/items` | 按类别取字典项（默认只返回启用项） | 是 | 否 |
| POST | `/api/dict/items` | 新增或更新字典项（按 `category`+`code` upsert） | 是 | 否 |
| PATCH | `/api/dict/items/:id` | 改标签 / 排序 / 启停 / meta | 是 | 否 |
| GET | `/api/dict/categories` | 全部字典类别常量 | 是 | 否 |
| GET | `/api/units` | 单位列表 | 是 | 否 |
| POST | `/api/units` | 新增或更新单位（按 `code` upsert） | 是 | 否 |
| GET | `/api/dict/drawings` | 参考部件号查询（**仅供参考**，最多 500 条，只返回启用项） | 是 | 否 |
| POST | `/api/dict/drawings/import` | 批量导入参考部件号（结构化 `items` 或整行文本 `rows`） | 是 | 否 |
| GET | `/api/dict/defect-columns` | 不良"类别 × 表面"列定义（供前端渲染表格） | 是 | 否 |

### 3.3 样品信息（`internal/sample/http/http.go`）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| POST | `/api/sample/parse` | 解析原始串（**纯辅助、只写解析缓存**，不影响保存） | 是 | 否 |
| POST | `/api/sample/confirm` | 人工确认/修正结构化字段（**不改写 `sample_raw`**） | 是 | 否 |
| GET | `/api/sample/cache` | 查解析缓存状态（规范化键 / 是否命中 / token） | 是 | 否 |
| GET | `/api/sample/pending` | 待核对清单（`state=ACTIVE` 且 `needs_review=true`，最多 100） | 是 | 否 |

### 3.4 表单与工序记录（`internal/inspection/http/http.go`）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/forms` | 表单分页列表（`keyword` / `state` / `needs_review` / `from` / `to`） | 是 | 否 |
| POST | `/api/forms` | 新建表单（`form_no` 必填，可只带样品信息）→ **201** | 是 | 否 |
| GET | `/api/forms/:formNo` | 取表单 + 全部笔 + 每笔明细（现场"接着填"用） | 是 | 否 |
| PATCH | `/api/forms/:formNo` | 更新表单级字段（字段级合并；表单不存在则创建）→ **200** | 是 | 否 |
| POST | `/api/forms/:formNo/records` | **核心**：新建/更新一笔（字段级合并 + 明细整笔替换 + 批量 `records`） | 是 | 否 |
| GET | `/api/records/:id` | 取一笔完整内容（含 `lines` 与其所属 `form`） | 是 | 否 |
| PATCH | `/api/records/:id` | **核心**：按笔 ID 更新（字段级合并 + 明细整笔替换） | 是 | 否 |
| POST | `/api/records/:id/void` | 逻辑作废（保留数据与历史），可带 `reason` | 是 | 否 |
| GET | `/api/records/:id/history` | 该笔全部历史快照（按 `save_count` 倒序） | 是 | 否 |
| GET | `/api/records/:id/history/diff` | 两版之间的字段级 + 明细级差异 | 是 | 否 |
| GET | `/api/forms/:formNo/sample/pending` | 该表单的样品信息待核对提示（复用 V-8 / V-10） | 是 | 否 |
| GET | `/api/audit-logs` | 操作日志检索（`entity` / `entity_id` / `limit`） | 是 | 否 |

### 3.5 汇总统计（`internal/stats/http/http.go`）

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/api/stats/dimensions` | 可用 `group_by` 维度白名单 | 是 | 否 |
| GET | `/api/stats/summary` | 按维度分组汇总（良率 / 计数），最多 500 组 | 是 | 否 |
| GET | `/api/stats/defects` | 五类不良 × 三表面合计 | 是 | 否 |
| GET | `/api/stats/workload` | 按人员维度的工作量，最多 200 组 | 是 | 否 |
| GET | `/api/stats/incomplete-forms` | 未完成（`is_complete=false`）的笔清单 | 是 | 否 |

### 3.6 其它

| 方法 | 路径 | 说明 | 需登录 | 仅管理员 |
|---|---|---|---|---|
| GET | `/healthz` | 健康检查，返 `{"status":"ok","time":"..."}`（**非统一信封**） | 否 | 否 |
| GET | `/` | 服务名与版本（**非统一信封**） | 否 | 否 |

**合计 39 个接口**（含 `/` 与 `/healthz`）。

---

## 4. 接口详情

### 4.1 认证与用户

#### POST /api/auth/login（公开）

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `username` | string | **是** | 登录名 |
| `password` | string | **是** | 明文密码 |

```json
{ "username": "zhangsan", "password": "Passw0rd!" }
```

**响应**（同时 `Set-Cookie: sid=<token>; Path=/; Max-Age=43200; HttpOnly; SameSite=Lax`）

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "token": "3vJ8kQ0mZ1sT7xR2pL5nB9cD4fG6hJ1kM3nP8qS2tV",
    "expires_at": "2026-05-20T12:30:00Z",
    "user": {
      "id": "8f14e45f-ceea-4a1c-9c1a-2f0f4a1d3b77",
      "username": "zhangsan",
      "display_name": "张三",
      "employee_no": "E1024",
      "unit_id": "u-01",
      "is_admin": false,
      "must_change_pwd": false,
      "status": "active",
      "failed_attempts": 0,
      "last_login_at": "2026-05-20T00:30:00Z",
      "created_at": "2026-01-04T02:00:00Z",
      "updated_at": "2026-05-20T00:30:00Z"
    },
    "note": "浏览器可用 Cookie；其它调用方可使用 Authorization: Bearer <token>"
  }
}
```

**典型错误**：`400` 请求体格式不正确 / 请输入登录名与密码；`401` 登录名或密码不正确（用户不存在与密码错误同一提示，避免账号枚举）；`403` 账号已停用 / 连续登录失败次数过多（账号被暂时锁定，阈值 5 次、10 分钟）。

#### POST /api/auth/logout

无请求体（也可传 `{}`）。取当前请求的令牌（优先级同 §1.2）并置 `revoked_at`，清除 Cookie。

```json
{ "code": "ok", "message": "", "data": { "logged_out": true } }
```

**典型错误**：`401`（未登录 / 会话已失效）。

#### GET /api/auth/me

```json
{ "code": "ok", "message": "", "data": { "user": { "id": "…", "username": "zhangsan", "is_admin": false, "...": "…" }, "is_admin": false } }
```

**典型错误**：`404` 用户不存在（会话存在但用户被物理删除）。

#### POST /api/auth/change-password

| 字段 | 类型 | 必填 |
|---|---|---|
| `old_password` | string | **是** |
| `new_password` | string | **是**（长度 ≥ 6，服务端校验） |

```json
{ "old_password": "Passw0rd!", "new_password": "NewPass2026" }
```

```json
{ "code": "ok", "message": "", "data": { "changed": true, "note": "密码已修改，原会话已全部失效，请重新登录" } }
```

**典型错误**：`400` 新密码长度至少 6 位 / 原密码不正确；`404` 用户不存在。
> 副作用：改密成功后**当前会话立即失效**，前端应清空本地状态并跳登录页。

#### GET /api/users（仅管理员）

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `keyword` | string | 空 | 模糊匹配 `username` / `display_name` / `employee_no` |
| `page` | int | 1 | ≤0 或非法回落 1 |
| `page_size` | int | 20 | ≤0 / 非法 / **>200** 回落 20 |

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "id": "8f14e45f-…", "username": "zhangsan", "display_name": "张三",
        "employee_no": "E1024", "unit_id": "u-01", "is_admin": false,
        "must_change_pwd": false, "status": "active", "failed_attempts": 0,
        "last_login_at": "2026-05-20T00:30:00Z",
        "created_at": "2026-01-04T02:00:00Z", "updated_at": "2026-05-20T00:30:00Z"
      }
    ],
    "total": 1, "page": 1, "page_size": 20
  }
}
```

**典型错误**：`403` 该操作仅管理员可执行。

#### POST /api/users（仅管理员）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `username` | string | **是** | 唯一 |
| `password` | string | **是** | 非空即可（`password.Hash` 只拒绝空白） |
| `display_name` | string | 否 | 空则默认取 `username` |
| `employee_no` | string | 否 | 空串按 `null` 存 |
| `unit_id` | string | 否 | 空串按 `null` 存 |
| `is_admin` | bool | 否 | 默认 `false` |

```json
{ "username": "lisi", "password": "Init#2026", "display_name": "李四", "employee_no": "E1025", "unit_id": "u-01", "is_admin": false }
```

响应 **201**，`data` 为创建出的用户对象（结构同 4.1 用户对象；新建用户 `status=active`、`must_change_pwd=false`、`failed_attempts=0`）。

**典型错误**：`400` 请求体格式不正确 / 登录名（username）不能为空 / 密码不能为空；`409` 登录名已存在: lisi；`403` 非管理员。

#### PATCH /api/users/:id（仅管理员）

**请求体**（全部可选，**指针语义**：给了才改；`status` 只接受 `active` / `disabled`）

| 字段 | 类型 | 说明 |
|---|---|---|
| `display_name` | string | 显示名 |
| `employee_no` | string | 空串 → `null` |
| `unit_id` | string | 空串 → `null` |
| `is_admin` | bool | 管理员标记 |
| `status` | string | `active` \| `disabled` |
| `password` | string | 重置密码（非空），成功后清空失败计数与锁定 |

```json
{ "status": "disabled" }
```

响应 `200`，`data` 为更新后的用户对象。

**典型错误**：`400` 不能取消自己的管理员权限 / 不能停用自己的账号 / 状态只能是 active 或 disabled / 密码不能为空；`404` 用户不存在；`403` 非管理员。
> 副作用：把目标用户置 `disabled` 或重置密码 → **该用户全部会话立即失效**。

---

### 4.2 字典、单位、参考部件号

#### GET /api/dict/items

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `category` | string | 空 = 全部 | 见 §7.2 类别常量 |
| `include_inactive` | string | 仅 `"true"` 时含停用项 | 其它值一律视为 false |

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "id": "b1c2…", "category": "process", "code": "一次加硬", "label": "一次加硬",
        "sort_no": 10, "active": true,
        "meta": "{\"aliases\":[\"一次加硬处理\"]}",
        "created_at": "2026-01-04T02:00:00Z", "updated_at": "2026-01-04T02:00:00Z"
      }
    ],
    "total": 1
  }
}
```

> `meta` 在响应里是**字符串**（内含 JSON 文本），不是对象。

#### POST /api/dict/items

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `category` | string | **是** | 类别常量 |
| `code` | string | **是** | 取值编码 |
| `label` | string | 否 | 空则取 `code` |
| `sort_no` | int | 否 | 默认 0 |
| `active` | bool | 否 | 默认 **true** |
| `meta` | JSON | 否 | 任意 JSON（对象/数组等）；`null` 视为不设置 |

```json
{ "category": "sample_project", "code": "M3", "label": "M3", "sort_no": 30, "active": true, "meta": { "aliases": ["M-3"] } }
```

响应 `200`，`data` 为写入后的字典项（`meta` 为字符串）。已存在 `(category, code)` 时**更新** `label`/`sort_no`/`active`/`meta` 并返回该条。

**典型错误**：`400` 请求体格式不正确 / 字典项的 category 与 code 不能为空。

#### PATCH /api/dict/items/:id

| 字段 | 类型 | 说明 |
|---|---|---|
| `label` | string | 改标签 |
| `sort_no` | int | 改排序 |
| `active` | bool | 启停 |
| `meta` | JSON | 覆盖 meta；`null` / 缺省 → 保持不变 |

```json
{ "active": false }
```

响应 `200`，`data` 为更新后的字典项。**典型错误**：`404` 字典项不存在。

#### GET /api/dict/categories

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "categories": ["process", "sample_project", "sample_model", "sample_spec", "sample_size", "sample_side", "sample_class", "unit"]
  }
}
```

#### GET /api/units

| 查询参数 | 类型 | 默认 |
|---|---|---|
| `include_inactive` | string | 仅 `"true"` 时含停用项 |

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      { "id": "c9f0…", "code": "U01", "name": "一厂", "sort_no": 10, "active": true,
        "created_at": "2026-01-04T02:00:00Z", "updated_at": "2026-01-04T02:00:00Z" }
    ],
    "total": 1
  }
}
```

#### POST /api/units

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `code` | string | **是** | 唯一 |
| `name` | string | 否 | 空则取 `code` |
| `sort_no` | int | 否 | 默认 0 |
| `active` | bool | 否 | 默认 **true** |

```json
{ "code": "U02", "name": "二厂", "sort_no": 20, "active": true }
```

响应 `200`，`data` 为单位对象（按 `code` upsert）。**典型错误**：`400` 单位编码（code）不能为空。

#### GET /api/dict/drawings

| 查询参数 | 类型 | 说明 |
|---|---|---|
| `project` | string | 精确匹配 `project_code` |
| `model` | string | 精确匹配 `model_code` |
| `spec` | string | 精确匹配 `spec_code` |
| `size` | string | 精确匹配 `size_code` |
| `side` | string | 精确匹配 `side_code` |
| `keyword` | string | `drawing_no` 模糊匹配 |

只返回 `active=1` 的记录，按 `seq_no, drawing_no` 排序，**最多 500 条**（无 `limit` 参数）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "id": "7d1a…", "seq_no": "05",
        "drawing_no": "S1 - MD - R - VID2 - MONOC & BINOC",
        "spec_code": "S1", "size_code": "MD", "side_code": "R", "model_code": "VID2",
        "class_code": "MONOC_AND_BINOC", "is_rx": false, "active": true,
        "created_at": "2026-01-04T02:00:00Z", "updated_at": "2026-01-04T02:00:00Z"
      }
    ],
    "total": 1,
    "note": "参考清单仅用于辅助选择；输入与清单不一致不影响保存"
  }
}
```

#### POST /api/dict/drawings/import

| 字段 | 类型 | 说明 |
|---|---|---|
| `items` | object[] | 结构化条目，字段同 `Drawing`（`drawing_no` 必填，空则跳过） |
| `rows` | string[] | 整行文本，按制表符（无制表符则按逗号）拆成「序号 + 部件号」，并尽力拆解维度 |

```json
{
  "items": [
    { "seq_no": "23", "drawing_no": "S2 - LG - R - VID1 RX", "active": true }
  ],
  "rows": [
    "24\tS1 - SM - L - VID2 - MONOC",
    "S2 - MD - R - VID1 RX"
  ]
}
```

```json
{ "code": "ok", "message": "", "data": { "inserted": 2, "updated": 1, "total": 3 } }
```

**典型错误**：`400` 没有可导入的数据（items 或 rows 至少提供一个）。
> 按 `drawing_no` upsert；`items` 元素缺 `drawing_no` 会被**静默跳过**（`total` 仍为入参条数）。

#### GET /api/dict/defect-columns

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "leading_columns": [
      { "key": "total_qty", "label": "检验总数" },
      { "key": "qualified_qty", "label": "合格品" },
      { "key": "defect_qty", "label": "不良品" }
    ],
    "groups": [
      { "code": "spot", "label": "点伤", "columns": [
        { "key": "spot_concave", "label": "凹面" },
        { "key": "spot_convex", "label": "凸面" },
        { "key": "spot_both", "label": "双面" } ] },
      { "code": "scratch", "label": "划伤", "columns": [
        { "key": "scratch_concave", "label": "凹面" },
        { "key": "scratch_convex", "label": "凸面" },
        { "key": "scratch_both", "label": "双面" } ] },
      { "code": "dirt", "label": "脏污", "columns": [
        { "key": "dirt_concave", "label": "凹面" },
        { "key": "dirt_convex", "label": "凸面" },
        { "key": "dirt_both", "label": "双面" } ] },
      { "code": "hard_spot", "label": "加硬点", "columns": [
        { "key": "hard_spot_concave", "label": "凹面" },
        { "key": "hard_spot_convex", "label": "凸面" },
        { "key": "hard_spot_both", "label": "双面" } ] },
      { "code": "debris", "label": "垃圾点", "columns": [
        { "key": "debris_concave", "label": "凹面" },
        { "key": "debris_convex", "label": "凸面" },
        { "key": "debris_both", "label": "双面" } ] }
    ],
    "note": "每个子格填的是该类不良的镜片数量"
  }
}
```

---

### 4.3 样品信息

#### POST /api/sample/parse

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `sample_raw` | string | 否 | 空串也返回成功（`unmatched_reason=raw_only`） |

```json
{ "sample_raw": "M2 - S2 - SM - L - VID1 Rx - MONOC" }
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "parsed": {
      "raw": "M2 - S2 - SM - L - VID1 Rx - MONOC",
      "project": "M2", "model": "VID1 Rx", "spec": "S2", "size": "SM", "side": "L",
      "class": "MONOC", "is_rx": true,
      "tokens": ["M2", "S2", "SM", "L", "VID1", "Rx", "MONOC"],
      "confidence": 0.9
    },
    "drawing": {
      "id": "d31f…", "seq_no": "13", "drawing_no": "S1 - MD - L - VID1 Rx",
      "spec_code": "S1", "size_code": "MD", "side_code": "L", "model_code": "VID1",
      "is_rx": true, "active": true
    },
    "matched": false,
    "unmatched_reason": "no_drawing",
    "from_cache": false,
    "note": "未在参考部件号清单中找到匹配项；这属于正常情况，原始串仍然有效（不影响保存）"
  }
}
```

> `unmatched_reason` 取值：`raw_only`（拆不出结构化字段）/ `partial`（五核心字段不全）/ `no_drawing`（清单无匹配）；字段齐全且匹配时该键**省略**、`matched=true`。
> 具体返回的字段以缓存命中情况为准：命中缓存时按缓存里的解析结果返回。

#### POST /api/sample/confirm

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `sample_raw` | string | **是** | 原始串，作为缓存键（**不会被改写**） |
| `project` / `model` / `spec` / `size` / `side` / `class` | string | 否 | 人工确认后的结构化值 |
| `is_rx` | bool | 否 | 是否处方镜 |

```json
{ "sample_raw": "M2 - S2 - SM - L - VID1 Rx - MONOC", "project": "M2", "model": "VID1 Rx", "spec": "S2", "size": "SM", "side": "L", "class": "MONOC", "is_rx": true }
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "parsed": { "raw": "M2 - S2 - SM - L - VID1 Rx - MONOC", "project": "M2", "model": "VID1 Rx", "spec": "S2", "size": "SM", "side": "L", "class": "MONOC", "is_rx": true, "tokens": ["M2","S2","SM","L","VID1","Rx","MONOC"], "confidence": 0 },
    "note": "已记录人工确认结果；原始串未被改写"
  }
}
```

**典型错误**：`400` 原始串不能为空。
> 只写 `sample_parse_cache`，**不会**改动任何 `forms` 记录。

#### GET /api/sample/cache

| 查询参数 | 类型 | 必填 |
|---|---|---|
| `sample_raw` | string | **是** |

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "normalized_key": "m2 s2 sm l vid1 rx monoc",
    "from_cache": true,
    "tokens": ["M2", "S2", "SM", "L", "VID1", "Rx", "MONOC"],
    "unknown_tokens": ["QC"]
  }
}
```

**典型错误**：`400` 缺少 sample_raw 参数。

#### GET /api/sample/pending

无查询参数（`state=ACTIVE` + `needs_review=true`，固定 LIMIT 100）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      { "id": "…", "form_no": "QS-2026-0007", "sample_raw": "M2 S2 ND2 SMR",
        "sample_parsed_by": "auto", "sample_confidence": 0.5, "sample_verified": false,
        "needs_review": true, "sample_unmatched_reason": "no_drawing",
        "state": "ACTIVE", "created_at": "2026-05-20T00:30:00Z", "updated_at": "2026-05-20T00:30:00Z" }
    ],
    "total": 1,
    "note": "这些表单的样品信息未能与参考清单匹配；不影响数据可用性，仅供人工核对"
  }
}
```

> `items` 里的表单**不含** `records`（列表接口不展开笔）。

---

### 4.4 表单

#### GET /api/forms

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `keyword` | string | 空 | 模糊匹配 `form_no` 或 `sample_raw` |
| `state` | string | **`ACTIVE`** | 精确匹配（大写化后比较）。传 `VOID` 取已作废；**无法一次取全部状态** |
| `needs_review` | string | 仅 `"true"` 时过滤待核对 | |
| `from` | string | 空 | `created_at >= from`（字符串比较） |
| `to` | string | 空 | 服务端拼成 `to + "T23:59:59Z"` 后做 `created_at <=` |
| `sample_project` | string | 空 | 精确匹配样品「项目」（M1/M2 等） |
| `sample_model` | string | 空 | 精确匹配样品「型号」 |
| `sample_spec` | string | 空 | 精确匹配样品「规格」 |
| `sample_size` | string | 空 | 精确匹配样品「大小」 |
| `sample_side` | string | 空 | 精确匹配样品「左右」 |
| `unit` | string | 空 | 精确匹配 `COALESCE(unit_text, unit_id)` |
| `page` / `page_size` | int | 1 / 20 | `page_size > 200` 回落 20 |

> 样品维度与单位筛选可任意组合（多条件为 AND）。例：`?sample_spec=S2&sample_size=MD&state=ACTIVE`。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "id": "3f2c…", "form_no": "QS-2026-0001", "unit_id": "u-01", "unit_text": "一厂",
        "sample_raw": "M1 - S1 - MD - L - VID2 - MONOC & BINOC", "sample_project": "M1",
        "sample_model": "VID2", "sample_spec": "S1", "sample_size": "MD", "sample_side": "L",
        "sample_parsed_by": "auto", "sample_confidence": 0.9, "sample_verified": false,
        "needs_review": false, "state": "ACTIVE",
        "created_by": "u-1001", "created_at": "2026-05-20T00:30:00Z",
        "updated_by": "u-1001", "updated_at": "2026-05-20T00:30:00Z"
      }
    ],
    "total": 1, "page": 1, "page_size": 20
  }
}
```

#### POST /api/forms

**请求体**（`saveReq` 的只取 `form_no` / `form` / `enforce` / `reason`）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `form_no` | string | **是** | 表单号，空 → 400 |
| `form` | object | 否 | 表单级**字段补丁**（只提交本次要改的键） |
| `enforce` | string | 否 | `warn`（默认）\| `strict`（表单级无 error 级规则，实际不影响） |
| `reason` | string | 否 | 变更原因，写入审计日志 |

```json
{
  "form_no": "QS-2026-0001",
  "form": {
    "sample_raw": "M1 - S1 - MD - L - VID2 - MONOC & BINOC",
    "sample_project": "M1", "sample_model": "VID2", "sample_spec": "S1",
    "sample_size": "MD", "sample_side": "L", "unit_text": "一厂"
  },
  "reason": "新建表单"
}
```

响应 **201**，`data` 为 `SaveResult`（`record` 为 `null`，`missing_fields` 为 `null`，`is_complete` 为 `false`）：

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "action": "create_form",
    "form": { "id": "3f2c…", "form_no": "QS-2026-0001", "sample_raw": "M1 - S1 - MD - L - VID2 - MONOC & BINOC",
              "sample_project": "M1", "state": "ACTIVE", "sample_parsed_by": "auto", "sample_confidence": 0,
              "sample_verified": false, "needs_review": false,
              "created_by": "u-1001", "created_at": "2026-05-20T00:30:00Z",
              "updated_by": "u-1001", "updated_at": "2026-05-20T00:30:00Z" },
    "record": null,
    "issues": [],
    "warnings": [],
    "errors": [],
    "missing_fields": null,
    "is_complete": false
  }
}
```

**典型错误**：`400` 表单号（form_no）不能为空 / 存在无法识别的表单字段（`details` 为键名数组）/ 请求体格式不正确。

#### GET /api/forms/:formNo

无查询参数。返回表单 + **全部笔（按 `sequence_no` 升序）** + 每笔的 `lines`。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "id": "3f2c…", "form_no": "QS-2026-0001", "unit_text": "一厂",
    "sample_raw": "M1 - S1 - MD - L - VID2 - MONOC & BINOC", "sample_project": "M1",
    "sample_parsed_by": "auto", "sample_confidence": 0, "sample_verified": false,
    "needs_review": false, "state": "ACTIVE",
    "created_at": "2026-05-20T00:30:00Z", "updated_at": "2026-05-20T01:05:00Z",
    "records": [
      {
        "id": "9a77…", "form_id": "3f2c…", "sequence_no": 1,
        "process_code": "一次加硬", "submitted_by": "张三", "submitted_at": "2026-05-20 08:30:00",
        "inspected_by": "李四", "inspected_at": "2026-05-20 09:10:00",
        "machine_operator": "王五", "handover_by": "赵六",
        "recorded_by": "u-1001", "recorded_at": "2026-05-20T00:30:00Z",
        "save_count": 2, "last_saved_by": "u-1001", "last_saved_at": "2026-05-20T01:05:00Z",
        "is_complete": true, "missing_fields": "[]", "remark": "左右片各 50",
        "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95,
        "state": "ACTIVE", "created_at": "2026-05-20T00:30:00Z", "updated_at": "2026-05-20T01:05:00Z",
        "lines": [
          { "id": "l-01", "record_id": "9a77…", "seq": 1,
            "total_qty": 100, "qualified_qty": 95, "defect_qty": 5,
            "spot_concave": 3, "spot_convex": 0, "spot_both": 0,
            "scratch_concave": 0, "scratch_convex": 0, "scratch_both": 2,
            "dirt_concave": 0, "dirt_convex": 0, "dirt_both": 0,
            "hard_spot_concave": 0, "hard_spot_convex": 0, "hard_spot_both": 0,
            "debris_concave": 0, "debris_convex": 0, "debris_both": 0,
            "created_at": "2026-05-20T01:05:00Z", "updated_at": "2026-05-20T01:05:00Z" }
        ]
      }
    ]
  }
}
```

> 无笔时 `records` 为 `[]`；无明细时 `lines` 为 `[]`。`missing_fields` 是**字符串**（内含 JSON 数组文本），如 `"[]"` 或 `"[\"machine_operator\"]"`。

**典型错误**：`404` 表单不存在: QS-0001。

#### PATCH /api/forms/:formNo

路径即表单号。请求体同 `POST /api/forms`，但**不需要** `form_no`（`form_no` 键在补丁中会被忽略，路径优先）。表单不存在时**创建**。

```json
{ "form": { "unit_text": "二厂", "remark": "例检" }, "reason": "换厂区" }
```

响应 **200**（注意：即使是新建表单也返回 200，而非 201），`data` 为 `SaveResult`，`action` 为 `create_form` 或 `update_form`。

**典型错误**：`400` 存在无法识别的表单字段；`409` 唯一性冲突（如明细行 `seq` 重复）。

---

### 4.5 工序记录（核心）

> `POST /api/forms/{formNo}/records` 与 `PATCH /api/records/{id}` **共用同一个请求结构体 `saveReq` 和同一套保存逻辑**（service `SaveRecord`）。

#### 4.5.1 请求体字段（两个核心接口共用）

| 字段 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|
| `form_no` | string | 见下 | — | `POST /api/forms/{formNo}/records` 取路径值（body 里的会被覆盖）；`PATCH /api/records/{id}` 由记录反查出表单号 |
| `sequence_no` | int | 否 | 空 | 指定则**更新该序号的笔**（不存在则按该序号新建）；不指定则**新建一笔**（序号 = 当前最大值 + 1）。**`PATCH` 时忽略**（路径 ID 优先） |
| `mode` | string | 否 | `merge` | `merge`（字段级合并）\| **只有 `replace` 有特殊含义**；其它任意值/空 = merge |
| `enforce` | string | 否 | `warn` | `warn` \| `strict`（大小写不敏感；其它值 = warn） |
| `reason` | string | 否 | 空 | 变更原因，写入 `record_history.reason` |
| `expected_save_count` | int | 否 | 空 | 乐观锁：仅对**已存在**记录生效，与库里 `save_count` 不等 → **409** |
| `client_request_id` | string | 否 | 空 | **幂等键**（snake_case）。等价于请求头 `X-Idempotency-Key`（**请求头优先**）；也接受 `clientRequestId` / `idempotency_key`。详见 §4.7 |
| `form` | object | 否 | — | 表单级**字段补丁**（对已存在表单做字段级合并；表单不存在则先创建） |
| `record` | object | 否 | — | 笔级**字段补丁** |
| `lines` | object[] | 否 | — | **出现即整笔替换**该笔明细（含空数组 `[]` = 清空明细）；缺省或 `null` = 保持库中明细不变 |
| `records` | object[] | 否 | — | **批量模式**（仅 `POST /api/forms/:formNo/records` 支持）：元素形如 `{"record": {...}, "lines": [...]}`；**整批原子** |

**字段级合并语义（`Patch.Merge`）**

* 请求里**出现**的键才改；**没出现**的键保持原值。
* 可空字段（`*string` / `*bool` / `*int` / `*float64`）显式传 `null` → **清空**。
* 非指针字段（如 `sample_confidence`(number)、`needs_review`/`sample_verified`(bool)、行上的各计数）传 `null` → **保持原值**（不会清成 0）。
* 只读键**静默忽略**（不报错）；**未知键 → 400**（`details` 是键名数组）。因此：
  * 表单补丁里**不能**出现 `records`、`id`、`form_no`、`created_by`、`created_at` 之外的拼写错误键（前 5 个被忽略）。
  * 笔补丁里**不能**出现 `lines` / `form` / 任意拼错键（会被判为无法识别 → 400）。
* `mode=replace` 时：笔级结构先被**清空**（仅保留 `id`、`form_id`、`sequence_no`、`recorded_by`、`recorded_at`、`created_at`、`state`），再套用 `record` 补丁；**未提交 `lines` 时仍读取库中现有明细**（不会清空明细）。

**`lines` 提交即整笔替换**

* 先 `DELETE FROM record_lines WHERE record_id=?` 再逐行 `INSERT`；行 `id` / `record_id` / `created_at` / `updated_at` 由服务端重建，**提交时忽略**。
* `seq <= 0` 时按数组下标自动编为 1..n；**显式传入重复 `seq` 会撞唯一约束 → 409 conflict**。
* 行内字段**缺省即 0**（不是"保持不变"），所以替换时必须提交该行的完整计数。
* `lines` 不是数组（如 `{}`）→ `400 lines 格式不正确`。

**服务端重算（提交这些字段无效）**：`total_qty` / `qualified_qty` / `defect_qty` 由所有明细行求和；`pass_rate = qualified/total*100`（四舍五入 2 位，`total=0` 时为 `null`）；`is_complete` 与 `missing_fields` 由"6 个纸面栏目 + 是否有明细"计算；`save_count` 新建时 = 1、每次保存 +1；`recorded_by`/`recorded_at`（仅新建时）、`last_saved_by`/`last_saved_at`、`updated_at` 由服务端写入。

#### 4.5.2 保存响应（`SaveResult`）

| 字段 | 类型 | 说明 |
|---|---|---|
| `action` | string | `create_form` \| `update_form` \| `create_record` \| `save_record` \| `create_form+create_record`（表单与笔同时新建时用 `+` 连接） |
| `form` | object | 保存后的表单（**不含** `records`） |
| `record` | object\|null | 保存后的笔（含 `lines`） |
| `issues` | Issue[] | 全部校验问题（表单级 → 笔级 → 明细级） |
| `warnings` | Issue[] | `level=warning` 的子集 |
| `errors` | Issue[] | `level=error` 的子集（非 strict 时通常为 `[]`） |
| `missing_fields` | string[] | 缺失项（`lines`、6 个纸面栏目名） |
| `is_complete` | bool | 是否填完整 |

| 方法+路径 | 成功状态码 | 响应 `data` |
|---|---|---|
| POST `/api/forms/:formNo/records`（单笔） | 200 | `SaveResult` |
| POST `/api/forms/:formNo/records`（批量） | 200 | `{"results": [SaveResult, …], "count": n}` |
| PATCH `/api/records/:id` | 200 | `SaveResult` |

#### 4.5.3 场景一：工序现场分次填写（可直接复制）

**第 1 次：新建表单 + 首笔（只填现场刚拿到的信息）**

```http
POST /api/forms/QS-2026-0001/records
Content-Type: application/json
Cookie: sid=<token>
```

```json
{
  "mode": "merge",
  "enforce": "warn",
  "reason": "现场首填",
  "form": {
    "sample_raw": "M1 - S1 - MD - L - VID2 - MONOC & BINOC",
    "sample_project": "M1",
    "sample_model": "VID2",
    "sample_spec": "S1",
    "sample_size": "MD",
    "sample_side": "L",
    "unit_text": "一厂"
  },
  "record": {
    "process_code": "一次加硬",
    "submitted_by": "张三",
    "submitted_at": "2026-05-20 08:30:00"
  },
  "lines": [
    {
      "seq": 1,
      "total_qty": 100,
      "qualified_qty": 95,
      "defect_qty": 5,
      "spot_concave": 3,
      "scratch_both": 2
    }
  ]
}
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "action": "create_form+create_record",
    "form": { "id": "3f2c…", "form_no": "QS-2026-0001", "unit_text": "一厂",
              "sample_raw": "M1 - S1 - MD - L - VID2 - MONOC & BINOC", "sample_project": "M1",
              "sample_model": "VID2", "sample_spec": "S1", "sample_size": "MD", "sample_side": "L",
              "sample_parsed_by": "auto", "sample_confidence": 0, "sample_verified": false,
              "needs_review": false, "state": "ACTIVE",
              "created_by": "u-1001", "created_at": "2026-05-20T00:30:00Z",
              "updated_by": "u-1001", "updated_at": "2026-05-20T00:30:00Z" },
    "record": {
      "id": "9a77…", "form_id": "3f2c…", "sequence_no": 1,
      "process_code": "一次加硬", "submitted_by": "张三", "submitted_at": "2026-05-20 08:30:00",
      "recorded_by": "u-1001", "recorded_at": "2026-05-20T00:30:00Z",
      "save_count": 1, "last_saved_by": "u-1001", "last_saved_at": "2026-05-20T00:30:00Z",
      "is_complete": false, "missing_fields": "[\"inspected_by\",\"inspected_at\",\"machine_operator\",\"handover_by\"]",
      "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95,
      "state": "ACTIVE", "created_at": "2026-05-20T00:30:00Z", "updated_at": "2026-05-20T00:30:00Z",
      "lines": [
        { "id": "b0a1…", "record_id": "9a77…", "seq": 1,
          "total_qty": 100, "qualified_qty": 95, "defect_qty": 5,
          "spot_concave": 3, "spot_convex": 0, "spot_both": 0,
          "scratch_concave": 0, "scratch_convex": 0, "scratch_both": 2,
          "dirt_concave": 0, "dirt_convex": 0, "dirt_both": 0,
          "hard_spot_concave": 0, "hard_spot_convex": 0, "hard_spot_both": 0,
          "debris_concave": 0, "debris_convex": 0, "debris_both": 0,
          "created_at": "2026-05-20T00:30:00Z", "updated_at": "2026-05-20T00:30:00Z" }
      ]
    },
    "issues": [
      { "rule": "V-10", "level": "warning", "field": "inspected_by", "message": "表单既有栏目「检验人」尚未填写" },
      { "rule": "V-10", "level": "warning", "field": "inspected_at", "message": "表单既有栏目「检验时间」尚未填写" },
      { "rule": "V-10", "level": "warning", "field": "machine_operator", "message": "表单既有栏目「操机员」尚未填写" },
      { "rule": "V-10", "level": "warning", "field": "handover_by", "message": "表单既有栏目「交接员」尚未填写" }
    ],
    "warnings": [ "（同上 4 条）" ],
    "errors": [],
    "missing_fields": ["inspected_by", "inspected_at", "machine_operator", "handover_by"],
    "is_complete": false
  }
}
```

**第 2 次：拿响应里的 `record.id` 补填检验栏目（只提交要改的字段，不碰 `lines`）**

```http
PATCH /api/records/9a77e2b0-6d1c-4f0a-9c3e-1a2b3c4d5e6f
Content-Type: application/json
```

```json
{
  "expected_save_count": 1,
  "reason": "补填检验信息",
  "record": {
    "inspected_by": "李四",
    "inspected_at": "2026-05-20 09:10:00",
    "machine_operator": "王五",
    "handover_by": "赵六",
    "remark": "左右片各 50"
  }
}
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "action": "save_record",
    "form": { "id": "3f2c…", "form_no": "QS-2026-0001", "state": "ACTIVE", "…": "…" },
    "record": {
      "id": "9a77…", "form_id": "3f2c…", "sequence_no": 1,
      "process_code": "一次加硬", "submitted_by": "张三", "submitted_at": "2026-05-20 08:30:00",
      "inspected_by": "李四", "inspected_at": "2026-05-20 09:10:00",
      "machine_operator": "王五", "handover_by": "赵六", "remark": "左右片各 50",
      "save_count": 2, "last_saved_by": "u-1001", "last_saved_at": "2026-05-20T01:05:00Z",
      "is_complete": true, "missing_fields": "[]",
      "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95,
      "state": "ACTIVE", "lines": [ { "seq": 1, "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "…": "…" } ]
    },
    "issues": [],
    "warnings": [],
    "errors": [],
    "missing_fields": [],
    "is_complete": true
  }
}
```

**第 3 次：发现明细填错，重交整份明细（`lines` 提交即整笔替换）**

```json
{
  "expected_save_count": 2,
  "mode": "merge",
  "reason": "修正不良子项",
  "lines": [
    { "seq": 1, "total_qty": 100, "qualified_qty": 95, "defect_qty": 5,
      "spot_concave": 2, "spot_convex": 1, "scratch_both": 2 }
  ]
}
```

> 明细是"全量提交"：**必须带上该笔所有行**，否则未提交的行会消失。只想改笔级字段就别传 `lines`。

#### 4.5.4 场景二：次日一次性完整录入（批量 `records`）

```http
POST /api/forms/QS-2026-0002/records
Content-Type: application/json
```

```json
{
  "mode": "merge",
  "enforce": "strict",
  "reason": "次日集中补录",
  "form": {
    "sample_raw": "M2 - S2 - SM - L - VID1 Rx - MONOC",
    "sample_project": "M2",
    "sample_model": "VID1 Rx",
    "sample_spec": "S2",
    "sample_size": "SM",
    "sample_side": "L",
    "unit_text": "二厂"
  },
  "records": [
    {
      "record": {
        "process_code": "一次加硬",
        "submitted_by": "张三", "submitted_at": "2026-05-21 08:00:00",
        "inspected_by": "李四", "inspected_at": "2026-05-21 10:00:00",
        "machine_operator": "王五", "handover_by": "赵六"
      },
      "lines": [
        { "seq": 1, "total_qty": 200, "qualified_qty": 196, "defect_qty": 4,
          "spot_concave": 2, "scratch_both": 1, "dirt_convex": 1 }
      ]
    },
    {
      "record": {
        "process_code": "二次加硬",
        "submitted_by": "张三", "submitted_at": "2026-05-21 13:00:00",
        "inspected_by": "李四", "inspected_at": "2026-05-21 15:30:00",
        "machine_operator": "王五", "handover_by": "赵六"
      },
      "lines": [
        { "seq": 1, "total_qty": 150, "qualified_qty": 150, "defect_qty": 0 }
      ]
    }
  ]
}
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "results": [
      { "action": "create_form+create_record", "form": { "form_no": "QS-2026-0002", "…": "…" },
        "record": { "id": "r-01", "sequence_no": 1, "save_count": 1, "is_complete": true,
                    "total_qty": 200, "qualified_qty": 196, "defect_qty": 4, "pass_rate": 98,
                    "lines": [ { "seq": 1, "…": "…" } ] },
        "issues": [], "warnings": [], "errors": [], "missing_fields": [], "is_complete": true },
      { "action": "create_record", "form": { "form_no": "QS-2026-0002", "…": "…" },
        "record": { "id": "r-02", "sequence_no": 2, "save_count": 1,
                    "total_qty": 150, "qualified_qty": 150, "defect_qty": 0, "pass_rate": 100,
                    "lines": [ { "seq": 1, "total_qty": 150, "qualified_qty": 150, "defect_qty": 0, "…": "…" } ] },
        "issues": [], "warnings": [], "errors": [], "missing_fields": [], "is_complete": true }
    ],
    "count": 2
  }
}
```

**批量模式的注意事项（务必按代码行为处理）**

1. 只支持 `POST /api/forms/:formNo/records`；`PATCH /api/records/:id` 即使带 `records` 也**不会**走批量逻辑（该字段被忽略）。
2. 元素结构必须是 `{"record": {...}, "lines": [...]}`。**元素若是对象但缺 `record` 键，会被解析成空补丁且不报错**（结果：新建一笔空白记录）。只有元素根本不是 JSON 对象时才会走"把整个元素当作 `record` 补丁"的兼容分支，而这种元素随后通常以 `400 record 补丁格式不正确` 失败。
3. **顶层 `sequence_no` 与 `expected_save_count` 在批量模式下不生效**（不会被复制到各笔）→ 每一笔都新建（序号自动递增），且无乐观锁。
4. **整批在同一个事务内保存，是原子的**：任一笔失败（400/409/422）→ 整批回滚，**表单与已保存的笔都不会落库**，响应含 `"atomic": true`。错误消息会带"第 N 笔"用于定位。
5. 顶层 `form` / `mode` / `enforce` / `reason` 会套用到每个元素；`mode=replace` 时先清空笔级字段。
6. 批量成功时 `results` 与 `records` 一一对应、顺序一致；`count` 为笔数。

#### 4.5.5 保存的典型错误

| 场景 | 响应 |
|---|---|
| 表单号为空 | `400 {"code":"bad_request","message":"表单号（form_no）不能为空"}` |
| 表单补丁有未知键 | `400 {"code":"bad_request","message":"存在无法识别的表单字段","details":["sample_project2"]}` |
| 笔补丁有未知键（含误写 `lines`） | `400 {"code":"bad_request","message":"存在无法识别的工序记录字段","details":["lines"]}` |
| `lines` 不是数组 | `400 {"code":"bad_request","message":"lines 格式不正确: json: cannot unmarshal object into Go value of type []inspection.RecordLine"}` |
| PATCH 找不到笔 | `404 {"code":"not_found","message":"工序记录不存在"}` |
| 笔不属于路径上的表单 | `400 {"code":"bad_request","message":"该工序记录不属于指定表单"}` |
| 乐观锁冲突 | 见下 |
| strict 校验失败 | 见 §2.3 |
| `lines` 内 `seq` 重复 | `409 {"code":"conflict","message":"明细行序号重复（V-7：(record_id, seq) 唯一）"}` |

**409 乐观锁示例**

```json
{
  "code": "conflict",
  "message": "该记录已被他人修改（期望保存次数 1，实际 2）",
  "details": {
    "current": {
      "id": "9a77…", "form_id": "3f2c…", "sequence_no": 1, "save_count": 2,
      "is_complete": true, "missing_fields": "[]", "total_qty": 100,
      "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95, "state": "ACTIVE",
      "last_saved_by": "u-1002", "last_saved_at": "2026-05-20T02:00:00Z"
    },
    "current_save_count": 2
  }
}
```

> 前端处理建议：用 `details.current` 覆盖本地草稿，提示"该笔已被 <last_saved_by> 修改"，由用户确认后用新的 `expected_save_count` 重试。

#### 4.5.6 GET /api/records/:id

无查询参数。返回笔 + 明细 + 所属表单。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "record": {
      "id": "9a77…", "form_id": "3f2c…", "sequence_no": 1,
      "process_code": "一次加硬", "save_count": 2, "is_complete": true, "missing_fields": "[]",
      "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "pass_rate": 95, "state": "ACTIVE",
      "lines": [ { "id": "b0a1…", "record_id": "9a77…", "seq": 1, "total_qty": 100, "qualified_qty": 95, "defect_qty": 5, "…": "…" } ]
    },
    "form": { "id": "3f2c…", "form_no": "QS-2026-0001", "state": "ACTIVE", "…": "…" }
  }
}
```

**典型错误**：`404` 工序记录不存在。

#### 4.5.7 POST /api/records/:id/void

**请求体**（可省略整个 body；解析失败被忽略，不会报 400）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `reason` | string | 否 | 作废原因，写入 `void_reason` 与审计日志 |

```json
{ "reason": "现场重复登记" }
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "id": "9a77…", "form_id": "3f2c…", "sequence_no": 1, "save_count": 2,
    "state": "VOID", "voided_at": "2026-05-21T01:00:00Z", "void_reason": "现场重复登记",
    "lines": null
  }
}
```

> 逻辑作废：数据、明细、历史、审计全部保留；`save_count` 不变；`is_complete` / 汇总不变。统计默认排除（除非 `include_void=true`）。

**典型错误**：`404` 工序记录不存在。

#### 4.5.8 GET /api/records/:id/history

无查询参数，按 `save_count` 倒序返回全部快照（**保存前**的内容）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "id": "h-02", "record_id": "9a77…", "form_id": "3f2c…", "save_count": 2,
        "form_snapshot": "{\"id\":\"3f2c…\",\"form_no\":\"QS-2026-0001\",\"…\":\"…\"}",
        "record_snapshot": "{\"id\":\"9a77…\",\"save_count\":1,\"…\":\"…\"}",
        "lines_snapshot": "[{\"seq\":1,\"total_qty\":100,\"…\":\"…\"}]",
        "saved_by": "u-1001", "saved_at": "2026-05-20T00:30:00Z",
        "changed_by": "u-1001", "changed_at": "2026-05-20T01:05:00Z",
        "reason": "补填检验信息"
      }
    ],
    "total": 1
  }
}
```

> `form_snapshot` / `record_snapshot` / `lines_snapshot` 都是**字符串**（内含 JSON 文本），需前端二次 `JSON.parse`。首次新建不写快照（无历史）。

#### 4.5.9 GET /api/records/:id/history/diff

| 查询参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `from` | string | **是** | 起始快照 ID（`history` 返回的 `id`），不存在 → 404 |
| `to` | string | 否 | 目标快照 ID；缺省或 `current` → 与当前笔比较 |

```http
GET /api/records/9a77…/history/diff?from=h-01&to=current
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "from_save_count": 1,
    "to_save_count": 2,
    "changes": [
      { "scope": "record", "field": "save_count", "label": "save_count", "from": 1, "to": 2 },
      { "scope": "record", "field": "is_complete", "label": "is_complete", "from": false, "to": true },
      { "scope": "record", "field": "missing_fields", "label": "missing_fields",
        "from": "[\"inspected_by\",\"inspected_at\",\"machine_operator\",\"handover_by\"]", "to": "[]" },
      { "scope": "line", "field": "scratch_both", "label": "scratch_both", "index": 1, "from": 1, "to": 2 }
    ],
    "total": 4
  }
}
```

* `scope`：`record` \| `line`；`index` 仅在 `line` 时有效（行序号，从 1 开始）。
* 比较的笔级字段：`total_qty`、`qualified_qty`、`defect_qty`、`pass_rate`、`is_complete`、`missing_fields`、`save_count`、`last_saved_by`、`last_saved_at`。
* 比较的明细字段：三个计数 + 15 个不良子项 + `process_code` / `process_text` / `remark`；只在一侧存在的行以摘要字符串表示（`"总数 100 / 合格 95 / 不良 5"`，另一侧为 `null`）。
* `label` 取字段中文名映射，**未映射的字段名原样返回**（如 `defect_qty`）；已知字段如 `is_complete` 也不在映射表内，同样是原名。
* `from` / `to` 反序列化快照失败 → `400`。

**典型错误**：`404` 历史快照不存在: xxx / 工序记录不存在（`to=current` 但记录已不在）。

#### 4.5.10 GET /api/forms/:formNo/sample/pending

无查询参数。复用 `ValidateForm`，返回该表单的样品信息提示（恒为 warning）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "form_no": "QS-2026-0007",
    "needs_review": true,
    "issues": [
      { "rule": "V-8", "level": "warning", "field": "sample_raw",
        "message": "样品信息与参考部件号清单不匹配，已标记待核对（不影响保存），原因: no_drawing" },
      { "rule": "V-10", "level": "warning", "field": "unit_id", "message": "「单位」尚未填写" }
    ]
  }
}
```

**典型错误**：`404` 表单不存在。

#### 4.5.11 GET /api/audit-logs

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `entity` | string | 空 = 全部 | `form` \| `form_record` |
| `entity_id` | string | 空 | 表单 ID 或笔 ID |
| `limit` | int | 100 | ≤0 / >500 / 非法 → 100 |

按 `id` 倒序（即时间倒序）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "id": 1024,
        "actor_id": "u-1001", "actor_name": "张三",
        "action": "save_record", "entity": "form_record", "entity_id": "9a77…",
        "before": "{\"form\":{…},\"record\":{…},\"lines\":[…] }",
        "after": "{\"form_no\":\"QS-2026-0001\",\"mode\":\"merge\",\"record\":{…},\"lines\":[…] }",
        "ip": "10.0.0.31", "user_agent": "Mozilla/5.0 …",
        "created_at": "2026-05-20T01:05:00Z"
      }
    ],
    "total": 1
  }
}
```

* `before`：更新前快照（`{"form","record","lines"}` 的 JSON 文本；新建与作废时为缺省）。
* `after`：**本次请求体原样**（`saveReq` 序列化结果，含 `lines`；客户端传的 `form_no` 也在其中）。
* `detail`：仅作废接口写入原因。
* `action` 取值：`create_form`、`update_form`（entity=`form`）；`create_record`、`save_record`、`create_form+create_record`、`void_record`（entity=`form_record`）。
  > 代码注释里提到的 `replace_record` **不会出现**（`mode=replace` 时 action 仍是 `save_record`）。

---

### 4.7 幂等（写接口重复提交保护）

写方法（`POST` / `PATCH` / `PUT` / `DELETE`）都支持幂等键，用于**弱网重试**场景——避免重复写入（重复的历史快照与操作日志、重复的笔）。

| 项 | 说明 |
|---|---|
| 键来源（优先级） | 1) 请求头 `X-Idempotency-Key` → 2) 请求头 `Idempotency-Key` → 3) 请求体 `client_request_id` → 4) `clientRequestId` → 5) `idempotency_key` / `idempotencyKey` |
| 作用域 | `(user_id, 幂等键)` 唯一；**不同用户互不影响** |
| 命中且请求体一致 | **直接回放首次响应**（同状态码、同响应体），响应头 `X-Idempotent-Replay: true`，**不执行**任何写操作 |
| 命中但请求体不同 | `409 {"code":"conflict","message":"幂等键已被使用且请求内容不同…","details":{"idempotency_key":"…"}}` |
| 失败响应 | **非 2xx 不写入幂等记录**，因此可以用同一个键重试直到成功 |
| 有效期 | 环境变量 `IDEMPOTENCY_TTL`，默认 `24h`；设为 `<=0` 时**关闭**该功能 |
| 存储 | 表 `idempotency_requests`；读取时顺手清理过期记录 |
| 键的持久性 | 键由客户端生成（建议 UUID）。**同一业务动作务必复用同一个键**；不同动作不要复用同一个键 |

**示例：弱网重试不会写两遍**

```bash
# 第 1 次（网络超时但服务端已成功）
curl -X POST 'http://127.0.0.1:8080/api/forms/M-260908-01/records' \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -H 'X-Idempotency-Key: 6f1a8b2c-…' \
  -d '{"record":{"process_code":"一次加硬"},"lines":[{"total_qty":100,"qualified_qty":100}]}'

# 客户端重试（同一个键）→ 回放首次响应，不会新增第二笔
curl -X POST 'http://127.0.0.1:8080/api/forms/M-260908-01/records' \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -H 'X-Idempotency-Key: 6f1a8b2c-…' \
  -d '{"record":{"process_code":"一次加硬"},"lines":[{"total_qty":100,"qualified_qty":100}]}'
# 响应头含：X-Idempotent-Replay: true，body 与首次完全一致
```

**注意**

* 幂等记录只保存**响应体与状态码**；回放时不会重新设置其它业务响应头。
* 幂等表的读写故障**不会阻断业务**（服务端记录错误后照常处理请求）——保护机制不会成为故障点。
* 与**乐观锁**的区别：乐观锁（`expected_save_count`）防的是"两个不同的人同时改同一笔"；幂等键防的是"同一个请求被重复提交"。

---

### 4.6 汇总统计

见 §6。

#### GET /api/stats/dimensions

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "dimensions": ["day", "form_no", "handover_by", "inspected_by", "machine_operator", "month",
      "process", "sample_class", "sample_model", "sample_project", "sample_raw", "sample_side",
      "sample_size", "sample_spec", "submitted_by", "unit"]
  }
}
```

#### GET /api/stats/summary

**查询参数**：`group_by`（可重复、可逗号分隔）+ §6.2 筛选参数。

```http
GET /api/stats/summary?group_by=sample_project,sample_size&from=2026-05-01&to=2026-05-31&process=一次加硬
```

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "group_by": ["sample_project", "sample_size"],
    "groups": [
      { "key": "M1 | MD", "record_count": 12, "total_qty": 1200, "qualified_qty": 1152,
        "defect_qty": 48, "pass_rate": 96 },
      { "key": "M2 | SM", "record_count": 5, "total_qty": 500, "qualified_qty": 480,
        "defect_qty": 20, "pass_rate": 96 }
    ],
    "total": { "key": "", "record_count": 17, "total_qty": 1700, "qualified_qty": 1632,
               "defect_qty": 68, "pass_rate": 96 },
    "note": "不良子项合计可能大于不良品数（同一片镜片可计入多个类别，见需求 Q5）"
  }
}
```

* 多维度时 `key` 用 `" | "` 连接（空值表现为空串，如 `"M1 | "`）。
* 分组结果按 `record_count` 倒序，**最多 500 组**；`group_by` 为空时 `groups` 为 `[]`，只返回 `total`。
* `total.key` 恒为空串 `""`。

**典型错误**：`400 {"code":"bad_request","message":"不支持的分组维度: sampleSize","details":{"supported":["day","form_no", …]}}`（注意：**只接受 snake_case 的 `sample_size`**，`sampleSize` 会报错）。

#### GET /api/stats/defects

**查询参数**：仅 §6.2 筛选参数（无 `group_by`）。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "point":     { "concave": 12, "convex": 4, "both": 2, "total": 18 },
    "scratch":   { "concave": 7,  "convex": 0, "both": 5, "total": 12 },
    "dirt":      { "concave": 3,  "convex": 1, "both": 0, "total": 4 },
    "hard_spot": { "concave": 0,  "convex": 2, "both": 0, "total": 2 },
    "debris":    { "concave": 1,  "convex": 0, "both": 0, "total": 1 },
    "note": "同一片镜片可能被计入多个不良类别（需求 Q5，待定）"
  }
}
```

> ⚠️ 点伤这一组的键名是 **`point`**（不是 `spot`，尽管明细列名前缀是 `spot_*`）。

#### GET /api/stats/workload

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `by` | string | `inspected_by` | `inspected_by` \| `recorded_by` \| `last_saved_by` |

外加 §6.2 筛选参数。按笔数倒序，**最多 200 组**；空值统一归入 `"(未填)"`。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "by": "inspected_by",
    "items": [
      { "key": "李四", "record_count": 31, "total_qty": 3100, "qualified_qty": 2976, "defect_qty": 124, "pass_rate": 96 },
      { "key": "(未填)", "record_count": 2, "total_qty": 200, "qualified_qty": 200, "defect_qty": 0, "pass_rate": 100 }
    ],
    "total": 2
  }
}
```

**典型错误**：`400 不支持的人员维度: xxx`。

#### GET /api/stats/incomplete-forms

| 查询参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `limit` | int | 100 | ≤0 / >500 / 非法 → 100 |

条件固定为 `r.state='ACTIVE' AND r.is_complete=0 AND f.state='ACTIVE'`，按 `last_saved_at` 倒序。

```json
{
  "code": "ok",
  "message": "",
  "data": {
    "items": [
      {
        "form_id": "3f2c…", "form_no": "QS-2026-0001",
        "record_id": "9a77…", "sequence_no": 1, "process_code": "一次加硬",
        "missing_fields": "[\"machine_operator\",\"handover_by\"]",
        "last_saved_at": "2026-05-20T01:05:00Z"
      }
    ],
    "total": 1,
    "note": "这些笔尚未填完整（缺失表单既有栏目或计数明细），可供跟进"
  }
}
```

---

## 5. 数据模型速查

> JSON 键 = 数据库列名（snake_case）。`omitempty` 的指针/可选字段在为空时**不会出现在 JSON 中**。

### 5.1 Form（表单，表 `forms`，`form_no` 唯一）

| 字段 | 类型 | JSON 键 | 说明 |
|---|---|---|---|
| ID | string | `id` | UUIDv4，服务端生成。**只读（补丁静默忽略）** |
| FormNo | string | `form_no` | 表单号，必填且唯一。**只读**（`POST /api/forms` 用顶层 `form_no` 传入；存在 `patch["form_no"]` 但被忽略） |
| UnitID | string\|null | `unit_id` | 单位 ID（可写）。统计/筛选中 `COALESCE(unit_text, unit_id)`，**`unit_text` 优先** |
| UnitText | string\|null | `unit_text` | 单位文本（可写） |
| SampleRaw | string | `sample_raw` | 样品原始串（事实来源，任何时候都不被解析器改写） |
| SampleProject | string\|null | `sample_project` | 项目 M1/M2 |
| SampleModel | string\|null | `sample_model` | 型号 VID1 / VID1 Rx / VID2 |
| SampleSpec | string\|null | `sample_spec` | 规格 S1/S2 |
| SampleSize | string\|null | `sample_size` | 大小 MD/SM/LG |
| SampleSide | string\|null | `sample_side` | 左右 L/R |
| SampleClass | string\|null | `sample_class` | 结构类别（含义未确认，冗余维度） |
| SampleIsRx | bool\|null | `sample_is_rx` | 是否处方镜 |
| SampleDrawingID | string\|null | `sample_drawing_id` | 匹配到的参考部件号 ID（可写，一般不手工填） |
| SampleUnmatchedReason | string\|null | `sample_unmatched_reason` | `raw_only` / `partial` / `no_drawing`（可写） |
| SampleParsedBy | string | `sample_parsed_by` | 解析来源，默认 `auto` |
| SampleConfidence | number | `sample_confidence` | 置信度（非指针：传 `null` 保持原值） |
| SampleVerified | bool | `sample_verified` | 人工确认标记（非指针） |
| NeedsReview | bool | `needs_review` | 待核对（非指针） |
| UnknownTokens | string\|null | `unknown_tokens` | 未识别 token 的 JSON 文本 |
| State | string | `state` | `ACTIVE` / `VOID`（默认 ACTIVE；模型上可写） |
| VoidedAt / VoidReason | string\|null | `voided_at` / `void_reason` | 作废信息（可写，正常走记录作废接口） |
| Remark | string\|null | `remark` | 备注 |
| CreatedBy | string\|null | `created_by` | 创建人。**只读**（新建时写当前用户） |
| CreatedAt | string | `created_at` | UTC RFC3339（秒精度）。**只读** |
| UpdatedBy | string\|null | `updated_by` | **服务端每次保存覆盖为当前用户**（传了也会被覆盖） |
| UpdatedAt | string | `updated_at` | 服务端维护 |
| Records | FormRecord[] | `records` | **不是数据库列**：仅 `GET /api/forms/:formNo` 返回；出现在 `form` 补丁里会 → 400 |

**表单补丁可用键**：`unit_id`、`unit_text`、`sample_raw`、`sample_project`、`sample_model`、`sample_spec`、`sample_size`、`sample_side`、`sample_class`、`sample_is_rx`、`sample_drawing_id`、`sample_unmatched_reason`、`sample_parsed_by`、`sample_confidence`、`sample_verified`、`needs_review`、`unknown_tokens`、`state`、`voided_at`、`void_reason`、`remark`、`updated_by`、`updated_at`。
**静默忽略**：`id`、`form_no`、`created_by`、`created_at`。**其它任何键 → 400。**

### 5.2 FormRecord（一笔工序填写，表 `form_records`，`(form_id, sequence_no)` 唯一）

| 字段 | 类型 | JSON 键 | 说明 |
|---|---|---|---|
| ID | string | `id` | UUIDv4。**只读** |
| FormID | string | `form_id` | **只读** |
| SequenceNo | int | `sequence_no` | 笔序号。**只读**，新建时用顶层 `sequence_no` 指定或自动取 `MAX+1` |
| ProcessCode / ProcessText | string\|null | `process_code` / `process_text` | 工序（可写，字典类别 `process`） |
| SubmittedBy | string\|null | `submitted_by` | 送检人（纸面栏目，可写，字符串） |
| SubmittedAt | string\|null | `submitted_at` | 送检时间（纸面栏目，可写，**自由文本**，建议 `YYYY-MM-DD HH:MM:SS` 或 RFC3339） |
| InspectedBy | string\|null | `inspected_by` | 检验人（可写） |
| InspectedAt | string\|null | `inspected_at` | 检验时间（可写，同时间格式建议；**日/月分组按此前 10/7 字符截取**） |
| MachineOperator | string\|null | `machine_operator` | 操机员（可写） |
| HandoverBy | string\|null | `handover_by` | 交接员（可写） |
| RecordedBy | string\|null | `recorded_by` | 首次录入人；**新建时由服务端写入当前用户**（已存在时补丁可写，但不建议） |
| RecordedAt | string\|null | `recorded_at` | 首次录入时间；同上 |
| SaveCount | int | `save_count` | **只读**，新建 = 1，每次保存 +1（乐观锁基准） |
| LastSavedBy | string\|null | `last_saved_by` | **服务端每次覆盖**为当前用户 |
| LastSavedAt | string\|null | `last_saved_at` | **服务端每次覆盖** |
| IsComplete | bool | `is_complete` | **只读，服务端重算**：6 个纸面栏目全非空且至少有 1 行明细 |
| MissingFields | string\|null | `missing_fields` | **只读**，缺失项 JSON 数组的**字符串**，如 `"[\"machine_operator\"]"` |
| Remark | string\|null | `remark` | 备注（可写） |
| TotalQty | int | `total_qty` | **只读，服务端按明细求和** |
| QualifiedQty | int | `qualified_qty` | **只读，服务端求和** |
| DefectQty | int | `defect_qty` | **只读，服务端求和** |
| PassRate | number\|null | `pass_rate` | **只读**：`qualified/total*100` 四舍五入 2 位；`total=0` 时为 **`null`**（`omitempty`） |
| State | string | `state` | `ACTIVE` / `VOID`。**只读**（补丁忽略），只能通过 `POST /api/records/:id/void` 改 |
| VoidedAt / VoidReason | string\|null | `voided_at` / `void_reason` | **只读** |
| CreatedAt | string | `created_at` | **只读** |
| UpdatedAt | string | `updated_at` | 服务端维护 |
| Lines | RecordLine[] | `lines` | **只在响应里出现**；放进 `record` 补丁 → 400 |

**笔补丁可用键**：`process_code`、`process_text`、`submitted_by`、`submitted_at`、`inspected_by`、`inspected_at`、`machine_operator`、`handover_by`、`recorded_by`、`recorded_at`、`last_saved_by`、`last_saved_at`、`remark`、`updated_at`（后 5 个会被服务端覆盖或不应使用）。
**静默忽略（只读）**：`id`、`form_id`、`sequence_no`、`save_count`、`is_complete`、`missing_fields`、`total_qty`、`qualified_qty`、`defect_qty`、`pass_rate`、`state`、`voided_at`、`void_reason`、`created_at`。**其它任何键（含 `lines`、`form`）→ 400。**

### 5.3 RecordLine（明细行，表 `record_lines`，`(record_id, seq)` 唯一）

| 字段 | 类型 | JSON 键 | 说明 |
|---|---|---|---|
| ID | string | `id` | 服务端重建，**提交时忽略** |
| RecordID | string | `record_id` | 服务端设置，**提交时忽略** |
| Seq | int | `seq` | 行号。`<=0` 时按数组下标自动编 1..n；重复 → `409 conflict` |
| TotalQty | int | `total_qty` | 检验总数（缺省 0） |
| QualifiedQty | int | `qualified_qty` | 合格品 |
| DefectQty | int | `defect_qty` | 不良品 |
| SpotConcave / SpotConvex / SpotBoth | int | `spot_concave` / `spot_convex` / `spot_both` | 点伤·凹/凸/双面 |
| ScratchConcave / ScratchConvex / ScratchBoth | int | `scratch_concave` / `scratch_convex` / `scratch_both` | 划伤·凹/凸/双面 |
| DirtConcave / DirtConvex / DirtBoth | int | `dirt_concave` / `dirt_convex` / `dirt_both` | 脏污·凹/凸/双面 |
| HardSpotConcave / HardSpotConvex / HardSpotBoth | int | `hard_spot_concave` / `hard_spot_convex` / `hard_spot_both` | 加硬点·凹/凸/双面 |
| DebrisConcave / DebrisConvex / DebrisBoth | int | `debris_concave` / `debris_convex` / `debris_both` | 垃圾点·凹/凸/双面 |
| ProcessCode / ProcessText | string\|null | `process_code` / `process_text` | 工序同时保留在行上（不收敛语义） |
| Remark | string\|null | `remark` | 行备注 |
| CreatedAt / UpdatedAt | string | `created_at` / `updated_at` | 服务端维护 |

> 15 个不良子项之和 ≠ 不良品数是**允许**的（同一片镜片可计入多个类别，V-2 恒为警告）。

### 5.4 其它响应结构

| 结构 | 字段（JSON） |
|---|---|
| `User` | `id`、`username`、`display_name`、`employee_no`、`unit_id`、`is_admin`、`must_change_pwd`、`status`、`failed_attempts`、`locked_until`、`last_login_at`、`created_at`、`updated_at`（`password_hash` 为 `json:"-"`，不会下发） |
| `dictionary.Item` | `id`、`category`、`code`、`label`、`sort_no`、`active`、`meta`（**字符串**）、`created_at`、`updated_at` |
| `dictionary.Unit` | `id`、`code`、`name`、`sort_no`、`active`、`created_at`、`updated_at` |
| `dictionary.Drawing` | `id`、`seq_no`、`drawing_no`、`project_code`、`model_code`、`spec_code`、`size_code`、`side_code`、`class_code`、`is_rx`、`active`、`note`、`created_at`、`updated_at` |
| `HistoryEntry` | `id`、`record_id`、`form_id`、`save_count`、`form_snapshot`、`record_snapshot`、`lines_snapshot`（后三者是**字符串**）、`saved_by`、`saved_at`、`changed_by`、`changed_at`、`reason` |
| `AuditEntry` | `id`(number)、`actor_id`、`actor_name`、`action`、`entity`、`entity_id`、`before`、`after`、`ip`、`user_agent`、`detail`、`created_at` |
| `FieldChange`（diff） | `scope`、`field`、`label`、`index`、`from`、`to` |
| `SaveResult` | `action`、`form`、`record`、`issues`、`warnings`、`errors`、`missing_fields`、`is_complete` |

**时间格式**：服务端写入的时间（`created_at`、`updated_at`、`last_saved_at`、`voided_at`、`expires_at` 等）统一为 **UTC RFC3339 秒精度**，如 `2026-05-20T00:30:00Z`；纸面栏目（`submitted_at`、`inspected_at`）是**客户端自由文本**，服务端按字符串比较，建议前端统一用 `YYYY-MM-DD HH:MM:SS` 或 RFC3339，否则 V-6 与 `day`/`month` 分组会失真。

---

## 6. 汇总统计

### 6.1 可用 `group_by` 维度（白名单全量，共 16 个）

| `group_by` 取值 | 对应表达式 | 说明 |
|---|---|---|
| `sample_project` | `f.sample_project` | 项目（M1/M2） |
| `sample_model` | `f.sample_model` | 型号 |
| `sample_spec` | `f.sample_spec` | 规格 |
| `sample_size` | `f.sample_size` | 大小 |
| `sample_side` | `f.sample_side` | 左右 |
| `sample_class` | `f.sample_class` | 结构类别 |
| `sample_raw` | `f.sample_raw` | 样品原始串（**基数高**） |
| `form_no` | `f.form_no` | 表单号（**基数高**） |
| `process` | `r.process_code` | 工序 |
| `unit` | `COALESCE(f.unit_text, f.unit_id)` | 单位（`unit_text` 优先） |
| `inspected_by` | `r.inspected_by` | 检验人 |
| `submitted_by` | `r.submitted_by` | 送检人 |
| `machine_operator` | `r.machine_operator` | 操机员 |
| `handover_by` | `r.handover_by` | 交接员 |
| `day` | `substr(r.inspected_at, 1, 10)` | 检验日期（前 10 字符） |
| `month` | `substr(r.inspected_at, 1, 7)` | 检验月份（前 7 字符） |

* 白名单外的取值 → `400`，`details.supported` 返回完整列表。
* 多个维度可用 `?group_by=sample_project,sample_size` 或 `?group_by=sample_project&group_by=sample_size`（两种写法等价，会去空白）。

### 6.2 筛选参数（`summary` / `defects` / `workload` 共用）

| 查询参数 | 对应条件 | 说明 |
|---|---|---|
| `from` | `COALESCE(r.inspected_at, r.created_at) >= from` | 字符串比较，建议 `YYYY-MM-DD` |
| `to` | `COALESCE(r.inspected_at, r.created_at) <= to + "T23:59:59Z"` | 服务端自动补时分秒 |
| `process` | `r.process_code = ?` | 精确 |
| `sample_project` | `f.sample_project = ?` | 精确 |
| `sample_model` | `f.sample_model = ?` | 精确 |
| `sample_spec` | `f.sample_spec = ?` | 精确 |
| `sample_size` | `f.sample_size = ?` | 精确 |
| `sample_side` | `f.sample_side = ?` | 精确 |
| `inspected_by` | `r.inspected_by = ?` | 精确 |
| `unit` | `COALESCE(f.unit_text, f.unit_id) = ?` | 精确 |
| `include_void` | 仅 `"true"` 时**包含**已作废记录 | 默认排除（同时要求 `r.state='ACTIVE'` 且 `f.state='ACTIVE'`） |

> 只支持精确匹配（`keyword` 类模糊查询不在统计接口里）。

### 6.3 返回结构

```jsonc
// GET /api/stats/summary
{
  "group_by": ["process"],
  "groups": [
    { "key": "一次加硬", "record_count": 12, "total_qty": 1200,
      "qualified_qty": 1152, "defect_qty": 48, "pass_rate": 96 }
  ],
  "total": { "key": "", "record_count": 12, "total_qty": 1200,
             "qualified_qty": 1152, "defect_qty": 48, "pass_rate": 96 },
  "note": "不良子项合计可能大于不良品数（同一片镜片可计入多个类别，见需求 Q5）"
}
```

* 按「笔」聚合（`form_records` JOIN `forms`）；`record_count` = 笔数。
* `pass_rate` = `qualified_qty / total_qty * 100`，四舍五入 2 位；`total_qty=0` 时为 `0`（不是 `null`）。
* 分组结果 `ORDER BY record_count DESC LIMIT 500`；`total` 是全量总计（不受 500 限制）。
* `workload` 的返回项结构与 `groups` 相同，包在 `{"by": "...", "items": [...], "total": n}` 中，`LIMIT 200`。

### 6.4 ⚠️ 口径提示：不良子项合计可能大于不良品数

同一片镜片可被同时计入多个不良类别（如既算"点伤"又算"划伤"），因此：

* `/api/stats/defects` 返回的 15 个子项合计**可能大于** `defect_qty`（不良品数）；
* 保存时这类数据**不会阻断**（V-2 恒为 warning，`strict` 也不阻断）；
* 前端展示时请明确标注"子项合计为不良登记次数，不等于不良品数"。

---

## 7. 附录

### 7.1 字段中文名对照（来自 `inspection/validate.go` 的 `fieldLabels`）

| 字段名 | 中文名 |
|---|---|
| `form_no` | 表单号 |
| `sample_raw` | 样品信息 |
| `sample_project` | 项目 |
| `sample_model` | 型号 |
| `sample_spec` | 规格 |
| `sample_size` | 大小 |
| `sample_side` | 左右 |
| `unit_id` | 单位 |
| `unit_text` | 单位 |
| `process_code` | 工序 |
| `submitted_by` | 送检人 |
| `submitted_at` | 送检时间 |
| `inspected_by` | 检验人 |
| `inspected_at` | 检验时间 |
| `machine_operator` | 操机员 |
| `handover_by` | 交接员 |
| `lines` | 计数明细 |

> 未在此表中的字段（如 `total_qty`、`defect_qty`、`is_complete`）在 `diff` 接口的 `label` 里**原样返回字段名**。

### 7.2 枚举取值

**字典类别（`GET /api/dict/categories` 的 8 个常量）**

| 常量值 | 含义 |
|---|---|
| `process` | 工序 |
| `sample_project` | 项目 |
| `sample_model` | 型号 |
| `sample_spec` | 规格 |
| `sample_size` | 大小 |
| `sample_side` | 左右 |
| `sample_class` | 结构类别（含义未确认，冗余维度） |
| `unit` | 单位 —— 仅类别常量存在；**`cmd/seed` 未写入任何 `unit` 类别字典项**，单位实际用 `units` 表（`GET/POST /api/units`） |

**种子字典值（`cmd/seed/main.go`，共 17 条；`code` = `label`）**

| 类别 | 取值（sort_no） |
|---|---|
| `process` | 一次加硬(10)、二次加硬(20) |
| `sample_project` | M1(10)、M2(20) |
| `sample_model` | VID1(10)、VID1 Rx(11)、VID2(20) |
| `sample_spec` | S1(10)、S2(20) |
| `sample_size` | MD(10)、SM(20)、LG(30) |
| `sample_side` | L(10)、R(20) |
| `sample_class` | MONOC(10)、BINOC(20)、`MONOC_AND_BINOC`（label 为 `MONOC & BINOC`，30） |

**参考部件号种子数据**：`backend/data/reference_drawings.csv`（序号 05～22，共 18 条），例如 `S1 - MD - R - VID2 - MONOC & BINOC`、`S1 - MD - L - VID1 Rx`。**仅供参考**：现场输入与清单不一致不影响保存（只打 `needs_review` 标记）。

**其它枚举**

| 位置 | 取值 |
|---|---|
| 用户 `status` | `active`、`disabled` |
| 表单 / 笔 `state` | `ACTIVE`、`VOID` |
| `enforce` | `warn`（默认）、`strict` |
| `mode` | `merge`（默认）、`replace`（其它值等同 merge） |
| `Issue.level` | `warning`、`error` |
| `unmatched_reason` / `sample_unmatched_reason` | `raw_only`、`partial`、`no_drawing`（空 = 匹配成功） |
| `sample_class` 解析值 | `MONOC`、`BINOC`、`MONOC_AND_BINOC` |
| `SaveResult.action` | `create_form`、`update_form`、`create_record`、`save_record`、`create_form+create_record` |
| `audit_logs.action` | `create_form`、`update_form`、`create_record`、`save_record`、`create_form+create_record`、`void_record` |
| `audit_logs.entity` | `form`、`form_record` |
| `stats/workload` 的 `by` | `inspected_by`（默认）、`recorded_by`、`last_saved_by` |
| 不良类别组 | `spot`(点伤)、`scratch`(划伤)、`dirt`(脏污)、`hard_spot`(加硬点)、`debris`(垃圾点) |

### 7.3 与 `docs/design.md` §6 的差异（前端请注意）

| design.md 写法 | 代码实际 |
|---|---|
| JSON 字段 camelCase（`formNo`、`totalQty`、`expectedSaveCount`） | **全部 snake_case**（`form_no`、`total_qty`、`expected_save_count`） |
| `mode: incremental \| full` | `mode: merge \| replace`；校验强度由**独立参数** `enforce: warn \| strict` 控制 |
| `Issue.code` | `Issue.rule` |
| `clientRequestId` 幂等键 | **已实现**：优先读请求头 `X-Idempotency-Key`，其次请求体 `client_request_id`（snake_case）；命中回放首次响应并带 `X-Idempotent-Replay: true` |
| `POST /forms/:formNo/sample/confirm` | 实际为 `POST /api/sample/confirm`（body 带 `sample_raw`） |
| `GET /forms/:formNo/summary` | **未实现**（可用 `GET /api/forms/:formNo/sample/pending` 与笔上的 `is_complete`/`missing_fields` 替代） |
| `GET /forms/:formNo/export`、`GET /forms/export` | **未实现**（当前无导出接口） |
| `GET /stats/save-activity` | **未实现**（可用 `GET /api/audit-logs` 近似） |
| `GET /dict/items` 返回 `version` | **不返回** `version`/`etag`（无缓存协商字段） |
| `POST /forms` 与 `PATCH /forms/:formNo` 的区别 | 二者都走同一保存逻辑；`POST` 需 body 里的 `form_no` 且返回 **201**，`PATCH` 用路径参数且返回 **200**（表单不存在时同样会创建） |
| 表单列表筛选（工序 / 样品维度 / 良率 / isComplete） | **部分已实现**：`keyword` / `state` / `needs_review` / `from` / `to` / **`sample_project` / `sample_model` / `sample_spec` / `sample_size` / `sample_side` / `unit`** + 分页；**工序、良率区间、isComplete 仍未支持** |
| 唯一约束冲突（`lines` 内 `seq` 重复） | 已映射为 **`409 conflict`**（不再是 500） |
| `?groupBy=day\|process\|sampleSize` | 参数名为 **`group_by`**，取值必须用白名单里的 snake_case（`sample_size`） |
| 路径 `/auth/login` 等 | 全部带 `/api` 前缀 → `/api/auth/login` 等 |

---

*文档依据代码整理：`internal/server/server.go`、`internal/pkg/httpx/*`、`internal/middleware/*`、`internal/auth/*`、`internal/users/store.go`、`internal/dictionary/*`、`internal/sample/*`、`internal/inspection/*`、`internal/stats/*`、`internal/pkg/config/config.go`、`internal/pkg/password/password.go`、`cmd/seed/main.go`、`migrations/sqlite/0001_init.sqlite.up.sql`。*

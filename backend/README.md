# lens-qs-api（后端）

镜片表面质量检验记录系统 —— **后端服务**，位于项目根的 `backend/` 目录下（前端将来放在平级的 `frontend/`）。

把纸质《镜片表面质量检验记录表》搬到线上的后端：录入、字段级合并保存、历史追溯、检索与汇总统计。
定位是"**在线文档的升级版**"：不做审批流、不做流程管控，**一切以最大灵活性为先**（不强制字段、顺序、完整性、工序集合）。

需求、设计与接口文档在**上级 `docs/` 目录**：

| 文档 | 内容 |
|---|---|
| [`../docs/requirements.md`](../docs/requirements.md) | 需求（业务口径 B-x、校验 V-x、待确认问题 Q-x） |
| [`../docs/design.md`](../docs/design.md) | 系统设计（数据模型、保存语义、架构） |
| [`../docs/features.md`](../docs/features.md) | 功能说明（简版）：功能清单与实现状态、设计原则 |
| [`../docs/api.md`](../docs/api.md) | **接口文档**（与前端对齐用，含逐接口请求/响应示例） |

---

## 技术选型

| 项 | 选择 | 说明 |
|---|---|---|
| 语言 | Go 1.24+ | 单二进制部署，无运行时依赖 |
| Web 框架 | **Gin** | 通用度高、易审查 |
| 数据库 | **SQLite（默认）/ PostgreSQL** | 开发与单机部署用 SQLite（纯 Go 驱动，无需 CGO）；生产用 PostgreSQL |
| 数据访问 | `database/sql` + `sqlx` | 薄封装，SQL 显式可读；两套占位符由 `Rebind` 统一 |
| 迁移 | 自带 SQL 迁移执行器 | `migrations/{sqlite,postgres}/`，`go:embed` 进二进制 |
| 密码 | Argon2id（`golang.org/x/crypto`） | PHC 格式自描述，便于将来调参 |
| 会话 | 服务端会话（Cookie + Bearer Token） | 令牌只存 SHA-256 哈希，可服务端吊销 |

**依赖很少**：gin、sqlx、pgx（PostgreSQL 驱动）、modernc sqlite、x/crypto。

---

## 快速开始（SQLite）

```bash
# 1) 准备配置
cp .env.example .env

# 2) 编译
make build            # 产物在 bin/

# 3) 建库 + 初始化数据 + 建账号
./bin/lens-qs-migrate
./bin/lens-qs-seed
./bin/lens-qs-createuser -username admin -password 'Admin@12345' -admin -name 管理员
./bin/lens-qs-createuser -username zhangsai -password 'Test@12345' -name 张赛赛 -emp 1001

# 4) 启动
./bin/lens-qs-api     # 默认 :8080
curl http://127.0.0.1:8080/healthz
```

也可以直接用 `go run`：`make migrate && make seed && make run`。

### Docker（PostgreSQL + API）

```bash
cp .env.example .env
docker compose up -d --build
curl http://127.0.0.1:8080/healthz
```

容器入口会自动等待数据库、执行迁移、写入字典与参考部件号、并按环境变量创建初始管理员。

---

## 配置（环境变量）

| 变量 | 默认 | 说明 |
|---|---|---|
| `APP_ENV` | `dev` | `prod` 时关闭 gin 调试输出 |
| `APP_ADDR` | `:8080` | 监听地址 |
| `DB_DRIVER` | `sqlite` | `sqlite` \| `postgres` |
| `DB_DSN` | `./data/lens.db` | sqlite 文件路径或 postgres 连接串 |
| `AUTO_MIGRATE` | `false` | 启动时自动迁移 |
| `SESSION_TTL` | `12h` | 会话绝对有效期 |
| `SESSION_IDLE_TTL` | `2h` | 空闲超时 |
| `MAX_LOGIN_FAIL` | `5` | 连续失败上限 |
| `LOCK_DURATION` | `10m` | 达到上限后的锁定时长 |
| `COOKIE_NAME` | `sid` | 会话 Cookie 名 |
| `COOKIE_SECURE` | `false` | 前置 HTTPS 时置 `true` |
| `CORS_ORIGINS` | 空 | 允许的来源，逗号分隔（留空则不下发 CORS 头） |
| `BOOTSTRAP_ADMIN_USER/PASS` | 空 | 库为空时自动创建管理员 |

---

## 数据模型（两层）

> **v0.7 重构**：业务方确认真实场景是"**每道工序各填一份表单，只是各工序填的内容不同**"，
> 因此**一次表单提交 = 某工序填的那张表单**；原来的"一张表单 + 多笔工序记录"模型已废弃。

```
forms（表单提交，form_no 唯一）
├─ 纸质表头栏目（paper_*，纸面手写值）
│    单位 paper_unit_id/paper_unit_text、送检人 paper_submitter、送检时间 paper_submitted_at、
│    检验人 paper_inspector、检验时间 paper_inspected_at、
│    操机员 paper_machine_operator、交接员 paper_handover_by
├─ 计数区栏目
│    operation_code 检验工序（表单第一列，检索/统计主维度）
│    total_qty / qualified_qty / defect_qty / pass_rate
├─ 样品信息：sample_raw（事实来源）+ 项目/型号/规格/大小/左右
├─ 系统留痕（≠ 纸面人名）：created_by / submitted_by / submitted_at
│                            / last_saved_by / last_saved_at / save_count
├─ 完成度：is_complete / missing_fields
├─ form_lines（明细行，1..N）
│  └─ 合计 3 列 + 不良缺陷明细 35 列 × 凹面/凸面/双面 = 105 个计数列
└─ form_history（每次保存前的完整快照，只增不可改）
```

> ⚠️ **纸质栏目与系统留痕不混用**（需求 B-11 / B-17）：
> `paper_submitter` / `paper_inspector` 等是**纸面上手写的人名**（时间按录入原文存，不做日期格式假设）；
> `created_by` / `submitted_by` 是**实际登录的系统用户**，仅用于追溯。
> 表单列合计 = 7 个纸质栏目 + 5 个计数栏目 + 105 个缺陷子列 = **117 个可填列**。
> 单位在库中只有 `paper_unit_*` 两列；接口仍接受旧写法 `unit_id`/`unit_text`（写入归一，同时给出时以 `paper_unit_*` 为准），读取时作为只读别名回显。

**表单列目录**是字段的唯一事实来源，位于 `internal/formfields/formfields.go`：

| 分组 | 数量 |
|---|---|
| **纸质表头栏目** | **7 栏目（8 字段）**：单位、送检人、送检时间、检验人、检验时间、操机员、交接员 |
| 计数区栏目 | 5（检验工序 / 检验总数 / 合格品 / 不合格品 / 合格率） |
| 不良缺陷明细 | **35 列**（点伤、划伤、法兰边点伤、…、法兰边凹痕） |
| 展开后的计数子列 | **105**（每列 × 凹面 `concave` / 凸面 `convex` / 双面 `both`） |
| **合计可填列** | **117** |

字段命名：`<缺陷编码>_<表面>`，例如 `prick_concave`、`flange_recess_both`。
`form_lines` 的列清单与插入语句都由该目录生成，因此**新增一个缺陷列只需改目录 + 加一次迁移**。

接口 `GET /api/form-fields` 与 `GET /api/form-templates` 会把列布局下发给前端。

**工序表单模板**见 `internal/operations`：已登记 `GENERAL`（一般检验）、`一次加硬`、`二次加硬`；
**未登记的工序同样合法**（按通用模板处理）。各工序"具体填哪些列"业务方尚未确定，
因此当前**一律开放全部列、不设必填**。

### 保存语义

**字段级合并**，且区分三态：

| 情况 | 语义 |
|---|---|
| 键**不存在** | 不修改（保持原值） |
| 键为 `null` | **清空**该字段（缺陷格子同样是清零该格子） |
| 键有值 | 写入 |
| `defects` 为 `{}` | 清空全部缺陷计数 |
| `lines` 出现 | **整笔替换**明细（含 `[]` = 清空） |
| 未知键 | **400**，并在 `details.hint` 提示 `GET /api/form-fields` |

**表头与明细的优先级**：有明细时按明细行重算表头汇总，但**补丁里显式给出的表头栏目优先**
（贴合纸质表单"表头是一行合计值"的填法）。合格率由 合格品/检验总数 计算（分母 0 → null）。

每次保存前把**保存前的提交 + 明细**写入 `form_history`，并写 `audit_logs`；乐观锁用 `save_count`。

### 校验强度

`enforce` 参数控制，**默认 `warn`**：

| 规则 | 说明 | 默认 | `enforce=strict` |
|---|---|---|---|
| V-1 | 合格 + 不良 = 总数 | 警告 | ⛔ 阻断 |
| V-2 | 缺陷子项合计 = 不合格品 | 警告 | 警告（**恒不阻断**，见需求 Q5） |
| V-3 | 子项边界 | 警告 | ⛔ 阻断 |
| V-4 | 非负整数 | 警告 | ⛔ 阻断 |
| V-6 | 送检时间 ≤ 检验时间 ≤ 现在 | 警告 | 警告 |
| V-7 | `form_no` 唯一 / `(form_id, seq)` 唯一 | ⛔ 阻断 | ⛔ 阻断 |
| V-8 | 样品信息与参考清单不匹配 | 警告 + 待核对标记 | 警告（**不阻断**） |
| V-11 | 未知字段名 | ⛔ 阻断（400） | ⛔ 阻断（400） |
| V-9 | 结构化项（项目 M1/M2）缺失 | 警告 | 警告 |
| V-10 | 表单既有栏目缺失 | 警告 + `missing_fields` | 警告 |

> 一句话：**"数据自相矛盾"可以在 strict 下被拦住；"还没填完"永远不拦。**

---

## 权限模型

只有**两级**，判定集中在 `internal/middleware`：

- **必须登录**才能调用业务接口（`POST /api/auth/login`、`/healthz` 除外）；
- **普通用户**：可读写全部业务数据（表单、笔、明细、字典、统计、历史、导出）；
- **管理员**（`users.is_admin`）：额外可调用用户管理接口。

身份一律取自会话，**不接受请求体里的操作人字段**。表单上的"检验人/送检人"是**纸面文字**，与系统账号无关。

---

## 接口一览

统一前缀 `/api`；响应 `{ code, message, data }`。除登录外都需登录。

### 认证与用户

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/login` | 登录，返回 token（同时下发 Cookie） |
| POST | `/auth/logout` | 登出 |
| GET | `/auth/me` | 当前用户 |
| POST | `/auth/change-password` | 改密（会吊销全部会话） |
| GET/POST | `/users`、PATCH `/users/:id` | 用户管理（**仅管理员**） |

### 字典与参考部件号

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/dict/items?category=` | 按类别取字典（工序/样品各维度/…） |
| POST | `/dict/items`、PATCH `/dict/items/:id` | 维护字典项 |
| GET/POST | `/units` | 单位 |
| GET | `/dict/drawings?spec=&size=&side=&model=` | 参考部件号（**仅供参考**） |
| POST | `/dict/drawings/import` | 批量导入（`items` 或制表符 `rows`） |
| GET | `/dict/defect-columns` | 不良列定义（类别 × 表面），供调用方渲染 |

### 样品信息

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/sample/parse` | 解析原始串 → 结构化字段 + 参考匹配 + 置信度（**纯辅助，不写库**） |
| POST | `/sample/confirm` | 人工确认解析结果（**不改写原始串**） |
| GET | `/sample/cache?sample_raw=` | 查看规范化键与 token 切分 |
| GET | `/sample/pending` | 待核对清单 |

### 工序表单与字段目录

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/form-templates` | **工序表单目录**：每个工序的模板 + 列布局 |
| GET | `/form-templates/:code` | 某工序的模板与完整列布局 |
| GET | `/form-fields` | **字段目录**：5 个顶层栏目 + 35 个缺陷列 × 3 表面（105 个子列） |

### 表单提交（核心）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/forms` | 列表（`keyword`/`state`/`operation_code`/样品各维度/`paper_inspector`/`paper_submitter`/`unit`/`from`/`to`/`incomplete` + 分页） |
| POST | `/forms` | **新建提交**；`form_no` 已存在则按其字段级合并更新（返回 201） |
| POST | `/forms/batch` | **批量提交**多张表单（`forms` 数组，整批原子） |
| GET | `/forms/:id` | 取提交详情（含明细与全部缺陷计数；也可用表单号查询） |
| PATCH | `/forms/:id` | **更新提交**（字段级合并；`lines` 提交则整笔替换） |
| POST | `/forms/:id/void` | 逻辑作废 + 原因 |
| GET | `/forms/:id/history` | 历史快照列表（按 `save_count` 倒序） |
| GET | `/forms/:id/history/diff?from=&to=` | 字段级 + 明细级差异（`to=current`） |
| GET | `/audit-logs?entity=&entity_id=&limit=` | 操作日志 |

### 汇总统计

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/stats/dimensions` | 可用分组维度 |
| GET | `/stats/summary?group_by=operation_code,sample_spec` | 按维度分组汇总 |
| GET | `/stats/defects` | **不良缺陷分布**：35 列 × 3 表面的合计 |
| GET | `/stats/workload?by=submitted_by\|last_saved_by` | 工作量 |
| GET | `/stats/incomplete-forms` | 未完成表单清单 |

分组维度（19 个）：`operation_code` `operation_text` `sample_project` `sample_model` `sample_spec`
`sample_size` `sample_side` `sample_raw` `form_no` `unit`
`paper_inspector` `paper_submitter` `paper_operator` `paper_handover`
`submitted_by` `last_saved_by` `created_by` `day` `month`。

> 前一组是**纸面手写人员**维度，后一组是**系统录入人**维度，两者不同（见上文警告）。

### 保存请求示例

```jsonc
// POST /api/forms   （新建；form_no 已存在则按其字段级合并更新）
{
  "form_no": "QS-260930-001",     // 可选：不给则自动生成 {YYMMDD}-{流水}
  "operation_code": "一次加硬",    // 检验工序（表单第一列）
  "enforce": "warn",              // warn（默认，不阻断）| strict（自相矛盾的数据拦下）
  "expected_save_count": 1,       // 可选：乐观锁，不一致返回 409
  "client_request_id": "uuid",    // 可选：幂等键（也可用请求头 X-Idempotency-Key）
  "reason": "补填脏污",            // 可选：写入历史与操作日志
  "form": {                       // 字段补丁：只提交要改的字段
    "sample_raw": "M2 S2 SM R VID2",
    "sample_project": "M2",
    // 纸质表单原有表头栏目（手写值，与系统留痕分区存放）
    "paper_unit_text": "一厂",
    "paper_submitter": "王五",
    "paper_submitted_at": "2026.9.8",
    "paper_inspector": "张赛赛",
    "paper_inspected_at": "2026.9.9",
    "paper_machine_operator": "赵六",
    "paper_handover_by": "钱七",
    "lines": [                    // 提交即整笔替换明细
      {
        "total_qty": 100, "qualified_qty": 96, "defect_qty": 4,
        "defects": {              // 只改给出的格子；给 {} 则清空
          "prick":            { "concave": 1, "convex": 1 },
          "scratch":          { "both": 1 },
          "flange_recess":    { "concave": 1 },
          "hardening_bubble": { "convex": 1 }
        }
      }
    ]
  }
}
```

响应会回带即时校验结果与完成度：

```jsonc
{
  "code": "ok",
  "data": {
    "action": "create_form",      // create_form | save_form
    "submission": {
      "form_no": "QS-260930-001", "operation_code": "一次加硬", "save_count": 1,
      "total_qty": 100, "qualified_qty": 96, "defect_qty": 4, "pass_rate": 96,
      "is_complete": true, "missing_fields": [],
      "lines": [ { "seq": 1, "total_qty": 100, "qualified_qty": 96, "defect_qty": 4,
                   "defects": { "prick": { "concave": 1, "convex": 1 } } } ]
    },
    "issues":   [ { "rule": "V-10", "level": "warning", "field": "unit_id",
                    "message": "「unit_id」尚未填写" } ],
    "warnings": [ /* 同上 */ ],
    "errors":   [],
    "missing_fields": [],
    "is_complete": true
  }
}
```

---

## 目录结构

```
lens-qs/                      项目根（唯一 git 仓库）
├─ docs/                      需求 / 设计 / 功能说明 / 接口文档
├─ backend/                   ← 本工程
│  ├─ cmd/
│  │  ├─ api/          启动 HTTP 服务
│  │  ├─ migrate/      执行/查看迁移
│  │  ├─ seed/         初始化字典 + 参考部件号
│  │  └─ createuser/   创建/更新用户
│  ├─ internal/
│  │  ├─ auth/         登录、会话、改密（+ http/ 子包）
│  │  ├─ users/        用户（两级）
│  │  ├─ dictionary/   字典、单位、参考部件号（+ http/）
│  │  ├─ sample/       样品信息解析、缓存、参考匹配（+ http/）
│  │  ├─ inspection/   **核心**：表单/笔/行、合并保存、历史、校验、差异（+ http/）
│  │  ├─ stats/        汇总统计（+ http/）
│  │  ├─ middleware/   认证、管理员校验、CORS、请求 ID
│  │  ├─ server/       路由装配
│  │  ├─ store/        迁移执行器、时间格式、测试库
│  │  └─ pkg/          config / database / httpx / id / password
│  ├─ migrations/      {sqlite,postgres}/*.sql（go:embed）
│  ├─ data/            参考部件号清单（seed 用）
│  ├─ scripts/         端到端验证脚本（PowerShell）
│  ├─ Dockerfile / docker-compose.yml / Makefile
│  └─ .env.example
└─ frontend/                  （将来）前端工程，与 backend/ 平级
```

---

## 测试

```bash
make test          # 全部测试
make check         # 格式化 + vet + 测试
```

覆盖内容：

- **样品解析**（`internal/sample`）：参考清单写法、照片手写串 `M2 S2 ND2 SMR`、Rx 型号、全角字符、字典补充取值；并断言**解析不改写原始串**。
- **合并与校验**（`internal/inspection`）：字段级合并（未提交字段不被清空）、显式 `null` 清空、未知字段报告、默认只警告 / `strict` 阻断、V-2 恒不阻断、良率与完成度计算、差异对比。
- **保存闭环**（`internal/inspection`）：分次录入 → 补填 → 历史快照 → 操作日志 → 第二笔 → 明细整笔替换 → 乐观锁 409 → 作废保留数据 → 表单级合并保留原始串。
- **汇总统计**（`internal/stats`）：按规格/项目/工序分组的正确性、总计、非法维度报错。
- **迁移**（`internal/store`）：内嵌迁移可读、迁移可重复执行。

端到端（需要先启动服务）：

```powershell
pwsh -File scripts/e2e.ps1    # 解析 / 表单 / 分次录入 / 批量子提交 / strict 与 warn
pwsh -File scripts/e2e2.ps1   # 汇总 / 不良分布 / 未完成 / 工作量 / 作废 / 日志 / 权限 / 字典
```

---

## 设计取舍（来自需求确认）

| 决定 | 理由 |
|---|---|
| 除 `form_no` 与 `(form_id, seq)` 唯一外**不做任何强制** | 需求 F-1~F-4：生产流程繁杂且无法确认，系统不得用流程约束绑住现场 |
| 校验**默认只警告**，`strict` 才阻断 | 需求 F-7；否则"分次填写"根本无法落地 |
| 工序字段**在笔与行上都保留** | 需求 F-6：多工序落库形态无法确认，两种现实都要能表达 |
| 原始串**永不被解析或人工修正改写** | 需求 B-6：以表单上的原始输入为准，参考清单只是参考 |
| 汇总维度做成**通用 `group_by`** | 需求 §4.7：将来加维度不改接口 |
| 权限只有两级，判定集中一处 | 需求 B-14：生产部门人员安排未定，将来可收紧而不改业务代码 |

## 已知待办

- **汇总统计**属需求 §4.7，本期已实现基础分组能力；按笔去重口径、汇总报表导出等待确认（需求 Q21）。
- **不良重复计数**（同一片镜片计入多个类别）已确认存在，登记为待定；统计接口已标注该口径。
- **"每道工序各填一行"与"分次补齐同一组栏目"**两种形态目前都支持，确认后可收敛（需求 Q19）。

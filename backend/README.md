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

## 数据模型（三层）

```
Form（一张纸质表单，form_no 唯一）
 ├─ 样品信息：sample_raw（原始串，事实来源）+ 结构化字段（项目/型号/规格/大小/左右 + 冗余）
 └─ FormRecord（一笔 = 一次工序填写，(form_id, sequence_no) 唯一）
      ├─ 工序 + 表单既有栏目（送检人/送检时间/检验人/检验时间/操机员/交接员）
      ├─ 系统留痕（recorded_by / last_saved_by / save_count）
      ├─ 汇总（total_qty / qualified_qty / defect_qty / pass_rate，服务端重算）
      ├─ RecordLine（一行计数：检验总数、合格品、不良品、五类不良 × 三表面）
      └─ RecordHistory（每次保存前的完整快照，追加写、不可改不可删）
```

**为什么不是"一表单号一条记录"**：纸质表单要在多个工序中使用，各工序的检验发生在不同时间、可能由不同人填写。拆成"笔"后，每笔只被本次填写的内容更新，历史信息不会被后续填写覆盖掉。

### 保存语义

**字段级合并（PATCH 语义）**：请求里出现的字段才更新，没出现的保持原值。
这样"不同工序/不同人只填自己那部分"才能成立。

| 内容 | 更新方式 |
|---|---|
| 表单级字段（样品信息、单位） | 字段级合并 |
| 笔级字段（工序、人员、时间、备注） | 字段级合并 |
| 明细 `lines` | 提交则**整笔替换**（不做行级合并，避免"半行数据"） |
| 每次保存 | 保存前内容写入 `record_history`，并写 `audit_logs` |
| 汇总与完成度 | **服务端重算**，忽略调用方上报值 |

### 校验强度

`enforce` 参数控制，**默认 `warn`**：

| 规则 | 说明 | 默认 | `enforce=strict` |
|---|---|---|---|
| V-1 | 合格 + 不良 = 总数 | 警告 | ⛔ 阻断 |
| V-2 | 五类不良子项合计 = 不良品 | 警告 | 警告（**恒不阻断**，见需求 Q5） |
| V-3 | 子项边界 | 警告 | ⛔ 阻断 |
| V-4 | 非负整数 | 警告 | ⛔ 阻断 |
| V-6 | 送检时间 ≤ 检验时间 ≤ 现在 | 警告 | 警告 |
| V-7 | `form_no` / `(form_id, sequence_no)` 唯一 | ⛔ 阻断 | ⛔ 阻断 |
| V-8 | 样品信息与参考清单不匹配 | 警告 + 待核对标记 | 警告（**不阻断**） |
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

### 表单与工序记录（核心）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/forms` | 表单列表（关键字/状态/待核对/日期/样品维度/单位） |
| POST | `/forms` | 新建表单 |
| GET | `/forms/:formNo` | **取表单及其全部笔与明细**（"接着填"用） |
| PATCH | `/forms/:formNo` | 更新表单级字段（字段级合并） |
| POST | `/forms/:formNo/records` | **新建一笔**；带 `records` 数组时批量提交多笔 |
| PATCH | `/records/:id` | **保存/更新一笔**（增量填写主接口） |
| GET | `/records/:id` | 取一笔（含明细） |
| POST | `/records/:id/void` | 逻辑作废 + 原因 |
| GET | `/records/:id/history` | 历史快照列表 |
| GET | `/records/:id/history/diff?from=&to=` | 字段级 + 明细级差异（`to=current`） |
| GET | `/audit-logs?entity=&entity_id=&limit=` | 操作日志 |

### 汇总统计

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/stats/dimensions` | 可用分组维度 |
| GET | `/stats/summary?group_by=sample_spec,sample_size` | **按样品信息各子项分组汇总** |
| GET | `/stats/defects` | 不良类别 × 表面分布 |
| GET | `/stats/workload?by=inspected_by\|recorded_by\|last_saved_by` | 工作量 |
| GET | `/stats/incomplete-forms` | 未完成表单清单 |

分组维度：`sample_project` `sample_model` `sample_spec` `sample_size` `sample_side` `sample_class` `sample_raw` `form_no` `process` `unit` `inspected_by` `submitted_by` `machine_operator` `handover_by` `day` `month`。

### 保存请求示例

```jsonc
// POST /api/forms/M-260908-01/records
{
  "enforce": "warn",              // warn（默认，不阻断）| strict（自相矛盾的数据拦下）
  "mode": "merge",                // merge（默认，字段级合并）| replace（整体替换）
  "expected_save_count": 1,       // 可选：乐观锁，不一致返回 409
  "reason": "补填二次加硬",         // 可选：写入历史与操作日志
  "form":   { "sample_raw": "M2 S2 ND2 SMR", "sample_project": "M2" },  // 只提交要改的
  "record": { "process_code": "一次加硬", "inspected_by": "张赛赛" },
  "lines":  [ { "total_qty": 100, "qualified_qty": 100 } ]              // 提交即整笔替换
}
```

响应会回带即时校验结果与完成度：

```jsonc
{
  "code": "ok",
  "data": {
    "action": "create_record",
    "record": { "sequence_no": 1, "save_count": 1, "is_complete": false,
                "total_qty": 100, "qualified_qty": 100, "pass_rate": 100 },
    "issues":   [ { "rule": "V-10", "level": "warning", "field": "submitted_by",
                    "message": "表单既有栏目「送检人」尚未填写" } ],
    "warnings": [ /* 同上 */ ],
    "errors":   [],
    "missing_fields": ["submitted_by", "submitted_at", "machine_operator", "handover_by"],
    "is_complete": false
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
| 除 `form_no` 与 `(form_id, sequence_no)` 唯一外**不做任何强制** | 需求 F-1~F-4：生产流程繁杂且无法确认，系统不得用流程约束绑住现场 |
| 校验**默认只警告**，`strict` 才阻断 | 需求 F-7；否则"分次填写"根本无法落地 |
| 工序字段**在笔与行上都保留** | 需求 F-6：多工序落库形态无法确认，两种现实都要能表达 |
| 原始串**永不被解析或人工修正改写** | 需求 B-6：以表单上的原始输入为准，参考清单只是参考 |
| 汇总维度做成**通用 `group_by`** | 需求 §4.7：将来加维度不改接口 |
| 权限只有两级，判定集中一处 | 需求 B-14：生产部门人员安排未定，将来可收紧而不改业务代码 |

## 已知待办

- **汇总统计**属需求 §4.7，本期已实现基础分组能力；按笔去重口径、汇总报表导出等待确认（需求 Q21）。
- **不良重复计数**（同一片镜片计入多个类别）已确认存在，登记为待定；统计接口已标注该口径。
- **"每道工序各填一行"与"分次补齐同一组栏目"**两种形态目前都支持，确认后可收敛（需求 Q19）。

# 镜片表面质量检验记录系统 — 初步系统设计

| 项目 | 内容 |
|---|---|
| 文档版本 | v0.3（已并入第三轮确认：表单既有项全纳入、多工序分次填写、权限放开、后端为交付范围） |
| 状态 | 草稿 |
| 上游文档 | `docs/requirements.md`（需求文档 v0.3） |
| 交付范围 | **仅后端**（需求 B-15）：数据模型、业务逻辑、REST 接口、用户与登录、字典、统计与导出、操作日志 |

> 本文档的目标：把需求文档中的「需要」落成可施工的**后端设计**——模块划分、数据模型、保存语义、接口、关键机制（双轨存储 / 解析 / 字段级合并 / 历史快照 / 认证）。
> 标注 `【待确认】` 与需求文档一致；标注 `【设计决定】` 表示设计侧已定，评审可直接否定。

> **v0.3 设计层面主要变更**
> 1. **数据模型重构**：由「一份表单 = 一条记录」改为「**一份表单（Form）+ 多笔工序记录（Record）+ 每笔多行明细（Line）**」三层结构（§4.2.3）。
> 2. **保存语义**：由"整体覆盖"改为"**字段级合并更新**"（`PATCH` 语义），并以 `enforce: warn | strict` 控制校验强度（§5.6）。
> 3. **表单既有栏目全部恢复**：单位 / 送检人 / 送检时间 / 检验人 / 检验时间 / 操机员 / 交接员；并区分"表单上写的"与"实际录入系统的人"（`recorded_by`）。
> 4. **删除 RBAC**：去掉 `Role` / `Permission` / `RolePermission` / `UserRole` 表，改为 `User.isAdmin` 布尔标记 + 可替换的授权判定层（§5.5）。
> 5. **新增完成度**：`isComplete` / `missingFields` 与"未完成表单清单"（§5.7）。
> 6. 前端页面结构降级为"接口使用场景说明"（§5.9）。

---

## 1. 设计目标与约束

### 1.1 设计目标
1. **贴近纸质表单**：字段与纸质表单一一对应，**既有栏目一个不落**（需求 B-11），不改变判定口径。
2. **双轨可追溯**：样品信息原始串永不失真（事实来源），结构化字段可查可统计。
3. **保存即生效**：无审批流；**同一笔记录可被多次增量保存**（字段级合并），历史可回看。
4. **支持两种录入节奏**：工序现场分次填（增量模式）与次日集中录入（完整模式）**共用一套模型与接口**。
5. **可演进**：新增工序、样品维度、单位、甚至新不良类别，走字典配置而非发版。
6. **安全可追溯**：认证 + 每次保存留痕 + 记录不可物理删除；**权限暂放开但结构可替换**。
7. **便于他人对接**：稳定清晰的 REST 契约（可导出 OpenAPI），校验规则集中一处。

### 1.2 设计约束
| 约束 | 说明 |
|---|---|
| 交付范围 | **仅后端**（需求 B-15）；不设计界面，但接口需携带足够的展示元信息 |
| 使用环节未知 | 系统最终用于工序现场还是集中录入**尚未确定**（需求 Q20）→ 不能用任一环节的假设简化校验强度，两种模式都要能跑 |
| 人员 | 使用者是产线检验人员与录入人员，非 IT 用户 → 接口错误信息必须可读、可指导操作 |
| 信息完备度 | 业务方对部件号附加成分含义**无法确认**（需求 B-8）→ 冗余字段可空且不参与校验 |
| 数据 | 质量记录，禁止物理删除，仅逻辑作废；**每次保存必须留痕** |
| 规模 | 数十人并发；年记录量 10^4~10^5 量级（低规模，单库足够，无需分布式） |
| 环境 | 目标为内网服务器；【待确认 Q10】是否需离线部署 |

### 1.3 关键技术决定（摘要）

> **v0.4 更新**：后端已按本节实现，代码在 `backend/`（Go + Gin），与文档同属一个项目根。
> 与原计划（NestJS + Prisma）的差异见 §3 与 §4.4 的说明。

| 编号 | 决定 | 理由 |
|---|---|---|
| D-1 | 后端 **Go 1.24 + Gin**，单体模块化 | 单二进制部署、无运行时依赖；框架通用度高、便于快速审查（业务方明确倾向 gin） |
| D-2 | 数据访问用 `database/sql` + **`sqlx`**，SQL 显式书写 | 薄封装、SQL 可读可审；两套占位符由 `Rebind` 统一 |
| D-3 | 数据库 **SQLite（开发/单机）或 PostgreSQL（生产）**，自带 SQL 迁移执行器 | 开发期零依赖起步；生产可平滑切换。表结构见 `backend/migrations/{sqlite,postgres}/` |
| D-4 | 认证用**服务端会话**：HttpOnly Cookie + Bearer Token 两种取令牌方式 | 浏览器与第三方调用方都能用；令牌只存 SHA-256 哈希，可服务端吊销 |
| D-5 | 密码用 **Argon2id**（`golang.org/x/crypto`），PHC 格式自描述 | 与需求 U-1 一致；将来调参不影响旧密码校验 |
| D-6 | 权限只有**两级**（普通用户 / 管理员），判定集中在 `middleware` + 可替换的判定层 | 需求 B-14；将来收紧只改一处 |
| D-7 | 计数字段用**固定列**（15 个不良子项 + 3 个合计列），类别字典化仅用于展示与统计映射 | 表结构稳定、校验与统计最简单 |
| D-8 | 样品信息结构化字段**冗余存储在表单主表**上 | 查询性能与实现复杂度最优；字段少且固定 |
| D-9 | 样品信息解析走**字典驱动的 token 集合匹配 + 解析缓存**；**原始串为事实来源** | 不依赖字符串位置；可迭代；不阻断保存（需求 B-6/B-8） |
| D-10 | **三层模型**：`Form` → `FormRecord`（笔）→ `RecordLine`（行）+ `RecordHistory` | 满足"多工序分次填写"且保留每笔独立的人员/时间/历史（需求 §5.1） |
| D-11 | **保存 = 字段级合并（PATCH 语义）**，`enforce` 控制校验强度；明细提交即整笔替换 | 兼容"不同人只填自己那部分"，同时让集中录入走同一接口（需求 §5.2） |
| D-12 | 每次保存前把**该笔保存前内容**写入 `RecordHistory`（只增不可改） | 质量记录可追溯；粒度到"笔"比"整表"更贴合现场 |
| D-13 | 汇总与良率**服务端重算**，忽略客户端上报值 | 数据一致性 |
| D-14 | 汇总统计做成**通用 `group_by`**（维度白名单 + 表达式映射） | 需求 §4.7：将来加维度不改接口 |

---

## 2. 总体架构

```
┌──────────────────────────────────────────────────────────────────────┐
│  调用方（不在本期交付范围，但需被支持）                                  │
│  ┌────────────────────┐  ┌────────────────────┐  ┌────────────────┐  │
│  │ 工序现场录入端      │  │ 次日集中录入端      │  │ 查询/统计/导出  │  │
│  │ （移动优先）        │  │ （桌面/Excel 式）   │  │ （管理端）      │  │
│  └─────────┬──────────┘  └─────────┬──────────┘  └───────┬────────┘  │
│            └───────────────┬───────┴────────────────────┘           │
│              HTTP/JSON (REST) + Cookie 会话 或 Bearer Token           │
└────────────────────────────┼─────────────────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────────────────┐
│  Nginx / Caddy（TLS 终止、反向代理、gzip、限流）                        │
└────────────────────────────┼─────────────────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────────────────┐
│  API 服务（Go + Gin，单体模块化）—— 实现见 backend/                      │
│  internal/auth        登录 / 会话 / 密码策略 / 锁定                    │
│  internal/users       用户、启停、重置密码、管理员标记                  │
│  internal/dictionary  字典（工序/单位/样品段/参考部件号/不良列定义）      │
│  internal/sample      样品信息解析器 + 解析缓存 + 参考部件号匹配          │
│  internal/inspection  **核心**：表单 / 笔 / 明细、字段级合并保存、        │
│                       历史快照与差异、校验 V-1..V-10、完成度             │
│  internal/stats       汇总统计（通用 group_by）、不良分布、工作量         │
│  internal/middleware  认证、管理员校验、CORS、请求 ID                   │
│  internal/store       SQL 迁移执行器、统一时间格式                      │
│  internal/pkg         config / database / httpx / id / password        │
└────────────────────────────┬─────────────────────────────────────────┘
                             │ database/sql + sqlx
┌────────────────────────────▼─────────────────────────────────────────┐
│  SQLite（开发 / 单机）或 PostgreSQL 16（生产）                          │
│  表：users / sessions / audit_logs                                    │
│      forms（form_no 唯一）                                            │
│      form_records（(form_id, sequence_no) 唯一）                       │
│      record_lines / record_history（保存快照，追加写）                  │
│      sample_parse_cache / sample_drawings / dict_items / units        │
│      schema_migrations（迁移版本）                                     │
└──────────────────────────────────────────────────────────────────────┘
```

### 2.1 部署形态
| 形态 | 适用 | 说明 |
|---|---|---|
| A. 单机内网（推荐起步） | 小规模试运行 | 一台服务器跑 Docker Compose：`db` + `api`；HTTPS 用自签或内网 CA |
| B. 开发/演示 | 本地 | SQLite + 单进程 API：`./bin/lens-qs-api` 或 `make run` |
| C. 后续扩展 | 增长后 | API 无状态可多实例；DB 主从；对象存储接图片 |

---

## 3. 代码仓库结构

**单一仓库**：`lens-qs` 是项目根，前后端都放在里面，**根目录只保留一个 git 仓库**（前后端不各自建库，避免嵌套）。

```
lens-qs/                        ← 项目根（唯一 git 仓库）
├─ docs/
│  ├─ requirements.md           需求文档
│  ├─ design.md                 本文档
│  ├─ features.md               功能说明（简版，后续开发目标）
│  └─ api.md                    接口文档（与前端对齐）
│
├─ backend/                     后端工程（Go + Gin）—— 本设计描述的对象
│  ├─ cmd/
│  │  ├─ api/                   启动 HTTP 服务（含优雅退出）
│  │  ├─ migrate/               执行 / 查看迁移
│  │  ├─ seed/                  初始化字典 + 参考部件号
│  │  └─ createuser/            创建 / 更新用户（含管理员）
│  ├─ internal/
│  │  ├─ auth/                  登录、会话、改密（+ http/ 子包）
│  │  ├─ users/                 用户（两级）
│  │  ├─ dictionary/            字典、单位、参考部件号（+ http/）
│  │  ├─ sample/                样品信息解析、缓存、参考匹配（+ http/）
│  │  ├─ inspection/            **核心**：表单/笔/行、合并保存、历史、校验、差异（+ http/）
│  │  ├─ stats/                 汇总统计（+ http/）
│  │  ├─ middleware/            认证、管理员校验、CORS、请求 ID
│  │  ├─ server/                路由装配
│  │  ├─ store/                 迁移执行器、统一时间格式、测试库
│  │  └─ pkg/                   config / database / httpx / id / password
│  ├─ migrations/               sqlite/ 与 postgres/ 两套 .sql（go:embed 进二进制）
│  ├─ data/                     参考部件号清单 CSV（seed 使用）
│  ├─ scripts/                  端到端验证脚本（PowerShell）
│  ├─ Dockerfile / docker-compose.yml / Makefile
│  ├─ .env.example
│  └─ README.md
│
└─ frontend/                    （将来）前端工程，与 backend/ 平级
```

> **分层理由**：前后端都要放进同一个项目根，方便统一管理文档、脚本与后续的前端工程；同时根目录只有一个 git 仓库，不在项目内嵌套第二个仓库。
> **校验规则、样品解析器、字段级合并逻辑**都在 `backend/internal/inspection` 与 `backend/internal/sample` 中**只实现一份**（Go 侧），接口返回的校验结果即这一份的实现结果。
> 前端不在本期范围（需求 B-15）。

---

## 4. 核心数据模型

### 4.1 ER 概览

```
users ──< sessions
  │
  ├──< audit_logs

forms（form_no 唯一；样品信息 + 单位 + 创建信息）
  ├──< form_records（(form_id, sequence_no) 唯一；每笔 = 一次工序填写）
  │        ├──< record_lines（该笔的计数明细行）
  │        └──< record_history（该笔每次保存前的快照，追加写）
  └──> units

sample_parse_cache     sample_drawings（参考部件号，仅最佳努力匹配） ──> dict_items（各维度）
idempotency_requests（写接口幂等：按 user_id + 幂等键回放首次响应，TTL 默认 24h）
dict_items（工序 / 单位 / 样品段 / 不良类别文案）
```

三层结构的含义：

| 层 | 表 | 对应纸质概念 | 数量关系 |
|---|---|---|---|
| 表单 | `forms` | 一张纸质表单 | 1 |
| 笔 | `form_records` | 该表单上的**一次工序检验填写**（同一张表可能被多个工序填过） | 0..N |
| 行 | `record_lines` | 一笔里的**一行计数**（检验总数/合格品/不良品/15 个不良子项） | 1..N（默认 1） |

> 与 v0.2 的差异：删除 `roles` / `permissions` / `role_permissions` / `user_roles`（需求 B-14 权限放开）；
> `inspection_docs` 拆分为 `forms` + `form_records`；`doc_history` 改为按笔的 `record_history`。

### 4.2 表定义

> **实现说明**：以下用 Prisma 风格伪 DDL 表达**便于阅读的表结构**（字段、类型、约束、索引）。
> 实际建表脚本是手写 SQL，位于 `backend/migrations/{sqlite,postgres}/*.sql`（两套方言），
> 由 `backend/internal/store/migrate.go` 的迁移执行器按版本号顺序应用。
> Go 结构体定义在 `backend/internal/inspection/models.go`、`internal/users/store.go` 等文件中，`db` 标签与列名一一对应。
>
> **时间字段的存储约定**：所有时间列都用 `TEXT` 存 **UTC RFC3339**（秒精度）。这样 SQLite 与 PostgreSQL 行为一致，
> 且可直接按字符串比较/排序。需要按时间范围过滤时用字符串区间即可（见 `inspection/list.go`、`stats/service.go`）。

#### 4.2.1 用户与权限

```prisma
model User {
  id             String   @id @default(uuid())
  username       String   @unique          // 登录名
  displayName    String                    // 姓名（记录与留痕显示）
  employeeNo     String?  @unique          // 工号
  unitId         String?                   // 所属单位/部门（对应表单上的「单位」，可空）
  passwordHash   String                    // Argon2id
  isAdmin        Boolean  @default(false)  // 仅用于保护用户管理接口；不代表业务权限（需求 B-14 / Q17）
  mustChangePwd  Boolean  @default(true)   // 首次登录强制改密
  status         String   @default("active") // active | disabled
  failedAttempts Int      @default(0)
  lockedUntil    DateTime?
  lastLoginAt    DateTime?
  createdAt      DateTime @default(now())
  updatedAt      DateTime @updatedAt
  sessions       Session[]
  @@index([unitId, status])
}

// ── 说明：v0.3 删除了 Role / Permission / RolePermission / UserRole 四张表 ──
// 需求 B-14：暂不做角色与权限划分，登录后默认开放全部功能。
// 权限判定集中在 AuthorizationService（当前恒返回 true），将来补权限只需改该服务。

model Session {
  id           String   @id @default(uuid())
  userId       String
  tokenHash    String   @unique      // 只存哈希，防库泄露
  ip           String?
  userAgent    String?
  createdAt    DateTime @default(now())
  lastSeenAt   DateTime @default(now())
  expiresAt    DateTime
  revokedAt    DateTime?
  @@index([userId, revokedAt])
}
```

#### 4.2.2 主数据 / 字典

```prisma
model Unit {                       // 单位/部门
  id      String @id @default(uuid())
  code    String @unique
  name    String
  active  Boolean @default(true)
}

model DictItem {                   // 通用字典：工序、样品维度的取值、其他枚举
  id       String @id @default(uuid())
  category String                 // process | sample_project | sample_model | sample_spec
                                  // | sample_size | sample_side | sample_class | unit_name | ...
  code     String                 // 一次加硬 / 二次加硬 / M1 / M2 / S1 / S2 / MD / SM / LG / L / R
  label    String                 // 显示名（可中英并存）
  sortNo   Int    @default(0)
  active   Boolean @default(true)
  meta     Json?                  // 扩展：别名 aliases、说明、判定标准文案
  @@unique([category, code])
  @@index([category, active, sortNo])
}

model SampleDrawing {              // 部件号索引表（源自图纸清单 05~22）
  id           String @id @default(uuid())
  seqNo        String?  @unique   // 05, 06 ... 22
  drawingNo    String   @unique   // "S1 - MD - R - VID2 - MONOC & BINOC"
  projectCode  String?            // M1/M2 —— 清单中未出现，通常为空【待确认 Q1】
  modelCode    String?            // VID1 RX / VID2
  specCode     String?            // S1 / S2
  sizeCode     String?            // MD / SM / LG
  sideCode     String?            // L / R
  classCode    String?            // MONOC | BINOC | MONOC_AND_BINOC | null
  isRx         Boolean  @default(false)
  active       Boolean  @default(true)
  note         String?
  @@index([specCode, sizeCode, sideCode, modelCode])
}

model SampleParseCache {           // "原始串 → 结构化结果"缓存，加速录入并保证一致性
  id          String   @id @default(uuid())
  rawKey      String   @unique    // 规范化后的原始串（大小写/空白/全角半角归一）
  rawSample   String              // 原文
  parsed      Json                // {project, model, spec, size, side, class, isRx}
  drawingId   String?
  confidence  Float    @default(0)
  hitCount    Int      @default(0)      // 命中次数（越常用越优先推荐）
  verifiedBy  String?                   // 人工确认者
  verifiedAt  DateTime?
  createdAt   DateTime @default(now())
  updatedAt   DateTime @updatedAt
}
```

#### 4.2.3 表单 / 笔 / 行（核心）

**三层结构**：`Form`（表单，对应一张纸质表单）→ `FormRecord`（笔，对应该表单的一次工序检验填写）→ `RecordLine`（行，对应一笔里的计数行）。

```prisma
model Form {                                       // 一张纸质表单
  id            String   @id @default(uuid())
  formNo        String   @unique                 // M-260908-01 —— 表单唯一键
  unitId        String?                          // 单位（表单既有栏目；也存原文）
  unitText      String?                          // 单位原文（字典外的自由文本）

  // ── 样品信息：原始轨（事实来源，任何解析/修正都不改写）──
  sampleRaw     String                            // 逐字保留，如 "M2 S2 ND2 SMR"

  // ── 样品信息：结构化轨（5 核心 + 冗余 + 匹配）────────────────
  sampleProject String?                           // M1 / M2 —— 必须可录入（B-7）
  sampleModel   String?                           // VID1 Rx / VID2
  sampleSpec    String?                           // S1 / S2
  sampleSize    String?                           // MD / SM / LG
  sampleSide    String?                           // L / R
  sampleClass   String?                           // 【冗余】MONOC / BINOC / MONOC_AND_BINOC（含义未确认）
  sampleIsRx    Boolean?                          // 【冗余】型号含 Rx/RX
  sampleDrawingId String?                         // 【冗余】匹配到的参考部件号（仅供参考）
  sampleUnmatchedReason String?                   // raw_only | no_drawing | partial
  sampleParsedBy  String   @default("auto")       // auto | manual | cache
  sampleConfidence Float   @default(0)
  sampleVerified  Boolean  @default(false)        // 人工确认过
  needsReview     Boolean  @default(false)        // 解析失败/不匹配 → 待核对清单（不阻断保存）
  unknownTokens   Json?                           // 未能识别的 token（便于字典补录）

  state         String   @default("ACTIVE")       // ACTIVE | VOID
  voidedAt      DateTime?
  voidReason    String?

  createdBy     String                            // 表单创建者（永不改变）
  createdAt     DateTime @default(now())
  lastSavedBy   String?
  lastSavedAt   DateTime?

  records       FormRecord[]

  @@index([state, createdAt])
  @@index([sampleSpec, sampleSize, sampleSide, sampleModel])   // 层级检索
  @@index([sampleProject])
  @@index([needsReview, state])
  @@index([unitId])
}

model FormRecord {                                 // 一笔 = 该表单的一次工序检验填写
  id            String   @id @default(uuid())
  formId        String
  sequenceNo    Int                                 // 第几笔，从 1 开始；(formId, sequenceNo) 唯一

  // ── 工序（纸质表单未印栏名，必须显式）────────────────
  processCode   String?                             // 如 "一次加硬"（字典 process）
  processText   String?                             // 工序原文/补充

  // ── 表单既有栏目（B-11：全部必须纳入）────────────────
  submittedBy     String?                           // 送检人（表单上写的）
  submittedAt     DateTime?                         // 送检时间
  inspectedBy     String?                           // 检验人（表单上写的）
  inspectedAt     DateTime?                         // 检验时间
  machineOperator String?                           // 操机员
  handoverBy      String?                           // 交接员

  // ── 系统留痕：谁把它录进系统（区别于表单上的检验人，见 Q18）──
  recordedBy    String                              // 首次录入本笔的用户
  recordedAt    DateTime @default(now())            // 首次录入时间

  // ── 保存与完整性 ─────────────────────────────────
  saveCount     Int      @default(1)                // 本笔被保存的次数
  lastSavedBy   String?                             // 最近一次保存的人
  lastSavedAt   DateTime?                           // 最近一次保存的时间
  isComplete    Boolean  @default(false)            // 依据缺失项计算
  missingFields Json?                               // 缺失字段清单（V-10 等）
  remark        String?

  // ── 汇总（由本笔明细行在服务端重算落库）──────────────
  totalQty      Int      @default(0)
  qualifiedQty  Int      @default(0)
  defectQty     Int      @default(0)
  passRate      Decimal? @db.Decimal(6, 3)          // 0~100

  state         String   @default("ACTIVE")         // ACTIVE | VOID
  voidedAt      DateTime?
  voidReason    String?

  form          Form     @relation(fields: [formId], references: [id], onDelete: Cascade)
  lines         RecordLine[]
  history       RecordHistory[]

  @@unique([formId, sequenceNo])
  @@index([formId])
  @@index([processCode, inspectedAt])
  @@index([inspectedBy, inspectedAt])
  @@index([state, isComplete])
  @@index([lastSavedAt])
}

model RecordLine {                                 // 一笔里的一行计数（默认 1 行）
  id            String @id @default(uuid())
  recordId      String
  seq           Int                                // 行序，从 1 开始

  totalQty     Int @default(0)                     // 检验总数
  qualifiedQty Int @default(0)                     // 合格品
  defectQty    Int @default(0)                     // 不良品

  // 点伤
  spotConcave Int @default(0)  spotConvex Int @default(0)  spotBoth Int @default(0)
  // 划伤
  scratchConcave Int @default(0) scratchConvex Int @default(0) scratchBoth Int @default(0)
  // 脏污
  dirtConcave Int @default(0)  dirtConvex Int @default(0)  dirtBoth Int @default(0)
  // 加硬点
  hardSpotConcave Int @default(0) hardSpotConvex Int @default(0) hardSpotBoth Int @default(0)
  // 垃圾点
  debrisConcave Int @default(0) debrisConvex Int @default(0) debrisBoth Int @default(0)

  // 行级工序（【待确认 Q19】若现实是"同一张表上每工序填一行"，工序落在行上）
  processCode   String?
  processText   String?
  remark        String?
  createdAt     DateTime @default(now())
  updatedAt     DateTime @updatedAt

  record        FormRecord @relation(fields: [recordId], references: [id], onDelete: Cascade)
  defects       LineDefect[]
  @@unique([recordId, seq])
  @@index([recordId])
}

model RecordHistory {                              // 保存快照：每次保存前的内容，追加写、不可改不可删
  id            String   @id @default(uuid())
  recordId      String
  formId        String                             // 冗余，便于按表单查询历史
  saveCount     Int                                // 该快照对应的保存次数（即被取代的那一版）
  formSnapshot  Json                               // 保存前的表单级字段（样品信息、单位等）
  recordSnapshot Json                              // 保存前的笔级字段（工序、人员、时间、汇总、完整性）
  linesSnapshot Json                               // 保存前的明细行数组
  savedBy       String                             // 这一版是谁保存的
  savedAt       DateTime                           // 这一版的保存时间
  changedBy     String                             // 谁做了本次修改
  changedAt     DateTime @default(now())
  reason        String?                            // 变更原因（可选）
  @@index([recordId, saveCount])
  @@index([formId, changedAt])
}
```

**关于"不良类别可扩展"的说明（D-5）**
- 本期五类不良（点伤/划伤/脏污/加硬点/垃圾点）以**固定列**实现：校验、统计、导出最直观，性能最好。
- 若业务方预期不良类别会频繁新增，则切换为**纵向模型**：
  `record_line_defects(line_id, defect_code, surface, qty)` + `defect_code` 走字典。
  切换成本评估：模型变更 + 统计 SQL 改写，约 1~2 人日；建议**在 M2 结束前定稿**，避免上线后迁移。
- 无论哪种模型，**列定义（类别 × 表面）都由字典驱动**，接口返回列定义，新增类别不需改客户端代码。

```prisma
model LineDefect {                                 // 纵向模型预留（可选启用）
  id        String @id @default(uuid())
  lineId    String
  defectCode String                                // spot | scratch | dirt | hard_spot | debris
  surface    String                                // concave | convex | both
  qty        Int    @default(0)
  @@unique([lineId, defectCode, surface])
}
```

#### 4.2.4 操作日志

> 无审批流程（需求 Q8），因此不存在 `DocReview`。留痕由 `RecordHistory`（业务可读快照）+ `AuditLog`（系统级日志）双重承担。

```prisma
model AuditLog {
  id         BigInt   @id @default(autoincrement())
  actorId    String?
  actorName  String?
  action     String                                // login | logout | create | save | void | export | ...
  entity     String                                // form | form_record | user | dict_item | ...
  entityId   String?
  before     Json?                                 // 变更前快照（敏感字段脱敏）
  after      Json?
  ip         String?
  userAgent  String?
  createdAt  DateTime @default(now())
  @@index([entity, entityId, createdAt])
  @@index([actorId, createdAt])
  @@index([createdAt])
}
```

### 4.3 数据流：两种录入场景的完整链路

**场景 A：工序现场分次增量填写**

```
① 录入端打开 → 拉取字典（工序/单位/样品段/参考部件号/不良列定义）

② 输入/扫描表单号 → GET /forms/:formNo
   ├─ 表单不存在 → 提示"新建表单" → POST /forms（先只填 formNo + 样品信息）
   └─ 表单已存在 → 返回**表单 + 已有哪些笔 + 每笔的完成度**
        └─ 录入端提示："已有 2 笔（一次加硬、二次加硬），本次为第 3 笔"

③ 填「样品信息」原始串（若本表单还没填）
   └─ POST /sample/parse 或本地解析 → 结构化字段回填；**任何情况都不改写 sampleRaw**

④ 填本次工序的内容：工序 + 送检人/送检时间/检验人/检验时间/操机员/交接员 + 计数明细
   └─ 只填本次能确定的字段，其余留空（不阻断）

⑤ 保存（mode=incremental）
   └─ POST /forms/:formNo/records（新笔）或 PATCH /records/:id（补填已有笔）
      服务端事务：
      1) SELECT ... FOR UPDATE 锁定表单与该笔
      2) 更新前把"表单快照 + 该笔快照 + 明细快照"写入 RecordHistory
      3) 字段级合并：只更新请求中出现的字段；lines 出现则整笔替换明细
      4) saveCount += 1；recordedBy/lastSavedBy/lastSavedAt 更新
      5) 校验（弱）：V-1/V-3/V-4 降级为 warning；计算 isComplete / missingFields
      6) 重算汇总（totalQty/qualifiedQty/defectQty/passRate）
      7) 写 AuditLog(action=create|save, before/after)
   └─ 响应返回 record + issues + missingFields，供录入端即时提示
   └─ 乐观锁：expectedSaveCount 不匹配 → 409 + 服务端当前内容 + 差异

⑥ 作废（可选）→ POST /records/:id/void
   └─ state = VOID + voidReason + AuditLog；数据与历史全部保留
```

**场景 B：次日由专人一次性完整录入**

```
① 拉取字典（同 A）

② 对每一张纸质表单：
   ├─ POST /forms                       新建表单（formNo + 样品信息原始串 + 单位）
   ├─ POST /sample/parse                解析原始串并与参考部件号比对（可选）
   └─ POST /forms/:formNo/records       逐笔提交，每笔带 mode=full
        或 一次请求提交多笔（见 §6.4 的批量约定）
        服务端：强校验（V-1/V-3/V-4 为 error）；不完整则 422 并返回问题清单

③ 全部录入完成后：
   └─ GET /stats/incomplete-forms       核对是否还有未填完整的表单
   └─ GET /forms/:formNo                逐张复查（含原始轨 vs 结构化轨对照）
```

**两种场景的一致性保证**

| 保证 | 实现 |
|---|---|
| 数据形态一致 | 都落到同一套 `forms` / `form_records` / `record_lines` |
| 历史一致 | 两种模式每次保存都写 `RecordHistory` |
| 汇总一致 | 都走服务端重算 |
| 差异只在 | 校验强度（`enforce`）与调用节奏，**不影响最终数据结构** |

---

## 5. 关键机制设计

### 5.1 样品信息双轨存储

**存储约定**

| 轨道 | 字段 | 规则 |
|---|---|---|
| 原始轨 | `sampleRaw` | 必填、不可为空、**任何解析或人工修改都不改写此字段**（需求 B-6） |
| 结构化轨 | `sampleProject/Model/Spec/Size/Side`（核心5） | 可空；人工修改后置 `sampleVerified=true`；`sampleProject`（M1/M2）必须可录入（B-7） |
| 冗余轨 | `sampleClass`、`sampleIsRx`、`sampleDrawingId` | 尽力填充；未识别保持 null，**不阻塞保存、不参与校验、不作验收条件**（B-8） |
| 差异标记 | `needsReview`、`sampleUnmatchedReason`、`unknownTokens`、`sampleConfidence` | 驱动"待核对"清单，仅提示不阻断 |

**一致性策略**
- `sampleRaw` 为**事实来源**；导出/打印以 `sampleRaw` 呈现，附加结构化字段作为附属信息。
- 若结构化字段与 `sampleRaw` 解析结果冲突（人工改过），保留人工值并记录 `sampleParsedBy='manual'`，审计留痕。
- **参考部件号清单不是判据**：`sampleRaw` 与清单对不上属于正常情况（现场未必照清单填写），因此：
  - 不报错、不降级、不要求用户去改原始串；
  - 仅置 `needsReview=true` + `sampleUnmatchedReason`（`raw_only` 只有原始串 / `no_drawing` 清单无匹配 / `partial` 部分匹配）；
  - 提供后台"待核对清单"供管理员补录字典或确认，属**可选治理动作**，不影响主流程。
- 待核对清单查询：`needsReview = true AND state = 'ACTIVE'`。

### 5.2 样品信息解析器（`backend/internal/sample/parse.go`）

**设计原则**：位置无关的 token 集合匹配；**解析是"尽力而为"的辅助能力，不是录入的前置条件**。

```
输入: "S1 - MD - R - VID2 - MONOC & BINOC"   （参考清单中的规范写法）
        或 "M2 S2 ND2 SMR"                    （现场手写串，可能不规范/有拼写差异）

步骤:
1) 归一化：全角→半角、大小写统一、中文空格→半角空格、去掉多余分隔符
   （统一把 "-" "_" "/" 空格 视为分隔符，但保留 "&" 语义）
2) 切分 token；对每个 token 做字典匹配（DictItem.category 各维度的 code + meta.aliases）
3) 归类：命中哪个 category 就填哪个字段（与 token 位置无关）
   - VID1/VID2/RX/Rx  → sampleModel（"VID1 Rx" 合并为一个 model 值）
   - S1/S2            → sampleSpec
   - MD/SM/LG         → sampleSize
   - L/R              → sampleSide
   - M1/M2            → sampleProject
   - MONOC/BINOC/&    → sampleClass（可组合 → MONOC_AND_BINOC）
4) 用 (spec,size,side,model[,class]) 组合去 SampleDrawing 反查 drawingId / seqNo
   └─ 仅作为**参考匹配**：命中则记录，未命中不视为错误
5) 输出置信度与标记（均为辅助信息，不阻断保存）：
   - 命中参考清单                  → confidence 1.0, needsReview false
   - 五核心字段齐全但未命中清单    → confidence 0.6, needsReview true, reason=no_drawing
   - 核心字段缺失                  → confidence <0.6, needsReview true, reason=partial
   - 仅能保留原始串                → confidence 0,   needsReview true, reason=raw_only
   - 未识别 token                  → 收集进 unknownTokens，供字典补录（**不报错**）
6) 写入/更新 sample_parse_cache，同类串二次录入直接复用（S-6）
```

> 注意（来自需求 §4.2.2）：**层级顺序 ≠ 字符串书写顺序**。字符串顺序为 `规格-大小-左右-型号-结构类别`，业务层级为 `项目→型号→规格→大小→左右`。解析器必须按 token 集合归类，禁止按位置取字段。

**不确定信息的处理原则（B-8）**
- `ND2`（照片中疑似读作 VID2）：无法确认。→ 若字典中有 `VID2` 的别名映射可自动纠正；否则原样进 `unknownTokens`，**不猜测、不阻断**。
- 序号 10/13/14/20/21/22 未标结构类别：`sampleClass` 一律留 null，**不需要**任何人去确认（登记为 `【待定】`）。
- 冗余字段（`sampleClass` / `sampleIsRx` / `sampleDrawingId`）全部可空、不参与任何校验、不纳入验收标准。

### 5.3 校验（单一实现，可被复用）

```ts
// backend/internal/inspection/validate.go   规则编号与需求文档 §4.1.3 一一对应
validateLine(line, mode): Issue[]              // V-1..V-5（行级）
validateRecord(record, lines, mode): Issue[]   // V-4, V-6, V-10 + 汇总一致性
validateForm(form): Issue[]                    // V-7, V-8, V-9（表单级）
parseSample(raw): ParsedSample                 // S-1..S-8
missingFields(record, lines): string[]         // V-10 缺失项（驱动 isComplete）
```

| 规则 | 完整模式（`mode=full`） | 增量模式（`mode=incremental`） |
|---|---|---|
| V-1 合格+不良=总数 | **error**（阻断） | **warning** |
| V-2 Σ15子项 = 不良品 | warning（业务方已确认存在重复计数 B-12，登记 `【待定】`） | warning |
| V-3 子项边界（各类别三子项之和 ≤ 不良品；子项 ≤ 总数） | error | warning |
| V-4 非负整数；总数 > 0 | error | warning（仅校验非负整数） |
| V-5 良率派生 | 计算（服务端重算） | 计算（服务端重算） |
| V-6 送检时间 ≤ 检验时间 ≤ 当前时间 | warning（表单上的时间常为人工补记） | warning |
| V-7 `formNo` 唯一；`(formId, sequenceNo)` 唯一 | error | error |
| V-8 样品信息与参考清单不匹配 | warning + `needsReview=true` | warning |
| V-9 `sampleProject`（M1/M2）为空 | warning | warning |
| V-10 表单既有栏目缺失（单位/送检人/送检时间/检验人/检验时间/操机员/交接员） | warning + 返回 `missingFields[]` | warning + 返回 `missingFields[]` |

**设计决定**
1. 服务端**永远重算** `totalQty/qualifiedQty/defectQty/passRate`，忽略客户端上报的汇总值。
2. 只有 **V-7 恒具阻断力**；V-1/V-3/V-4 仅在 `mode=full` 下阻断。其余规则只产生警告与标记。
3. **一切"还没填完"都不阻断保存**（需求总原则）——这直接决定了"现场分次填写"能成立。
4. `isComplete` 由 V-10（及 V-9）的缺失项是否为空计算，**不由校验是否通过计算**。

### 5.4 表单号生成（并发安全）

```
格式: {prefix}-{YYMMDD}-{seq:2}       示例 M-260908-01
实现: 表 form_no_sequence(prefix, ymd, lastSeq) + 事务内 SELECT ... FOR UPDATE / UPSERT 自增
      或 DB 序列 + 唯一索引兜底重试
规则: 日期取检验日期（Q7 待确认）
      formNo 由系统生成时保证唯一；调用方手工录入时校验唯一（V-7）
      **formNo 是「表单」的键**：手工录入已存在的 formNo → 意味着"给这张已有表单再加一笔"
      或"更新已有的一笔"，而不是新建表单（§5.6）
```

### 5.5 认证与授权

**认证**
- 登录：`POST /api/auth/login` → 校验 Argon2id → 创建 Session（随机 256bit token，仅存哈希）→ `Set-Cookie: sid=...; HttpOnly; Secure; SameSite=Lax; Path=/`；同时返回 Bearer token（供非浏览器调用方）
- 会话：默认有效期 12h，空闲 2h 失效（`lastSeenAt` 滑动续期）；本人可登出、管理员可吊销
- 失败锁定：连续 5 次失败 → `lockedUntil = now + 10min`（U-4）
- 首次登录/管理员重置 → `mustChangePwd=true`，仅允许访问改密接口（U-3）
- 【待确认 Q9】预留 `AuthProvider` 接口，便于后续接企业微信/LDAP/OIDC

**授权（放开模式，需求 B-14）**
- **不实现角色与权限点**。所有已登录用户对业务接口拥有全部能力：读取、创建、更新、作废、维护字典、查看历史与统计、导出。
- 唯一的边界：
  1. `AuthGuard`：**必须登录**（认证不能放开）；
  2. `AdminOnlyGuard`：仅"用户管理"接口（创建/停用用户、重置密码）要求 `user.isAdmin = true`。
- **可替换性要求（重要）**：把"谁能做什么"抽成 `AuthorizationService`，接口签名如
  `can(action: Action, ctx: { actor, form?, record? }): boolean`，**当前实现恒返回 `true`**（除用户管理）。
  将来生产部门明确人员安排后，只需实现该服务，**不改动任何业务代码**。
- 所有写操作从会话取操作人写入 `recordedBy` / `lastSavedBy`，**不接受客户端传入的身份字段**。

### 5.6 保存语义：字段级合并 + 历史快照

> **需求依据**：B-9（分次录入，可能只填一部分）、B-10（无审核）、需求 §5.2（两种录入场景）。

**核心语义**

| 项 | 规则 |
|---|---|
| 标识 | `formNo` 标识表单；`(formId, sequenceNo)` 标识一笔 |
| 保存入口 | 一个接口承担全部写入：`POST /api/forms/:formNo/records`（新建一笔）与 `PATCH /api/records/:id`（更新一笔）。两者都走同一套合并逻辑 |
| 合并语义 | **字段级合并**：请求里出现的字段才更新；未出现的保持原值。明细（`lines`）若出现，则**整笔替换**该笔明细（不做行级合并） |
| 校验强度 | `enforce: warn`（默认，只提示不阻断）\| `strict`（把自相矛盾的数据拦下来）。见 §5.3 |
| 历史 | 更新前把**该笔保存前内容 + 所属表单的当前快照**写入 `RecordHistory`，追加写、不可改不可删 |
| 不可变 | `form.createdBy/createdAt`、`record.recordedBy/recordedAt`、`id`、`sequenceNo` |
| 递增 | `record.saveCount += 1` |
| 并发 | 乐观锁：请求可携带 `expectedSaveCount`（或 `If-Match`）；不一致 → `409` + 服务端当前内容 + 差异 |
| 幂等 | 请求可携带 `clientRequestId`；服务端记录并去重，防弱网重试造成重复保存 |
| 汇总 | 服务端按笔重算 `totalQty/qualifiedQty/defectQty/passRate`，忽略客户端上报值 |
| 完整性 | 服务端重算 `isComplete` / `missingFields`（V-9、V-10） |
| 作废 | `state='VOID'` + `voidReason`；数据与历史保留；统计默认排除 |

**服务端事务伪代码**

```
saveRecord(input, actor, mode):
  tx:
    form = SELECT * FROM forms WHERE form_no = :formNo FOR UPDATE
    if form == null:
        validateForm(input.form)                       # V-7/V-8/V-9
        form = INSERT INTO forms(...)                  # createdBy = actor
    else:
        mergeInto(form, input.form)                    # 字段级合并（只改提交的字段）
        recomputeFormParseFlags(form)

    if input.sequenceNo == null:                       # 新建一笔
        seq = nextSequenceNo(form)                     # 事务内 max+1
        record = INSERT INTO form_records(form_id, sequence_no=seq,
                     recorded_by=actor, save_count=1, ...)
        action = 'create'
    else:
        record = SELECT * FROM form_records
                 WHERE form_id=form.id AND sequence_no=input.sequenceNo FOR UPDATE
        if record == null: 404
        assert !input.expectedSaveCount || input.expectedSaveCount == record.saveCount   # 否则 409
        INSERT INTO record_history(record_id, form_id, save_count=record.saveCount,
               form_snapshot=rowToJson(form), record_snapshot=rowToJson(record),
               lines_snapshot=loadLines(record),
               saved_by=record.lastSavedBy, saved_at=record.lastSavedAt,
               changed_by=actor.id, reason=input.reason)
        mergeInto(record, input.record)                # 字段级合并（只改提交的字段）
        record.saveCount += 1
        action = 'save'

    if input.lines != null:                            # 明细提交则整笔替换
        DELETE FROM record_lines WHERE record_id = record.id
        INSERT record_lines(...)

    issues = validate(form, record, lines, mode)       # §5.3；mode=full 时 V-1/V-3/V-4 为 error
    if issues.hasError: rollback + 422(issues)
    recomputeTotals(record)                            # 服务端重算
    record.isComplete, record.missingFields = completeness(record, lines)
    record.lastSavedBy = actor.id; record.lastSavedAt = now()
    form.lastSavedBy = actor.id;    form.lastSavedAt = now()
    INSERT audit_logs(action, entity='form_record', entity_id=record.id,
                      before, after, actor, ip, ua)
  return { form, record, issues, summary }
```

**历史与差异**
- 查询历史：`GET /api/records/:id/history` → 按 `saveCount` 倒序返回每一版。
- 差异：`backend/internal/inspection/diff.go` 提供 `DiffRecords(v1, v2)`，返回**字段级 + 明细级**变更清单（旧值/新值）。
- `RecordHistory` 里同时存了当时所属表单的快照，因此也能回答"这次保存是否顺手改了样品信息"。

### 5.7 操作日志与完整性

- 写入方式：全局 `AuditInterceptor`（登录、导出、用户/字典变更）+ 领域服务在**事务内**写 `AuditLog`（创建表单/新建笔/保存/作废）。
- 内容：操作人（id + 当时姓名）、动作、对象、变更前后 JSON、IP、UA、时间。
- 与 `RecordHistory` 的分工：`RecordHistory` 面向**业务回看**（"这一笔上次填的是什么"），`AuditLog` 面向**系统留痕**（"谁在何时动了什么"）。
- 脱敏：`passwordHash`、token 等永不入日志。
- 只读性：应用账号对 `audit_logs`、`record_history` 只有 INSERT/SELECT 权限（无 UPDATE/DELETE），DB 层保证不可篡改。
- 保留期：默认 3 年（Q12）。

### 5.8 弱网与幂等

| 机制 | 说明 |
|---|---|
| 幂等保存 ✅ | 表 `idempotency_requests` + 中间件 `internal/idempotency/http`：请求头 `X-Idempotency-Key` 或请求体 `client_request_id` 作键，`(user_id, 键)` 唯一，命中则回放首次响应（`X-Idempotent-Replay: true`）；同键不同体返回 409；非 2xx 不缓存以便重试；TTL 由 `IDEMPOTENCY_TTL` 控制（默认 24h）。详见 §6.6 |
| 增量合并的天然容错 | 调用方不需要先读后写整份数据，只提交本次填写的字段，减少冲突面 |
| 乐观锁冲突 | 返回 `409` + 服务端当前内容 + 差异，调用方据业务判断是否重试 |
| 字典缓存 | 字典接口返回 `version`/`etag`，调用方可长期缓存 |

### 5.9 接口使用场景（不是前端开发任务）

> 本期**不交付前端**（需求 B-15）。此表用于说明接口需要覆盖的场景，便于验证接口设计是否够用。

| 场景 | 需要的接口能力 |
|---|---|
| 工序现场分次填写 | 按 `formNo` 取表单 + 已有笔列表；新建/更新一笔（增量）；返回即时校验与缺失项 |
| 次日集中录入 | 一次提交"表单 + 多笔 + 每笔明细"（完整模式）；或按笔逐笔录入（同接口） |
| 查询与核对 | 按多条件检索、返回表单及全部笔、返回每笔完成度 |
| 追溯 | 查询某笔的全部历史快照与字段级差异 |
| 统计与导出 | 按时间/工序/样品维度聚合；导出 Excel/CSV |
| 字典维护 | 查询与维护工序、单位、样品段、参考部件号 |
| 用户管理 | 创建/停用用户、重置密码（仅 `isAdmin`） |

---

## 6. 接口设计（REST）

> **本节已按实际实现对齐**（v0.6）。字段命名一律 **snake_case**（JSON tag 与数据库列名一致）。
> 逐接口的请求/响应示例、错误码表、字段中文名对照见 [`docs/api.md`](api.md)（面向前端对接，更详细）。

统一前缀 `/api`，响应 `{ code, message, data }`；错误使用 HTTP 语义码。
**除 `GET /healthz`、`GET /`、`POST /api/auth/login` 外，所有接口都需要登录**（认证是唯一硬边界；权限只有"普通用户 / 管理员"两级，需求 B-14）。

### 6.1 认证与用户
| 方法 | 路径 | 说明 | 限制 |
|---|---|---|---|
| POST | `/api/auth/login` | 登录（返回 token；同时下发 Cookie） | 公开 |
| POST | `/api/auth/logout` | 登出 | 登录 |
| GET | `/api/auth/me` | 当前用户信息 | 登录 |
| POST | `/api/auth/change-password` | 修改密码（会吊销全部会话） | 登录 |
| GET | `/api/users` | 用户列表（分页 + `keyword`） | **管理员** |
| POST | `/api/users` | 创建用户 | **管理员** |
| PATCH | `/api/users/:id` | 修改用户（启停、重置密码、设置 `is_admin`） | **管理员** |

> 无 `/roles`、`/permissions` 接口（需求 B-14：不做角色权限体系）。

### 6.2 字典、单位、参考部件号
| 方法 | 路径 | 说明 | 限制 |
|---|---|---|---|
| GET | `/api/dict/items?category=&include_inactive=` | 按类别取字典 | 登录 |
| POST | `/api/dict/items` | 新增/更新字典项（按 category+code） | 登录 |
| PATCH | `/api/dict/items/:id` | 改标签、排序、启停 | 登录 |
| GET | `/api/dict/categories` | 返回全部字典类别 | 登录 |
| GET | `/api/units?include_inactive=` | 单位列表 | 登录 |
| POST | `/api/units` | 新增/更新单位 | 登录 |
| GET | `/api/dict/drawings?project=&model=&spec=&size=&side=&keyword=` | **参考**部件号查询（最多 500 条） | 登录 |
| POST | `/api/dict/drawings/import` | 批量导入（`items` 数组或制表符 `rows`） | 登录 |
| GET | `/api/dict/defect-columns` | 不良列定义（类别 × 表面 + 顺序） | 登录 |

> 字典接口当前**不返回** `version`/`etag`（无缓存协商字段）。

### 6.3 样品信息
| 方法 | 路径 | 说明 | 限制 |
|---|---|---|---|
| POST | `/api/sample/parse` | 解析原始串 → 结构化字段 + 参考匹配 + 置信度 + `unmatched_reason`（**纯辅助，不写库**） | 登录 |
| POST | `/api/sample/confirm` | 人工确认/修正结构化字段（**不改写 `sample_raw`**） | 登录 |
| GET | `/api/sample/cache?sample_raw=` | 查看规范化键与 token 切分 | 登录 |
| GET | `/api/sample/pending` | 待核对清单（`needs_review=true`） | 登录 |
| GET | `/api/forms/:formNo/sample/pending` | 某表单的样品信息提示 | 登录 |

### 6.4 表单与工序记录（核心）
| 方法 | 路径 | 说明 | 限制 |
|---|---|---|---|
| GET | `/api/forms` | 表单列表（`keyword` / `state` / `needs_review` / `from` / `to` / `sample_project` / `sample_model` / `sample_spec` / `sample_size` / `sample_side` / `unit` + 分页） | 登录 |
| POST | `/api/forms` | 新建表单（可只带 `form_no` + 样品信息），返回 **201** | 登录 |
| GET | `/api/forms/:formNo` | **取表单及其全部笔与明细**（现场"接着填"与集中录入都靠它） | 登录 |
| PATCH | `/api/forms/:formNo` | 更新表单级字段（**字段级合并**），返回 **200** | 登录 |
| POST | `/api/forms/:formNo/records` | **新建一笔**；带 `records` 数组时批量提交多笔（整批原子） | 登录 |
| GET | `/api/records/:id` | 取一笔的完整内容（含明细） | 登录 |
| PATCH | `/api/records/:id` | **保存/更新一笔**（字段级合并；明细提交则整笔替换）—— 分次填写的主接口 | 登录 |
| POST | `/api/records/:id/void` | 逻辑作废 + 原因 | 登录 |
| GET | `/api/records/:id/history` | 该笔的全部历史快照（按 `save_count` 倒序） | 登录 |
| GET | `/api/records/:id/history/diff?from=&to=` | 两版之间的字段级 + 明细级差异（`to=current` 表示当前） | 登录 |
| GET | `/api/audit-logs?entity=&entity_id=&limit=` | 操作日志检索 | 登录 |

> **尚未实现**（需求已确认、待后续）：`GET /forms/:formNo/summary`（表单完成度汇总）、`GET /forms/:formNo/export`、`GET /forms/export`（导出）。
> 表单完成度可由 `GET /api/forms/:formNo` 里每笔的 `is_complete` / `missing_fields` 自行汇总。

**保存请求（`POST /forms/:formNo/records` 与 `PATCH /records/:id` 共用；批量时用 `records` 数组）**

```jsonc
{
  "enforce": "warn",              // warn（默认，只警告不阻断）| strict（拦下自相矛盾的数据）
  "mode": "merge",                // merge（默认，字段级合并）| replace（整体替换）
  "sequence_no": 2,               // 可选：指定笔序号；不传则自动递增（仅在单笔提交时有效）
  "expected_save_count": 1,       // 可选：乐观锁，不匹配返回 409
  "client_request_id": "uuid",    // 可选：幂等键（也可用请求头 X-Idempotency-Key）
  "reason": "补填二次加硬",         // 可选：变更原因，写入历史与操作日志
  "form": {                       // 可选：本次要改动的表单级字段（只提交要改的）
    "sample_raw": "M2 S2 ND2 SMR",
    "sample_project": "M2",
    "unit_id": "u-01"
  },
  "record": {                     // 可选：本次要改动的笔级字段
    "process_code": "一次加硬",
    "submitted_by": "李四", "submitted_at": "2026-09-08T09:00:00+08:00",
    "inspected_by": "张赛赛", "inspected_at": "2026-09-09T10:30:00+08:00",
    "machine_operator": "王五", "handover_by": "赵六"
  },
  "lines": [                      // 可选：出现即整笔替换本笔明细
    { "seq": 1, "total_qty": 100, "qualified_qty": 100, "defect_qty": 0 }
  ]
}
```

**响应（每次保存都返回，便于调用方即时提示）**

```jsonc
{
  "code": "ok", "message": "",
  "data": {
    "action": "create_record",    // create_form | create_record | save_record | update_form | create_form+create_record
    "form":   { "id": "...", "form_no": "M-260908-01", "sample_raw": "..." },
    "record": { "id": "...", "sequence_no": 1, "save_count": 1,
                "is_complete": false,
                "missing_fields": ["submitted_by", "machine_operator"],
                "total_qty": 100, "qualified_qty": 100, "defect_qty": 0, "pass_rate": 100 },
    "issues":   [ { "rule": "V-10", "level": "warning",
                    "field": "submitted_by", "message": "表单既有栏目「送检人」尚未填写" } ],
    "warnings": [ /* 同 issues 中的 warning */ ],
    "errors":   [],
    "missing_fields": ["submitted_by", "machine_operator"],
    "is_complete": false
  }
}
```

**批量提交**（次日集中录入）：把 `records` 传成数组，整批**在同一个事务内**保存，任一笔失败则整批回滚：

```jsonc
{
  "enforce": "warn",
  "form": { "sample_raw": "M1 S1 MD L VID1 Rx", "sample_project": "M1" },
  "records": [
    { "record": { "process_code": "一次加硬" }, "lines": [ { "total_qty": 200, "qualified_qty": 198, "defect_qty": 2 } ] },
    { "record": { "process_code": "二次加硬" }, "lines": [ { "total_qty": 200, "qualified_qty": 200 } ] }
  ]
}
// -> { "data": { "results": [ …两笔… ], "count": 2, "atomic": true } }
```

### 6.5 汇总统计
| 方法 | 路径 | 说明 | 限制 |
|---|---|---|---|
| GET | `/api/stats/dimensions` | 可用分组维度列表 | 登录 |
| GET | `/api/stats/summary?group_by=sample_spec,sample_size&from=&to=&…` | 按维度分组汇总（最多 500 组） | 登录 |
| GET | `/api/stats/defects?from=&to=&…` | 不良类别 × 表面分布 | 登录 |
| GET | `/api/stats/workload?by=inspected_by\|recorded_by\|last_saved_by` | 工作量统计（最多 200 组） | 登录 |
| GET | `/api/stats/incomplete-forms?limit=` | 未完成表单清单 | 登录 |
| GET | `/api/audit-logs` | 操作日志检索（默认 100，上限 500） | 登录 |

**`group_by` 白名单（16 个维度，取值必须是 snake_case）**：
`sample_project`、`sample_model`、`sample_spec`、`sample_size`、`sample_side`、`sample_class`、`sample_raw`、`form_no`、`process`、`unit`、`inspected_by`、`submitted_by`、`machine_operator`、`handover_by`、`day`、`month`（可任意组合）。
筛选参数（`summary` / `defects` / `workload` 共用）：`from`、`to`、`process`、`sample_project`、`sample_model`、`sample_spec`、`sample_size`、`sample_side`、`inspected_by`、`unit`、`include_void`。

> **统计口径提示**：不良子项合计可能大于不良品数（同一片镜片可计入多个类别，需求 Q5，登记待定）。
> **尚未实现**：`/stats/save-activity`、导出接口（Excel/CSV）。

### 6.6 幂等（写接口重复提交保护）

写方法（`POST`/`PATCH`/`PUT`/`DELETE`）支持幂等键：

| 项 | 说明 |
|---|---|
| 键来源 | 请求头 `X-Idempotency-Key`（或 `Idempotency-Key`）优先；其次请求体 `client_request_id`（或 `clientRequestId` / `idempotency_key`） |
| 作用域 | `(user_id, idempotency_key)`，不同用户互不影响 |
| 命中行为 | 请求体指纹一致 → **回放首次响应**，响应头 `X-Idempotent-Replay: true`；指纹不同 → `409 conflict` |
| 不缓存的响应 | 非 2xx（失败）不写幂等记录，允许用同一个键重试 |
| 有效期 | `IDEMPOTENCY_TTL`（默认 24h；`<=0` 关闭该功能） |
| 存储 | 表 `idempotency_requests`（迁移 `0002_idempotency`）；读取时顺手清理过期记录 |

> 幂等表故障**不阻断业务**（记录错误后照常处理请求），保证"保护机制本身不会成为故障点"。

---

## 7. 性能与索引

| 场景 | 措施 |
|---|---|
| 表单列表筛选（表单号/日期/工序/样品维度/检验人） | 见 §4.2.3 的索引：`forms(状态+创建时间)`、`form_records(process+检验时间)`、`form_records(检验人+检验时间)` |
| 层级检索（S2+SM+L） | `forms(sampleSpec, sampleSize, sampleSide, sampleModel)` 复合索引；避免全表扫描 |
| 按表单号载入（现场"接着填"） | `formNo` 唯一索引 + `form_records(formId)` 索引，一次查询即可拿到表单与全部笔 |
| 统计 | 汇总字段落库在 `form_records`（`totalQty/qualifiedQty/defectQty`），**统计只需扫一张表，不必 JOIN 明细行**（明细只在导出时读取） |
| 保存写入成本 | 单事务内：写 1 条 `record_history`（小 JSON）+ 替换明细（通常 1 行）+ 更新记录与表单。历史条数 = 保存次数（个位数），开销可忽略 |
| 历史查询 | `record_history(recordId, saveCount)` 索引；详情按需分页 |
| 导出大结果集 | 流式写入（游标 + 分批），限制单次导出上限（如 5 万行） |
| 目标 | 单笔保存 P95 < 500ms；1000 笔量级条件查询 P95 < 2s |

---

## 8. 安全设计

| 项 | 措施 |
|---|---|
| 密码 | Argon2id（memory 64MB, iterations 3, parallelism 1）；禁用弱密码；不得明文落库与落日志 |
| 会话 | 随机 256bit；仅存哈希；HttpOnly + Secure + SameSite=Lax；支持全端吊销 |
| **唯一硬边界** | 业务接口一律要求登录；**不做业务权限校验**（B-14），但身份必须可信 |
| 身份来源 | 操作人一律取自会话/Bearer token，**不接受请求体中的操作人字段** |
| 管理员边界 | 仅用户管理接口校验 `isAdmin`；该标记不可由用户自助修改 |
| 输入 | 请求体字段白名单（只认识结构体上声明过的列名）+ `sqlx` 参数化查询（无拼接 SQL）；未知字段直接报 400，便于调用方发现拼写错误 |
| 限流 | 登录接口按 IP + 账号限流；导出接口按用户限流 |
| 传输 | 强制 HTTPS（反代跳转）；内网自签证书需导入信任 |
| 日志不可篡改 | `audit_logs`、`record_history` 的应用账号无 UPDATE/DELETE 权限 |
| 备份 | 每日 `pg_dump` + WAL 归档；备份文件加密；恢复演练纳入验收 |
| 隐私 | 仅采集姓名/工号等最小必要信息 |

---

## 9. 测试策略

| 层级 | 范围 | 要点 |
|---|---|---|
| 单元测试 | `internal/inspection`、`internal/sample`：校验 V-1..V-10（**按 enforce 分级**）、样品解析器（含 `M2 S2 ND2 SMR`、`S1 - MD - R - VID2 - MONOC & BINOC`、无法识别的 `ND2`）、良率计算、`diffRecord`、`missingFields` | 覆盖率 ≥ 90%（核心逻辑） |
| 服务测试 | **合并保存语义**：① 同一笔分 3 次提交不同字段 → 最终内容正确、`saveCount=3`、历史 2 条；② 明细提交 → 整笔替换；③ 未提交字段不被清空；④ 乐观锁 409；⑤ 幂等键去重；⑥ 两种 `enforce` 的校验分级差异 | 每次保存必须产生 1 条 `RecordHistory` + 1 条 `AuditLog` |
| 接口测试 | 所有接口**未登录必须 401**；用户管理接口非 `isAdmin` 必须 403；其余接口任何登录用户均可访问（验证 B-14 放开语义） | 401/403 语义正确 |
| E2E（接口级） | 创建表单 → 分次提交 3 笔（含只填一部分）→ 查询表单得到完整视图 → 更新同一笔 → 查看历史与差异 → 作废 → 导出 | 覆盖需求 §8 的两个场景 |
| 性能 | 1000/10000 笔数据下的列表、统计、含多版历史的详情 | 对照 §7 目标 |
| 验收 | 对齐需求文档 §8 验收标准（重点第 1、2 条：两种录入场景） | |

---

## 10. 里程碑与实施计划（后端）

| 阶段 | 交付 | 关键设计落地 |
|---|---|---|
| M1 骨架 ✅ | Go 工程、SQL 迁移（三层模型）、登录与会话、用户管理（两级）、字典管理 | §4.2.1、§4.2.2、§5.5 |
| M2 核心保存 ✅ | 表单/笔/行的创建与**字段级合并保存**、`enforce` 分级校验、`RecordHistory`、完成度 | §4.2.3、§4.3、§5.3、§5.6 |
| M3 样品信息 | 解析器 + 缓存 + 参考部件号匹配 + 待核对清单 + 层级检索 | §5.1、§5.2 |
| M4 查询与导出 | 列表/详情/历史差异、导出与导入模板、操作日志 | §6.4、§6.5 |
| M5 统计与上线 | 统计、备份与部署脚本、性能优化、验收测试 | §7、§8、§9 |

---

## 11. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| **使用环节未知（Q20）** | 若按"集中录入"假设简化校验，现场分次填就用不了；反之过度灵活会导致数据零碎 | **两种节奏都实现**（字段级合并 + `enforce` 参数）；校验强度可切换；完成度用 `isComplete` 显式表达 |
| **多工序落库形态未定（Q19）** | 工序字段归属（笔 vs 行）可能返工 | 笔与行**都保留** `processCode`，两种现实都能表达；确认后收敛（改动仅涉及查询与展示，不动核心保存逻辑） |
| 表单上的"检验人"与实际录入人混淆（Q18） | 追溯失真 | 拆成两组字段：`inspectedBy`（表单事实）与 `recordedBy`（系统留痕） |
| 权限放开（B-14）后误改他人数据 | 数据被改坏 | 每次保存留痕（`RecordHistory` + `AuditLog`）+ 逻辑作废不删数据；`AuthorizationService` 预留，将来可收紧 |
| 样品信息含义无法确认（Q2） | 冗余字段可能永远为空 | `sampleRaw` 保证数据价值；冗余字段可空、不校验、不作验收；新维度只需加字典 |
| 手写串不规范（如 `ND2`） | 解析命中率低 | 解析缓存 + 人工修正 + `unknownTokens` 驱动字典补录；**不阻塞保存** |
| 不良类别可能扩展（D-5） | 表结构迁移 | M2 结束前定稿；预留 `LineDefect` 纵向模型；列定义由接口下发 |
| 不良重复计数未解决（Q5） | 统计口径失真 | V-2 恒为警告；统计接口明确标注；登记 `【待定】` |
| 弱网导致重复提交 | 重复保存/重复笔 | 幂等键 `clientRequestId`（§5.8） |
| 质量数据可信度 | 数字记录易被质疑 | 历史快照与操作日志不可删（DB 层限制）；必要时后续加"内容指纹" |
| 参考部件号清单与实际填写脱节（B-6） | 结构化字段长期为空 | 允许"仅原始串"路径；用 `needsReview` 清单统计缺口，用实际录入串反哺字典 |

---

## 附录 A：与需求文档的对应关系

| 需求条目 | 设计落地位置 |
|---|---|
| B-2/B-3 工序独立项目 | `FormRecord.processCode/processText`（并同时保留在 `RecordLine`，见 Q19）；字典 category=`process` |
| B-4 子格为镜片数量 | `RecordLine` 15 个整数字段 + 校验 V-2/V-3 |
| B-6 原始输入为准 | `Form.sampleRaw` 为事实来源；解析与人工修正均不改写；V-8 恒为 warning（§5.1、§5.2） |
| B-7 项目 M1/M2 必须纳入 | `Form.sampleProject` + 字典 `sample_project`（V-9 提示缺失） |
| B-8 冗余字段可有可无 | `sampleClass` / `sampleIsRx` / `sampleDrawingId` 可空、不校验、不作验收（§5.1） |
| **B-9 分次录入 / 两种场景** | **三层模型**（Form/Record/Line）+ **字段级合并保存** + `enforce: warn\|strict`（§4.2.3、§5.2、§5.6） |
| B-10 无审核流程 | 无审批状态机、无 `/review`、无 `DocReview` 表（§5.6） |
| **B-11 表单既有栏目全部纳入** | `FormRecord.submittedBy/submittedAt/inspectedBy/inspectedAt/machineOperator/handoverBy` + `Form.unitId`；缺失走 V-10 提示 |
| B-12 不良重复计数 | V-2 恒为 warning；统计接口标注（§5.3） |
| **B-13 率值行不处理** | **不建字段**（无 `manualRateNote`），不参与任何计算 |
| **B-14 权限放开** | 删除 `Role/Permission/UserRole` 表与权限点；仅 `isAdmin` 保护用户管理；`AuthorizationService` 可替换（§5.5） |
| **B-15 仅后端** | 仓库内 `backend/` 只含后端；文档中的"场景"仅用于说明接口能力（§3、§5.9） |
| U-8/U-12 留痕 | `AuditLog` 拦截器 + `RecordHistory`（§5.6、§5.7） |
| Q-1 层级检索 | 复合索引（§4.2.3、§7） |
| Q-18 表单人与录入人区分 | `inspectedBy` vs `recordedBy`（§4.2.3） |
| Q-19 多工序形态 | 笔与行都保留工序字段（§4.2.3、§5.1） |

## 附录 B：变更记录

| 版本 | 日期 | 变更 | 作者 |
|---|---|---|---|
| v0.1 | 初始 | 依据需求文档 v0.1 编写初步设计 | — |
| v0.2 | 第二轮确认后 | ① 删除审批状态机与 `DocReview`，改为 UPSERT 覆盖 + `DocHistory` 快照（§4.2.3、§4.3、§5.6）；② `formNo` 升为记录唯一键，新增并发乐观锁与 409 约定；③ 样品信息改为"原始输入优先"，冗余字段不校验、不作验收（§5.1、§5.2）；④ 表头删除送检人/操机员/交接员；⑤ 校验 V-2 降级为 warning、新增 V-9、明确只有 V-1/V-3/V-4/V-6/V-7 具阻断力；⑥ 权限点与 API 随之调整（删除 submit/review/revise，新增 history/diff）；⑦ 前端路由删除 `/review`，新增历史差异视图；⑧ 里程碑、测试策略、风险表更新 | — |
| **v0.3** | 第三轮确认后 | ① **数据模型改为三层**：`Form` / `FormRecord` / `RecordLine` + `RecordHistory`（§4.1、§4.2.3）；② **保存语义改为字段级合并**，`mode: incremental \| full` 控制校验强度（§5.6）；③ **恢复表单既有栏目**并新增 `recordedBy` 区分"表单上的人"与"录入人"（§4.2.3、Q18）；④ **删除 RBAC 四张表**，改为 `User.isAdmin` + 可替换的 `AuthorizationService`（§4.2.1、§5.5）；⑤ **新增完成度** `isComplete` / `missingFields` / 未完成表单清单（§5.7、§6.4）；⑥ API 全面调整（`/forms`、`/records`、history/diff、defect-columns），删除 roles 接口；⑦ **删除前端目录与页面章节**，改为"接口使用场景"（§3、§5.9）；⑧ 校验新增 V-10、V-6 降级为 warning、按 mode 分级（§5.3）；⑨ 性能、安全、测试、里程碑、风险表全部更新 | — |
| **v0.5** | 骨架落地后（实现回写） | ① **技术栈落定并已实现**：Go 1.24 + Gin + sqlx，SQLite / PostgreSQL 双驱动（§1.3、§2）；② 表定义改为**手写 SQL 迁移**（`backend/migrations/{sqlite,postgres}/`），§4.2 的 Prisma 风格 DDL 仅作可读表结构说明；③ 时间列统一 **TEXT + UTC RFC3339**，双库行为一致（§4.2）；④ 校验参数由 `mode: incremental\|full` 改为 **`enforce: warn\|strict`**（与需求 v0.4 对齐），并明确 V-2/V-6/V-8/V-9/V-10 **任何模式都不阻断**；⑤ 仓库结构定为**单一仓库**：`lens-qs` 为项目根，`docs/` 与 `backend/` 平级，前端将来放 `frontend/`（§3）；⑥ 权限收敛为**两级**（普通用户 / 管理员，§5.5）；⑦ **汇总统计的通用 `group_by` 已实现**（需求 §4.7 的基础能力已具备）；⑧ 测试策略按实际落地的用例清单更新 | — |
| **v0.6** | 幂等与批量补丁后 | ① **§6 接口清单按实际实现对齐**（path 全部带 `/api` 前缀、字段名 snake_case、`mode: merge\|replace` + 独立 `enforce`、`Issue.rule`；标注尚未实现的 `/forms/:formNo/summary`、导出、`/stats/save-activity`）；② **新增 §6.6 幂等设计**并新增表 `idempotency_requests`（迁移 `0002_idempotency`）；③ **批量提交改为整批原子**（`SaveBatch` 同事务，失败整批回滚）；④ §4.1 ER 概览与 §5.8 同步更新 | — |

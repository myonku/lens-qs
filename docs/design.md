# 镜片表面质量检验记录系统 — 初步系统设计

| 项目 | 内容 |
|---|---|
| 文档版本 | v0.2（已并入业务方第二轮确认：取消审核、覆盖录入、原始输入优先） |
| 状态 | 草稿 |
| 上游文档 | `docs/requirements.md`（需求文档 v0.2） |

> 本文档的目标：把需求文档中的「需要」落成可施工的**核心设计**——技术选型、模块划分、数据模型、接口、关键机制（双轨存储 / 解析 / 覆盖与历史 / 权限 / 审计）。
> 标注 `【待确认】` 与需求文档一致；标注 `【设计决定】` 表示设计侧已定，评审可直接否定。

> **v0.2 设计层面主要变更**
> 1. **删除审批状态机**（原 D-8、原 §5.6 的 DRAFT/SUBMITTED/APPROVED/REJECTED/ARCHIVED）→ 改为"保存即生效 + 覆盖 + 追加式历史快照"（新 §5.6）。
> 2. **表单号成为记录唯一键**：同一表单号只有一条当前记录，重复保存走**覆盖**（UPSERT），新增 `doc_history` 历史快照表。
> 3. **样品信息以 `sample_raw` 为事实来源**，参考部件号清单降级为"最佳努力"的冗余字段，不参与校验。
> 4. 表头去掉送检人 / 送检时间 / 操机员 / 交接员。
> 5. 校验 V-2 降级为警告（重复计数已确认存在）、V-8 不阻断、新增 V-9；新增 V-7'（表单号唯一）。

---

## 1. 设计目标与约束

### 1.1 设计目标
1. **现场优先**：手机浏览器 60 秒内完成一条记录，界面与纸质表单同构。
2. **双轨可追溯**：样品信息原始串永不失真（事实来源），结构化字段可查可统计。
3. **保存即生效**：无审批流，现场负担最小；同一表单号多次录入以**最新为准**且历史可回看。
4. **可演进**：新增工序、样品维度、单位、甚至新不良类别，走字典配置而非发版。
5. **安全可审计**：完整用户体系 + 覆盖留痕 + 全链路审计，质量记录不可物理删除。
6. **部署简单**：可单机内网部署（无云依赖），也支持后续水平扩展。

### 1.2 设计约束
| 约束 | 说明 |
|---|---|
| 环境 | Windows 开发机；目标运行环境以内网 Linux/Windows 服务器为主 |
| 网络 | 现场可能弱网，需草稿离线容错 |
| 人员 | 使用者为产线检验人员，非 IT 用户 → UI 必须极简、容错、防误操作 |
| 数据 | 质量记录，禁止物理删除，仅逻辑作废；**覆盖必须留痕**（需求 B-9） |
| 信息完备度 | 业务方对部件号附加成分的含义**无法确认**（需求 B-8）→ 冗余字段必须可空且不参与校验 |
| 规模 | 数十名并发录入；年记录量预估 10^4~10^5 量级（低规模，无需分布式） |

### 1.3 关键技术决定（摘要）

| 编号 | 决定 | 理由 |
|---|---|---|
| D-1 | 前后端分离：Vue 3 + Vite + TypeScript（移动端 UI 库 Vant） | 移动端表单体验成熟，中文生态好 |
| D-2 | 后端 NestJS + Fastify（Node 20 / TypeScript） | 与前端同语言、装饰器式权限守卫、易维护 |
| D-3 | ORM 用 Prisma，数据库 PostgreSQL 16（开发期可用 SQLite） | 迁移可控；模型与 §4 表结构一一对应 |
| D-4 | 认证用**服务端会话 + HttpOnly Cookie**（而非 localStorage 里的 JWT） | 移动端浏览器更安全，可服务端吊销 |
| D-5 | 计数字段用**固定列**（15 个不良子项 + 3 个合计列），不良类别字典化仅用于展示与统计映射 | 表结构稳定、查询与校验最简单；类别扩展走"预留列 + 迁移" |
| D-6 | 样品信息结构化字段**冗余存储在记录主表**上（非 EAV 子表） | 查询性能与实现复杂度最优；字段少且固定 |
| D-7 | 样品信息解析走**字典驱动的 token 集合匹配 + 解析缓存**；**原始串为事实来源** | 不依赖字符串位置；可迭代；不阻断录入（需求 B-6/B-8） |
| D-8 | **无审批流**：保存即生效；表单号唯一键 + **覆盖录入** + **追加式历史快照** | 需求 B-9/B-10；覆盖需留痕以满足质量记录可追溯 |
| D-9 | 覆盖采用**整体替换 + 乐观锁**（不做字段级合并） | 语义简单、与"表单"这一整体一致；前端覆盖前先载入当前内容 |

---

## 2. 总体架构

```
┌──────────────────────────────────────────────────────────────────────┐
│  客户端                                                               │
│  ┌────────────────────┐  ┌────────────────────┐  ┌────────────────┐  │
│  │ 检验录入（移动优先）│  │ 查询 / 历史 / 统计  │  │ 管理后台        │  │
│  │ Vue3 + Vant        │  │ Vue3 + ElementPlus │  │ 用户/字典/审计  │  │
│  └─────────┬──────────┘  └─────────┬──────────┘  └───────┬────────┘  │
│            └───────────────┬───────┴────────────────────┘           │
│                   HTTP/JSON (REST) + Cookie 会话                     │
└────────────────────────────┼─────────────────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────────────────┐
│  Nginx / Caddy（TLS 终止、静态资源、反向代理、gzip）                    │
└────────────────────────────┬─────────────────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────────────────┐
│  API 服务（NestJS）                                                    │
│  ├─ AuthModule        登录/会话/密码策略/锁定                          │
│  ├─ UserModule        用户、角色、权限                                 │
│  ├─ DictModule        字典（工序/单位/样品段/参考部件号/不良类别）        │
│  ├─ SampleModule      样品信息解析器 + 解析缓存 + 参考部件号匹配          │
│  ├─ InspectionModule  记录 CRUD（含**覆盖保存**）、校验、历史快照        │
│  ├─ StatsModule       良率与不良分布统计、导出                         │
│  └─ AuditModule       审计日志（拦截器统一写入）                       │
│     共享层：ValidationService（V-1..V-9）、PermissionGuard、          │
│            FormNoService（表单号序列）、PrismaService                 │
└────────────────────────────┬─────────────────────────────────────────┘
                             │ Prisma
┌────────────────────────────▼─────────────────────────────────────────┐
│  PostgreSQL 16（单实例；每日 pg_dump 备份 + WAL 归档）                  │
│  表：users / roles / permissions / user_roles / sessions / audit_logs │
│      inspection_docs（表单号唯一） / inspection_lines                  │
│      doc_history（覆盖快照，追加写）                                    │
│      sample_parse_cache / sample_drawings / dict_items / units        │
└──────────────────────────────────────────────────────────────────────┘
```

### 2.1 部署形态
| 形态 | 适用 | 说明 |
|---|---|---|
| A. 单机内网（推荐起步） | 小规模试运行 | 一台服务器跑 Docker Compose：`db` + `api` + `web/nginx`；HTTPS 用自签或内网 CA |
| B. 开发/演示 | 本地 | SQLite + 单进程 API，`pnpm dev` 一条命令起 |
| C. 后续扩展 | 增长后 | API 无状态可多实例；DB 主从；对象存储接图片 |

---

## 3. 代码仓库结构（monorepo）

```
lens-qs/
├─ docs/
│  ├─ requirements.md          需求文档
│  └─ design.md                本文档
│  （字段与字典清单见本文档 §4；如后续独立成 `data-dictionary.md` 再补充）
├─ apps/
│  ├─ api/                     后端（NestJS）
│  │  ├─ prisma/
│  │  │  ├─ schema.prisma
│  │  │  ├─ migrations/
│  │  │  └─ seed.ts            角色/权限/字典/部件号种子数据
│  │  └─ src/
│  │     ├─ modules/{auth,user,dict,sample,inspection,stats,audit}/
│  │     ├─ common/{guards,interceptors,filters,decorators}/
│  │     └─ main.ts
│  └─ web/                     前端（Vue3 + Vite）
│     └─ src/
│        ├─ views/{entry,records,pending-sample,stats,admin}/
│        ├─ components/{form-paper-cell,defect-grid,sample-parser-panel,history-diff}/
│        ├─ stores/  api/  router/  utils/
├─ packages/
│  └─ shared/                  前后端共享：类型、枚举、校验函数（V-1..V-9）、样品解析器
│     └─ src/{types.ts, enums.ts, validate.ts, parse-sample.ts, defect-columns.ts}
├─ deploy/
│  ├─ docker-compose.yml
│  ├─ nginx.conf
│  └─ backup.sh
├─ scripts/
├─ .gitignore
├─ package.json                pnpm workspaces
└─ README.md
```

> `packages/shared` 是设计要点：**校验规则与样品解析器只写一份**，前端即时提示、后端强制复核，避免两套逻辑漂移。

---

## 4. 核心数据模型

### 4.1 ER 概览

```
users ──< user_roles >── roles ──< role_permissions >── permissions
  │
  ├──< sessions
  ├──< inspection_docs（form_no 唯一；created_by / inspected_by / last_saved_by）
  │            ├──< inspection_lines（当前明细行）
  │            │        └──< line_defects（可选纵向模型，扩展用）
  │            ├──< doc_history（每次覆盖的完整快照，追加写）
  │            └──> units, dict_items(工序)
  │
sample_parse_cache           sample_drawings（参考部件号，仅最佳努力匹配） ──> dict_items(各维度)
audit_logs
```

> 与原 v0.1 的差异：**删除 `doc_reviews`（审批流转历史）**，新增 **`doc_history`（覆盖快照）**；
> `inspection_docs` 不再有 `rootId/supersedesId/isCurrent/status` 这套版本链字段，改为 `entrySeq/entryCount` 覆盖语义。

### 4.2 表定义（Prisma 风格伪 DDL）

#### 4.2.1 用户与权限

```prisma
model User {
  id             String   @id @default(uuid())
  username       String   @unique          // 登录名
  displayName    String                    // 姓名（审计与单据显示）
  employeeNo     String?  @unique          // 工号
  unitId         String?                   // 所属单位/部门
  passwordHash   String                    // Argon2id
  mustChangePwd  Boolean  @default(true)   // 首次登录强制改密
  status         String   @default("active") // active | disabled
  failedAttempts Int      @default(0)
  lockedUntil    DateTime?
  lastLoginAt    DateTime?
  createdAt      DateTime @default(now())
  updatedAt      DateTime @updatedAt
  roles          UserRole[]
  sessions       Session[]
  @@index([unitId, status])
}

model Role {                      // inspector | admin | viewer（可扩展自定义；无 reviewer，需求 Q8）
  id          String @id @default(uuid())
  code        String @unique
  name        String
  description String?
  isSystem    Boolean @default(false)   // 系统内置角色不可删除
  permissions RolePermission[]
  users       UserRole[]
}

model Permission {                // 权限点：resource + action
  id       String @id @default(uuid())
  code     String @unique         // e.g. record:create / record:overwrite:any / user:manage
  resource String
  action   String
  name     String
}

model RolePermission { roleId String; permissionId String; @@id([roleId, permissionId]) }
model UserRole       { userId String; roleId String;       @@id([userId, roleId]) }

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

#### 4.2.3 检验记录（核心）

采用**主表（一表单号一条）+ 明细行**结构：主表承载表头与结构化样品信息，明细行承载一组计数（对应纸质表单的一行）。
**`formNo` 唯一**；重复保存走**覆盖**（§5.6），旧内容写入 `DocHistory`。

```prisma
model InspectionDoc {
  id            String   @id @default(uuid())
  formNo        String   @unique                 // M-260908-01 —— 记录唯一键
  unitId        String?                          // 单位（选填，便于统计）
  processCode   String                            // 工序：如 "一次加硬"（纸质表单无栏名，必须显式）
  processText   String?                           // 工序的原始/补充文本（保留录入原貌）

  // ── 样品信息：原始轨（事实来源，永不被覆盖以外的逻辑改写）──
  sampleRaw     String                            // 逐字保留，如 "M2 S2 ND2 SMR"

  // ── 样品信息：结构化轨（5 核心 + 冗余 + 匹配）────────────────
  sampleProject String?                           // M1 / M2 —— 必须可录入（B-7）
  sampleModel   String?                           // VID1 Rx / VID2
  sampleSpec    String?                           // S1 / S2
  sampleSize    String?                           // MD / SM / LG
  sampleSide    String?                           // L / R
  sampleClass   String?                           // 【冗余】MONOC / BINOC / MONOC_AND_BINOC，含义未确认
  sampleIsRx    Boolean?                          // 【冗余】型号含 Rx/RX
  sampleDrawingId String?                         // 【冗余】匹配到的参考部件号（仅供参考）
  sampleUnmatchedReason String?                   // raw_only | no_drawing | partial
  sampleParsedBy  String   @default("auto")       // auto | manual | cache
  sampleConfidence Float   @default(0)
  sampleVerified  Boolean  @default(false)        // 人工确认过
  needsReview     Boolean  @default(false)        // 解析失败/不匹配 → 进入待核对列表（不阻断保存）
  unknownTokens   Json?                           // 未能识别的 token（便于字典补录）

  // ── 表头人员与时间（送检人/操机员/交接员本期不实现，见需求 B-9）──
  inspectedBy     String?                          // 检验人（默认当前用户）
  inspectedAt     DateTime                         // 检验时间

  // ── 覆盖录入 ───────────────────────────────────
  entrySeq        Int      @default(1)             // 当前内容的录入序号（第 N 次录入）
  entryCount      Int      @default(1)             // 累计录入次数（= 历史条数 + 1）
  state           String   @default("ACTIVE")      // ACTIVE | VOID
  lastSavedBy     String                           // 当前内容由谁保存
  lastSavedAt     DateTime @default(now())

  // ── 汇总（由明细行在服务端重算落库，供列表/统计快速读取）──
  totalQty        Int      @default(0)
  qualifiedQty    Int      @default(0)
  defectQty       Int      @default(0)
  passRate        Decimal? @db.Decimal(6, 3)       // 0~100
  manualRateNote  String?                          // 纸质表单下方手写率值原样留档（无业务含义，B-11）

  createdBy       String                           // 首版创建者（永不改变）
  createdAt       DateTime @default(now())
  voidedAt        DateTime?
  voidReason      String?

  lines           InspectionLine[]
  history         DocHistory[]

  @@index([state, inspectedAt])
  @@index([processCode, inspectedAt])
  @@index([inspectedBy, inspectedAt])
  @@index([sampleSpec, sampleSize, sampleSide, sampleModel])   // 层级检索
  @@index([sampleProject])
  @@index([needsReview, state])
  @@index([unitId, inspectedAt])
  @@index([lastSavedAt])
}

model InspectionLine {                             // 一行 = 纸质表单的一行计数（当前内容）
  id        String @id @default(uuid())
  docId     String
  seq       Int                                    // 行序，从 1 开始

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

  remark    String?
  createdAt DateTime @default(now())
  updatedAt DateTime @updatedAt

  doc       InspectionDoc @relation(fields: [docId], references: [id], onDelete: Cascade)
  defects   LineDefect[]
  @@unique([docId, seq])
  @@index([docId])
}

model DocHistory {                                 // 覆盖快照：每次覆盖前保存旧内容，追加写、不可改不可删
  id          String   @id @default(uuid())
  docId       String
  entrySeq    Int                                  // 该快照对应的录入序号（被覆盖的那一版）
  snapshot    Json                                 // 主表全部业务字段（含 sampleRaw / 结构化字段 / 汇总）
  linesSnapshot Json                               // 旧明细行数组
  savedBy     String
  savedAt     DateTime                             // 该版保存时间
  overwrittenBy String                             // 谁覆盖了它
  overwrittenAt DateTime
  reason      String?                              // 覆盖原因（可选填写）
  @@index([docId, entrySeq])
  @@index([savedBy, savedAt])
}
```

**关于"不良类别可扩展"的说明（D-5）**
- 本期五类不良（点伤/划伤/脏污/加硬点/垃圾点）以**固定列**实现：校验、统计、打印最直观，性能最好。
- 若业务方预期不良类别会频繁新增，则切换为**纵向模型**：
  `inspection_line_defects(line_id, defect_code, surface, qty)` + `defect_code` 走字典。
  切换成本评估：模型变更 + 统计 SQL 改写，约 1~2 人日；建议**在 M1 结束前定稿**，避免上线后迁移。
- 无论哪种模型，前端都从字典读取"类别 × 表面"的列定义生成表格，新增类别无需改前端代码。

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

#### 4.2.4 审计

> 原 `DocReview`（审批流转历史）**已随审批流程一并删除**（需求 Q8：无需审核）。覆盖留痕由 `DocHistory`（业务可读快照）+ `AuditLog`（系统级日志）双重承担。

```prisma
model AuditLog {
  id         BigInt   @id @default(autoincrement())
  actorId    String?
  actorName  String?
  action     String                                // login | logout | create | overwrite | void | export | ...
  entity     String                                // inspection_doc | user | dict_item | ...
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

### 4.3 数据流：一次录入的完整链路

```
① 打开录入页
   └─ 拉取字典（工序/单位/样品段/参考部件号）、上次记录模板（复制用）

② 输入表单号
   ├─ 新表单号 → 空白表单，entrySeq = 1
   └─ 已有表单号 → 载入**当前内容**（补填/覆盖模式），提示"当前为第 N 次录入"

③ 填「样品信息」原始串  →  前端调用解析器（shared/parse-sample.ts，本地即时）
   ├─ 写 sample_parse_cache（命中则秒回，未命中则记录新映射）
   └─ 结构化结果可人工修正；**任何情况都不改写 sample_raw**

④ 填工序、检验人、检验时间、计数明细
   └─ shared/validate.ts 实时校验（V-1 阻断 / V-2 警告 / V-9 提示）→ 合计不符即时标红（E-3）

⑤ 本地草稿
   └─ 录入中每 5s 或字段变更写 IndexedDB（弱网/误退容错，E-5）

⑥ 保存（保存即生效，无审批）
   └─ 服务端事务：
      1) 按 formNo 查找现有记录（SELECT ... FOR UPDATE / 唯一索引）
      2) 已存在且非本流程内新建 → **先把旧主表字段 + 旧明细整体写入 DocHistory**
      3) 覆盖主表业务字段；明细行整体替换（删旧插新，同事务）
      4) 重新执行 V-1..V-9 → 重算汇总（totalQty/qualifiedQty/defectQty/passRate）
      5) entrySeq = 旧 entrySeq + 1；entryCount = 旧 entryCount + 1；lastSavedBy/lastSavedAt 更新
      6) needsReview 依解析结果置位（不阻断）
      7) 写 AuditLog(action=create|overwrite, before/after)
   └─ 乐观锁：若客户端携带的 entrySeq ≠ 服务端当前值 → 返回 409 + 差异，由用户决定是否继续

⑦ 作废（可选）
   └─ state = VOID + voidReason + AuditLog；数据与历史全部保留
```

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

### 5.2 样品信息解析器（`packages/shared/parse-sample.ts`）

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

### 5.3 校验（单一实现，双端复用）

```ts
// packages/shared/validate.ts   规则编号与需求文档 §4.1.3 一一对应
validateLine(line): Issue[]        // V-1..V-5（行级）
validateDoc(doc, lines): Issue[]   // V-4, V-6, V-7, V-9 + 汇总一致性
parseSample(raw): ParsedSample     // S-1..S-8
```

| 规则 | 判级 | 实现位置 |
|---|---|---|
| V-1 合格+不良=总数 | **error**（阻断保存） | 前端提示 + 服务端事务内强校验 |
| V-2 Σ15子项 = 不良品 | **warning**（不阻断）—— 业务方已确认存在重复计数（B-12），登记 `【待定】` | 前端提示 + 服务端只记标记 |
| V-3 子项边界（各类别三子项之和 ≤ 不良品；子项 ≤ 总数） | error | 同上 |
| V-4 非负整数、总数>0 | error | DTO 校验（class-validator） |
| V-5 良率派生 | 计算 | 服务端重算，前端仅展示 |
| V-6 检验时间不得晚于当前时间 | error | 服务端 |
| V-7 `formNo` 唯一（一表单号一记录） | error | DB 唯一索引 + 友好提示 |
| V-8 样品信息与参考清单不匹配 | **warning** + `needsReview=true`，**绝不阻断** | 服务端 |
| V-9 结构化必填项（`sampleProject`）为空 | warning（可保存为"待补全"）【待确认是否升 error，见需求 V-9】 | 前端 + 服务端 |

**设计决定**：服务端**永远重算** `totalQty/qualifiedQty/defectQty/passRate`，忽略客户端上报的汇总值。
**设计决定**：只有 V-1 / V-3 / V-4 / V-6 / V-7 具有阻断力；其余规则只产生提示与标记，保证"现场永远能存下数据"。

### 5.4 表单号生成（并发安全）

```
格式: {prefix}-{YYMMDD}-{seq:2}       示例 M-260908-01
实现: 表 form_no_sequence(prefix, ymd, lastSeq) + 事务内 SELECT ... FOR UPDATE / UPSERT 自增
      或 DB 序列 + 唯一索引兜底重试
规则: 日期取 inspectedAt 的日期（Q7 待确认）
      formNo 由系统生成时保证唯一；用户手工录入时校验唯一（V-7）
      **formNo 也是记录唯一键**：手工录入已存在的 formNo → 进入覆盖流程，而非新建（§5.6）
```

### 5.5 认证与授权

**认证**
- 登录：`POST /api/auth/login` → 校验 Argon2id → 创建 Session（随机 256bit token，仅存哈希）→ `Set-Cookie: sid=...; HttpOnly; Secure; SameSite=Lax; Path=/`
- 会话：默认有效期 12h，空闲 2h 失效（`lastSeenAt` 滑动续期）；管理员/本人可吊销
- 失败锁定：连续 5 次失败 → `lockedUntil = now + 10min`（U-4）
- 首次登录/管理员重置 → `mustChangePwd=true`，仅允许访问改密接口（U-3）
- 【待确认 Q9】预留 `AuthProvider` 接口，便于后续接企业微信/LDAP/OIDC

**授权**（无审批动作，权限只控制录入范围与可见范围）
- 权限点命名：`{resource}:{action}`
  `record:create` `record:read:self` `record:read:all` `record:overwrite:self` `record:overwrite:any`
  `record:void` `record:export` `record:history:read`
  `dict:manage` `user:manage` `audit:read` `stats:read:self` `stats:read:all`
- 守卫链：`AuthGuard`（会话有效）→ `PermissionGuard`（`@RequirePermission('record:overwrite:any')`）
- 行级可见性在 **Service 层**注入过滤条件，不依赖前端：
  - 无 `record:read:all` → `WHERE inspectedBy = :me OR createdBy = :me`
  - 有 `record:read:all` + `unitId` 范围 → 附加 `unitId IN (...)`
- 覆盖权限：默认要求 `record:overwrite:self`（本人记录）或 `record:overwrite:any`（他人记录，默认仅 admin）
- 所有写操作从会话取操作人写入 `lastSavedBy`，**不接受客户端传入的身份字段**

### 5.6 覆盖录入与历史（原"状态机与修订"）

> **需求依据**：B-9（同一表单号可多次录入、以最新为准）、B-10（无审核流程）；需求文档 §5。

**核心语义**

| 项 | 规则 |
|---|---|
| 记录唯一键 | `formNo` |
| 保存语义 | **UPSERT**：不存在则创建（`entrySeq=1`）；已存在则**整体覆盖**并 `entrySeq+1` |
| 覆盖内容 | 主表业务字段整体替换 + 明细行整体替换（同事务内删旧插新） |
| 不可变字段 | `createdBy`、`createdAt`、`id` |
| 历史 | 覆盖前把旧内容（主表业务字段 + 明细数组）写入 `DocHistory`，**追加写、不可改不可删** |
| 并发 | 乐观锁：请求携带客户端已知的 `entrySeq`，与服务端不一致 → `409` + 返回差异，由用户选择"覆盖"或"放弃" |
| 作废 | `state='VOID'` + `voidReason`；数据与历史保留；统计默认排除 |

**服务端事务伪代码**

```
saveDoc(input, actor):
  tx:
    doc = SELECT * FROM inspection_docs WHERE form_no = :formNo FOR UPDATE
    if doc == null:
        validateCreate(input)                      # V-4/V-6/V-7
        doc = INSERT(...)                          # entrySeq=1, entryCount=1
        action = 'create'
    else:
        if not canOverwrite(actor, doc): 403
        if input.entrySeq != doc.entrySeq: 409(conflict, diff)   # 已被他人覆盖
        validate(input)                            # V-1/V-3/V-4/V-6 阻断项
        INSERT INTO doc_history(doc_id, entry_seq=doc.entrySeq,
              snapshot=rowToJson(doc), lines_snapshot=loadLines(doc),
              saved_by=doc.lastSavedBy, saved_at=doc.lastSavedAt,
              overwritten_by=actor.id, overwritten_at=now(), reason=input.reason)
        DELETE FROM inspection_lines WHERE doc_id = doc.id
        UPDATE inspection_docs SET <业务字段>=..., entry_seq=doc.entrySeq+1,
              entry_count=doc.entryCount+1, last_saved_by=actor.id, last_saved_at=now()
        action = 'overwrite'
    INSERT lines
    recomputeTotals(doc)                           # 服务端重算，忽略客户端汇总
    setParseFlags(doc)                             # needsReview / unmatchedReason，不阻断
    INSERT audit_logs(action, entity='inspection_doc', entity_id=doc.id,
                       before, after, actor, ip, ua)
  return doc
```

**历史查询**
- 列表默认只展示**当前内容**（`inspection_docs` 一行）。
- 详情页"历史"页签：`DocHistory` 按 `entrySeq` 倒序，展示每一版的完整内容。
- "差异视图"：`packages/shared` 提供 `diffDoc(v1, v2)`，返回字段级 + 明细行级的变更清单（前端渲染为表格）。

### 5.7 审计日志

- 写入方式：全局 `AuditInterceptor`（登录、导出、用户/字典变更）+ 领域服务在**事务内**写 `AuditLog`（创建/覆盖/作废）。
- 内容：操作人（id + 当时姓名）、动作、对象、变更前后 JSON、IP、UA、时间。
- 覆盖时的 `before` 取覆盖前的主表快照（与 `DocHistory` 内容一致，但 `AuditLog` 面向系统审计、`DocHistory` 面向业务回看）。
- 脱敏：`passwordHash`、token 等永不入日志。
- 只读性：应用账号对 `audit_logs` 只有 INSERT/SELECT 权限（无 UPDATE/DELETE），DB 层保证不可篡改。
- 保留期：默认 3 年（Q12）。

### 5.8 离线与弱网（E-5）

| 机制 | 说明 |
|---|---|
| 草稿本地持久化 | 录入中每 5s 或字段变更后写 IndexedDB；保存成功后清除 |
| 保存重试队列 | 保存失败入本地队列，恢复网络自动重试；幂等键 `clientRequestId` 防重复保存 |
| 字典缓存 | 字典带 `etag/version`，本地缓存 24h，离线可用 |
| 冲突提示 | 保存时若 `entrySeq` 不一致（记录已被他人覆盖），提示差异并由用户选择是否覆盖 |

### 5.9 前端页面结构

| 路由 | 页面 | 角色 |
|---|---|---|
| `/login` | 登录（含首次改密） | 全部 |
| `/entry` | **检验录入**（与纸质表单同构：表头 + 计数网格 + 实时良率）；支持按表单号载入已有内容 | inspector/admin |
| `/records` | 记录列表（筛选/分页/导出），默认展示最新内容 | 全部（范围受权限） |
| `/records/:formNo` | 记录详情（原始轨/结构化轨对照）+ **历史页签（每版内容 + 差异）** | 全部 |
| `/pending-sample` | 样品信息待核对清单（字典补录入口） | admin |
| `/stats` | 统计看板（良率趋势、不良分布、工作量） | admin/viewer |
| `/admin/users` | 用户/角色/权限 | admin |
| `/admin/dict` | 字典（工序/单位/样品段/参考部件号） | admin |
| `/admin/audit` | 审计日志检索 | admin |

> **已删除**：`/review`（待审核工作台）——需求 Q8 确认无审核流程。

**录入页交互要点**：
- 计数网格列顺序与纸质表单完全一致（点伤(凹/凸/双) → 划伤 → 脏污 → 加硬点 → 垃圾点）；`inputmode="numeric"`；Tab/回车按列顺序跳转。
- 实时展示"合格+不良 vs 总数"差分提示；V-2 不一致时给出黄色警告（可继续保存）。
- 良率只读大字显示；样品信息面板可折叠，展示**原始串（只读，最醒目）** + 自动拆解结果 + 手动纠正入口。
- 顶部表单号输入框：输入已存在的表单号 → 自动载入当前内容并提示"第 N 次录入，保存将覆盖"。

---

## 6. 接口设计（REST 概要）

统一前缀 `/api`，响应 `{ code, message, data }`；错误使用 HTTP 语义码（400/401/403/404/409/422）。

### 6.1 认证与用户
| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| POST | `/auth/login` | 登录（返回用户信息 + 权限点集合） | 公开 |
| POST | `/auth/logout` | 登出 | 登录 |
| GET | `/auth/me` | 当前用户与权限 | 登录 |
| POST | `/auth/change-password` | 修改密码（含首次改密） | 登录 |
| GET/POST/PATCH | `/users`、`/users/:id` | 用户列表/创建/修改（含启停、重置密码） | `user:manage` |
| GET/POST/PATCH | `/roles`、`/roles/:id` | 角色与权限点配置 | `user:manage` |

### 6.2 字典
| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| GET | `/dict/items?category=` | 按类别取字典（前端缓存） | 登录 |
| POST/PATCH | `/dict/items`、`/dict/items/:id` | 新增/修改/停用 | `dict:manage` |
| GET | `/dict/drawings?spec=&size=&side=&model=` | **参考**部件号查询（仅用于辅助选择） | 登录 |
| POST | `/dict/drawings/import` | 批量导入参考部件号（Excel） | `dict:manage` |

### 6.3 样品信息
| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| POST | `/sample/parse` | 解析原始串 → 结构化字段 + 参考匹配 + 置信度 + `unmatchedReason`（**纯辅助，不写库**） | 登录 |
| GET | `/sample/pending` | 待核对清单（`needsReview=true AND state=ACTIVE`） | `dict:manage` |
| POST | `/sample/:docId/confirm` | 人工确认/修正结构化字段（**不改写 `sampleRaw`**） | 该记录写权限 |

### 6.4 检验记录
| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| GET | `/inspections` | 列表（筛选：formNo/日期区间/工序/样品各维度/检验人/单位/良率区间/含不良/needsReview）；默认返回最新内容 | `record:read:*` |
| GET | `/inspections/by-form-no/:formNo` | 按表单号取当前内容（录入页载入用） | `record:read:*` |
| POST | `/inspections` | **保存（UPSERT）**：表单号不存在则创建，存在则覆盖；返回 `{ entrySeq, entryCount, action: create\|overwrite }` | `record:create` / `record:overwrite:*` |
| GET | `/inspections/:id` | 详情（含明细行、解析标记） | 范围校验 |
| GET | `/inspections/:id/history` | 历史快照列表（每版内容） | `record:history:read` |
| GET | `/inspections/:id/history/diff?from=&to=` | 两版之间的字段级/明细级差异 | `record:history:read` |
| POST | `/inspections/:id/void` | 作废（逻辑）+ 原因 | `record:void` |
| GET | `/inspections/:id/print` | 表单样式 PDF | 范围校验 |

> **已删除的接口**：`PATCH /inspections/:id`（改为统一 `POST` 覆盖保存）、`/submit`、`/review`、`/revise`。
> **并发约定**：`POST /inspections` 请求体必须携带 `entrySeq`（客户端已知的当前版本号）；服务端不一致时返回 `409` + `{ serverDoc, diff }`，客户端可带 `force: true` 重试。

### 6.5 统计与导出
| 方法 | 路径 | 说明 | 权限 |
|---|---|---|---|
| GET | `/stats/summary?from=&to=&groupBy=` | 良率与计数汇总（基于当前内容） | `stats:read:*` |
| GET | `/stats/defects?from=&to=&groupBy=sampleSize` | 不良分类分布（**标注"子项合计可大于不良品数"**，见需求 Q5） | `stats:read:*` |
| GET | `/stats/workload?from=&to=` | 按检验员工作量 | `stats:read:all` |
| GET | `/stats/overwrite-activity?from=&to=` | 覆盖录入频次（数据质量监控） | `audit:read` |
| GET | `/inspections/export` | 导出 Excel（按当前筛选，默认最新内容） | `record:export`（写审计） |
| GET | `/audit-logs` | 审计检索 | `audit:read` |

---

## 7. 性能与索引

| 场景 | 措施 |
|---|---|
| 列表筛选（表单号/日期/工序/样品维度/检验人） | 见 §4.2.3 复合索引；日期区间 + state 组合索引 |
| 层级检索（S2+SM+L） | `(sampleSpec, sampleSize, sampleSide, sampleModel)` 复合索引；避免全表扫描 |
| 按表单号定位（录入页载入） | `formNo` 唯一索引，走主键级查找 |
| 统计 | 汇总字段落库于主表（`totalQty/qualifiedQty/defectQty`），统计只走主表（`inspection_docs` 一行 = 一表单号），**不需要 JOIN 明细行**；这是覆盖模型带来的额外收益 |
| 覆盖写入成本 | 每表单号的历史条数 = 录入次数（通常个位数），`DocHistory.snapshot` 为小 JSON，写入开销可忽略；单事务内完成"写快照 + 替换明细 + 更新主表" |
| 导出大结果集 | 流式写入（游标 + 分批），限制单次导出上限（如 5 万行） |
| 前端 | 字典与参考部件号列表本地缓存；列表虚拟滚动（>200 行） |
| 目标 | 保存 P95 < 500ms；1000 条条件查询 P95 < 2s |

---

## 8. 安全设计

| 项 | 措施 |
|---|---|
| 密码 | Argon2id（memory 64MB, iterations 3, parallelism 1）；禁用弱密码；不得明文落库与落日志 |
| 会话 | 随机 256bit；仅存哈希；HttpOnly + Secure + SameSite=Lax；支持全端吊销 |
| 越权 | 服务端逐接口鉴权 + Service 层行级过滤；不信任任何客户端身份字段 |
| 覆盖越权 | 覆盖前校验 `record:overwrite:self`（本人记录）/ `record:overwrite:any`；他人记录默认拒绝 |
| 输入 | DTO 白名单校验（class-validator）+ Prisma 参数化查询（无拼接 SQL） |
| 限流 | 登录接口按 IP + 账号限流；导出接口按用户限流 |
| 传输 | 强制 HTTPS（反代跳转）；内网自签证书需导入客户端信任 |
| 审计 | `audit_logs` 应用账号无 UPDATE/DELETE 权限；`doc_history` 同样只允许 INSERT/SELECT |
| 备份 | 每日 `pg_dump` + WAL 归档；备份文件加密；恢复演练纳入验收 |
| 隐私 | 仅采集姓名/工号等最小必要信息；送检人/操机员/交接员不采集（B-9） |

---

## 9. 测试策略

| 层级 | 范围 | 要点 |
|---|---|---|
| 单元测试 | `packages/shared`：校验 V-1..V-9、样品解析器（含 `M2 S2 ND2 SMR`、`S1 - MD - R - VID2 - MONOC & BINOC`、无法识别的 `ND2` 等用例）、良率计算、`diffDoc` | 覆盖率 ≥ 90%（核心逻辑） |
| 服务测试 | **覆盖语义**（同表单号 1/2/3 次保存后当前内容与历史条数正确）、乐观锁 409、表单号生成唯一、权限过滤、作废 | 覆盖必须产生 `DocHistory` + `AuditLog` 各一条 |
| 接口测试 | 每个端点的权限矩阵（对齐需求 §7 逐格验证） | 越权必须 403 |
| E2E | 登录 → 录入 → 校验拦截（V-1 阻断 / V-2 仅警告）→ 保存 → 同表单号再次录入覆盖 → 查看历史与差异 → 导出 | Playwright；移动端视口 |
| 性能 | 1000/10000 条数据下的列表、统计、含多版历史的详情页 | 对照 §7 目标 |
| 验收 | 对齐需求文档 §8 验收标准（重点第 5 条：覆盖与历史） | |

---

## 10. 里程碑与实施计划（初步）

| 阶段 | 交付 | 关键设计落地 |
|---|---|---|
| M1 骨架 | monorepo、Prisma schema、登录与会话、用户/角色 CRUD、字典管理 | §4.2.1、§4.2.2、§5.5 |
| M2 核心录入 | 录入页（同构表单）、**UPSERT 覆盖保存 + 历史快照**、校验 V-1..V-9、草稿离线、审计 | §4.2.3、§4.3、§5.3、§5.6、§5.7 |
| M3 样品信息 | 解析器 + 缓存 + 参考部件号匹配 + 待核对清单 + 层级检索 | §4.2.2、§5.1、§5.2 |
| M4 查询与导出 | 列表/详情/历史差异视图、导出与打印 | §6.4、§6.5 |
| M5 统计与上线 | 看板、备份与部署脚本、验收测试 | §7、§8、§9 |

---

## 11. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 样品信息含义无法确认（Q2，业务方也不掌握） | 冗余字段可能永远为空 | 策略已改为"**原始输入优先**"：`sampleRaw` 保证数据价值，冗余字段可空、不校验、不作验收条件（B-6/B-8）；新维度只需加字典 |
| 手写串不规范（如 `ND2`） | 解析命中率低 | 解析缓存 + 人工修正 + `unknownTokens` 驱动字典补录；**不阻塞保存** |
| **覆盖录入可能造成数据丢失/误覆盖** | 现场把上一个阶段的内容覆盖掉 | 覆盖前二次确认 + 展示当前内容；覆盖前完整快照进 `DocHistory`；支持历史与差异查看；`/stats/overwrite-activity` 监控异常覆盖频次 |
| **"只填一部分"的语义未定（Q15）** | 若允许多次部分录入，V-1 强校验会挡住合法录入 | 已列出两方案（见需求 Q15），**M2 开工前必须确认**；预留 `is_incomplete` 与"待补全"清单 |
| 并发覆盖同一表单号 | 一方内容被另一方覆盖 | 乐观锁（`entrySeq`）+ 409 + 差异提示 |
| 不良类别可能扩展（D-5） | 表结构迁移 | M1 结束前定稿；预留 `LineDefect` 纵向模型 |
| 不良重复计数未解决（Q5） | 统计口径失真 | V-2 降级为警告；统计页明确标注；登记 `【待定】` |
| 现场弱网 | 保存失败丢数据 | 本地草稿 + 幂等重试（§5.8） |
| 质量数据可信度 | 无水印/无签名的数字记录易被质疑 | 覆盖历史不可删 + 审计日志 DB 层只增；必要时后续加"记录指纹（内容 hash）" |
| 参考部件号清单与实际填写脱节（B-6） | 结构化字段长期为空 | 允许"仅原始串"路径；用 `needsReview` 清单统计缺口，用实际录入串反哺字典 |

---

## 附录 A：与需求文档的对应关系

| 需求条目 | 设计落地位置 |
|---|---|
| B-2/B-3 工序独立项目 | `InspectionDoc.processCode/processText`；字典 category=`process`；录入页显式字段 |
| B-4 子格为镜片数量 | `InspectionLine` 15 个整数字段 + 校验 V-2/V-3 |
| B-6 原始输入为准 | `sampleRaw` 为事实来源；解析与人工修正均不改写；V-8 降级为 warning（§5.1、§5.2） |
| B-7 项目 M1/M2 必须纳入 | `InspectionDoc.sampleProject` + 字典 `sample_project` + 录入页必现字段（V-9 提示） |
| B-8 冗余字段可有可无 | `sampleClass` / `sampleIsRx` / `sampleDrawingId` 可空、不校验、不作验收（§5.1） |
| B-9 同表单号多次录入、以最新为准 | `formNo` 唯一键 + UPSERT 覆盖 + `DocHistory` 快照（§4.2.3、§5.6） |
| B-10 无审核流程 | 删除审批状态机与 `/review` 页面、`DocReview` 表（§5.6、§5.9） |
| B-11 手写率值不处理 | `manualRateNote` 仅原样留档，不参与计算（§4.2.3） |
| B-12 不良重复计数 | V-2 降级为 warning；统计页标注（§5.3） |
| §4.2 双轨存储 | `sampleRaw` + `sample*` 字段 + `sample_parse_cache`（§5.1、§5.2） |
| §3 用户体系 | `User/Role/Permission/Session` + `AuthGuard/PermissionGuard`（§4.2.1、§5.5） |
| U-8/U-12 审计与覆盖留痕 | `AuditLog` 拦截器 + `DocHistory`（§5.6、§5.7） |
| E-5 离线草稿 | §5.8 |
| Q-1 层级检索 | 复合索引（§4.2.3、§7） |

## 附录 B：变更记录

| 版本 | 日期 | 变更 | 作者 |
|---|---|---|---|
| v0.1 | 初始 | 依据需求文档 v0.1 编写初步设计 | — |
| v0.2 | 第二轮确认后 | ① 删除审批状态机与 `DocReview`，改为 UPSERT 覆盖 + `DocHistory` 快照（§4.2.3、§4.3、§5.6）；② `formNo` 升为记录唯一键，新增并发乐观锁与 409 约定；③ 样品信息改为"原始输入优先"，冗余字段不校验、不作验收（§5.1、§5.2）；④ 表头删除送检人/操机员/交接员；⑤ 校验 V-2 降级为 warning、新增 V-9、明确只有 V-1/V-3/V-4/V-6/V-7 具阻断力；⑥ 权限点与 API 随之调整（删除 submit/review/revise，新增 history/diff）；⑦ 前端路由删除 `/review`，新增历史差异视图；⑧ 里程碑、测试策略、风险表更新 | — |

# 第一轮重建验收记录 G1

日期：2026-11-05。依据：[重建计划](rebuild-plan.md) A-01～A-07、[需求](requirements.md)、[系统设计](design.md)、[公共设计](common-design.md)、[字段](fields.md)、[API](api.md)。既有文档先提交为 `5a39645`，随后在 `codex/rebuild-round-one` 实施。实现提交：`aae3c34`。本文记录第一轮基础验收，不代表第二轮业务功能完成。

结论：G1 基础验收通过，可进入 B-01。当前版本仅用于隔离开发，不用于业务上线。

## 交付与验收边界

| 工作包 | 交付及可复验入口 |
|---|---|
| A-01 | AC 映射及旧测试处置如下；migrate/status 只读盘点、结构清单、数量、冲突、无凭据目标指纹；一致备份和恢复摘要核对 |
| A-02 | ledger/model.go 的显式 Content/Counts/Line/QuantityChange/Derived，唯一 Merge/Validate；完整 105 格、空值/零、单位/日期/整数/LOSS、五项缺项、warning、表头/明细差异、单据可比性和缺陷未知量 |
| A-03 | 新增两库 0007；单一非空当前指针、延迟循环外键及版本归属、不可变保存版本、筛选投影、占用、schema/template、创建及保存证据；已知 0006 重建与空库全链结果同构 |
| A-05 | 通用 WriteTx 写闸门、当前账户复查、10 秒事务/5 秒锁等待、成功幂等、事务审计、逻辑时钟、完整末事件；ReadView 一致读及固定版本集合；严格输入和凭据/来源协议 |
| A-04 | department 三态、密码策略/旧哈希验证、统一登录失败401、最后管理员、事务内撤销会话/幂等；只读 PIECE/片、规范工序类别、对象/null meta、缺项 seed、字段模板读取；人工缓存保留且待复核 |
| A-06 | API/容器/dev 启动不迁移/seed/重置/创建账号；显式初始化、盘点、备份、验证回执、指纹绑定重建/重置；迁移锁、名称/文件摘要检查、保全摘要核对和维护回执 |
| A-07 | 双库基础/集成/race、vet、build、旧库副本保全/恢复/失败回滚；旧路径404和待接回清单；CI 改为双库全包运行 |

当前基础没有发布、草稿、双内容指针、独立数量声明、日期原文或新增计量单位的运行协议。旧词仅在历史迁移、0006 输入识别和拒绝测试中保留。旧业务实现已从源码和路由退出，由 Git 基线保存；没有复制 legacy 服务并继续注册。

## 实测证据

以下均在隔离测试文件或生成的 PostgreSQL schema 中运行。原开发库未执行迁移、重置、seed、账号更新或服务切换。

| 检查 | 结果与证据 |
|---|---|
| SQLite 全包及 race | `go test -race ./... -count=1` 通过；包含规则、存储、共享 HTTP、账号维护和 seed |
| PostgreSQL 17 全包及 race | 显式临时测试 DSN，`go test -race ./... -count=1` 通过；每个数据库用例单独生成/清理 schema |
| 迁移/配置保全 | TestG1EmptyAndLegacyPreservation、TestG1EmptyAndLegacyHaveIdenticalSchema；账号哈希/ID、部门、停用人工工序/别名、组织目录、人工确认、非业务旧审计保留 |
| 未知输入/失败回滚 | TestG1PreflightAndFailedRebuildRollback：部门悬空、工序冲突拒绝；在配置更新处注入数据库触发器异常，先前删除/DDL/缓存动作全部回滚 |
| 迁移核验/只读状态 | TestG1MigrationIntegrityAndReadOnlyStatus：空库盘点不建表、文件摘要篡改拒绝；旧文件首次摘要标记为 legacy-name-and-shape-baseline |
| SQLite 一致备份/恢复 | TestG1SQLiteBackupRestore：重新打开备份核对数据摘要；备份副本可重建，源库指纹不变 |
| PostgreSQL 一致备份/恢复 | TestG1PostgresBackupRestore：真实 pg_dump custom、createdb、pg_restore，恢复库摘要与源库相同；恢复库重建成功且源库不变 |
| 贯穿样例 | TestG1IndependentGroupsHeaderAndLoss、TestG1BindCloseCorrectCancelAndReuse、TestG1AtomicRetryBoundaryClockAndRollback；两库均通过 |
| 并发/批量/授权 | TestG1ConcurrentOccupancyAndBatchRollback、TestG1IdentityAndSlotMatrix、TestG1AuthorizationBeforeRetryAndVersionConflict；竞争仅一方成功、跨类型/旧版本/停用后回放拒绝 |
| 事务证据与账号维护 | TestG1SnapshotEvidenceAndMaintenanceReset、TestG1LastAdministratorAndMaintenanceAtomicity；改名不改证据，取消不造保存版本，审计故障连同改类型/改密/撤销/幂等清理回滚 |
| 共享接口/路由退出 | TestG1SharedHTTPContracts：三态null、密码、来源、凭据冲突、重复/未知输入、字段/模板、旧路径与未挂载接口404；GET旧缓存路径不写入 |
| seed | TestG1SeedOnlyFillsMissing：重复 seed 不覆盖人工标签/别名/停用或已有图号 |
| Linux容器 | Go 1.26 Linux 容器内 SQLite/PostgreSQL 全包测试通过；镜像构建成功；隔离 SQLite 卷连续启动/优雅退出两次，前后数据摘要相同 |
| 工程检查 | `go vet ./...`、`go build ./...` 通过；本机 Go 1.27.1 windows/amd64，go.mod 1.26.0 |

现存开发库只读盘点：`backend/data/lens.db`，版本0006，账号1、字典21、参考图号18、测试生产批/组/单各1、会话4、领域幂等4、人工/自动缓存0。未发现配置转换冲突。一致备份位于 Git/Docker 排除的 `backend/backups/g1-20261105/`，含私人数据，不提交仓库。

在该库的隔离副本预演：重建至0007，账号仍1、参考图号仍18；3组完全相同的 process/inspection_operation 配置合并，规范工序映射后字典18，旧ID映射保留；PIECE单位1；测试批/组/单、会话和领域幂等均0。事务内维护回执证明账号/配置规范化摘要一致，源库重建前后指纹相同。备份副本与空库链、PostgreSQL 旧结构夹具另有自动化验证。

现存有效配置的 CORS_ORIGINS 含通配符，新版按规格拒绝；预演进程临时使用明确本地来源覆盖，未修改已有配置文件。正式接入时须确定可信来源，不能沿用通配来源。

本次没有远端推送或触发 GitHub Actions；CI 的 Linux 双库全包配置已更新，远端运行状态不作为本机通过证据。性能、依赖故障 healthz、正式部署/恢复指标及完整 API 验收属于 G2。

## AC-01～26 映射

“基础通过”仅指下列规则/仓储用例；这些业务 HTTP 均未接回，不能据此宣布对应 AC 的完整产品验收通过。

| AC | 第一轮基础证据 | 第二轮仍需完成 |
|---|---|---|
| 01 | IdentityAndSlotMatrix：trim-only身份与重复冲突；IndependentGroups：同批两类组 | B-01/02 批/组接口与筛选 |
| 02、03 | IndependentGroups：后序组直接录入，不存在前序来源/余额依赖 | B-01 多工序完整 API 案例 |
| 04 | 保存按单ID，新单独立ID，版本推进 | B-01/02 新建/补填详情与列表 |
| 05 | ContentRules、BindCloseCorrectCancelAndReuse：三态/数组替换、连续保存版本 | B-01/02 内容历史接口 |
| 06 | HeaderAndLoss、AtomicRetryBoundary：固定旧版本集合，后续更正不改旧内容 | B-03/04 当前统计及历史报告 |
| 07 | AtomicRetryBoundary、AuthorizationBeforeRetry：失败/版本冲突无半写 | B-01 HTTP冲突与响应 |
| 08 | BindCloseCorrectCancelAndReuse：取消释放、不追加内容版本 | B-01/03/04 取消详情与报告 |
| 09 | ConcurrentOccupancyAndBatchRollback、AtomicRetryBoundary | B-01 批量HTTP与弱网响应 |
| 10 | CountsComparabilityAndOverflow：未知/零、空集合、单张不可比、溢出 | B-03 统计全端点 |
| 11 | ContentRules、DefectUnknownIsPerSubmission：105格、计次与单据未知数 | B-03 缺陷统计响应 |
| 12 | 原文逐次保留，Mapping可空DTO，人工确认迁移保全 | B-05 完整解析/候选/确认/缓存协议 |
| 13 | 纸面检验人独立字段；保存者来自事务账户 | B-02/03 纸面人/账号工作量 |
| 14 | ContentRules：LOSS原因可单独保存、不扣表头，SAMPLING硬拒绝 | B-01/03 变化接口与汇总 |
| 15、16 | ContentRules：真实公历、精度互斥、UTC及上海归日、未知无兜底 | B-02/04 日期筛选/报告 |
| 17 | AtomicRetryBoundaryClock：同秒末事件、回退逻辑时钟、中间事件拒绝 | B-04 历史回放HTTP |
| 18 | ReadView和固定版本集合基础 | B-02/04 完整筛选分页与全范围小计，未实现 |
| 19 | IdentityAndSlotMatrix、AuthorizationBeforeRetry、HeaderAndLoss | B-01/03 两类HTTP及ALL分区 |
| 20 | SnapshotEvidenceAndMaintenanceReset：工序/操作者证据不可随改名变化；schema/template持久化 | B-02/04/05 全部历史展示与缓存失效 |
| 21 | IdentityAndSlotMatrix、ConcurrentOccupancy、CancelAndReuse | B-01 占用冲突完整API |
| 22 | ContentRules缺项顺序、BindCloseCorrectCancel、IdentityAndSlotMatrix关闭新增/绑定拒绝 | B-01 结束/重开接口 |
| 23 | ContentRules严格“片”/ID、SharedHTTPContracts独立部门 | B-01 内容接口422 |
| 24 | BindCloseCorrectCancel、CurrentSet：绑定前无占用，绑定后选入 | B-01/02 绑定HTTP与查询 |
| 25 | HeaderAndLoss：100/96/4对90/85/5；改明细不改表头 | B-03/04 所有统计/报告同源 |
| 26 | ContentRules：表头null不回填；声明/日期原文/派生字段拒绝 | B-01/03/04 内容与统计HTTP |

## 旧测试处置

- 原 acceptance/invariants 中的独立工序、身份占用、并发、批量回滚、幂等、未知量与原文案例，迁入 foundation_test/rules_test；通过内部仓储执行，不再依赖发布接口。
- 原 TestSamplingLoss 的“允许抽检LOSS”期望替换为422拒绝；“一厂”计量夹具改为“片”，部门独立测试。
- 原 release/upgrade 中有价值的账号保全、迁移、会话/权限、基线和重置案例，替换为 store/users/server/seed 的 G1 测试；旧发布基线不导入新业务历史。
- 原发布生命周期、草稿选择、旧声明/日期原文、任意单位和旧缓存副作用用例删除；对应协议有明确拒绝测试。
- 原旧统计/历史报告 HTTP 测试随旧服务退出；B-02～04 的新 API、筛选、分页、报告回放未实现，明确列在上表，不将删除测试等同验收通过。纯解析算法测试保留，B-05新协议尚未挂载。

## 第二轮衔接

下一步为 B-01：以固定的 Content/WriteTx/Repository/ReadView/schema/template 为基础，接回批、组、单据新建/PATCH/绑定/取消与批量接口。随后完成 B-02 统一筛选/版本选择、B-03统计、B-04报告、B-05解析、B-06 G2。若改变这些已验收基础边界，先重新验证受影响的 G1 项。

本轮不部署切换现有服务，不承诺业务台账已可用；低优先级导出/打印/历史导入保持原排期。

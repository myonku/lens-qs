# 第二轮重建验收记录 G2

日期：2026-11-05（Asia/Shanghai）。第一轮基线：`3681751`；第二轮实现：`ac5ba21`，共享输入契约收尾：`7d0d411`，分支 `codex/rebuild-round-two`。以下最终全包、性能和镜像复验包含两个实现提交。依据为[两轮计划](rebuild-plan.md) B-01～B-06、[需求](requirements.md) AC-01～26、[系统设计](design.md)、[公共设计](common-design.md)、[字段](fields.md)和[API](api.md)。

**结论：G2 开发交付验收通过。** 台账及共享业务接口已接回，双库、历史、并发、安全、备份恢复与性能验证完成。用户明确本次不作部署设计；没有切换现存服务或现存开发库，没有推送远端或触发 GitHub Actions。正式部署目标、可信来源/代理、RPO/RTO、记录保留期在后续工作中确定。

## 1. 工作包交付

| 工作包 | 当前实现与验证入口 |
|---|---|
| B-01 保存闭环 | [HTTP 编排](../backend/internal/ledger/http.go)接回批/组、新建/PATCH/绑定/取消、批量和详情；[仓储](../backend/internal/ledger/repository.go)复用同一 WriteTx/Merge/Derive。成功幂等响应包含原条目 ID；关闭只限制新增/绑定，批量任一失败全部回滚。WriteLifecycleAndContracts、ConcurrentHTTPAndHealth 验证真实 HTTP。 |
| B-02 查询与历史 | [统一 Filter/查询](../backend/internal/ledger/query.go)在 ReadView 中执行数据库筛选/分页；详情按页批取不可变版本及变化条目，不逐单读取关联。支持全部纸面人员/样品/账号/日期/缺项筛选、unknown_day_count、跨单版本拒绝及按 seq 对比。QueriesStatisticsAndHistory、CatalogAndWhitespaceQuery、ConcurrentReadViewPinsVersions 验证。 |
| B-03 统计 | [统计实现](../backend/internal/ledger/stats.go)使用同一筛选、当前版本及一致快照。表头唯一计数、逐单可比性、105 格缺陷、独立变化条目、类型分区、分组分页及两种工作量已实现。缺陷采用一次 SQL 的单据内/跨单分层聚合；不把行和变化 JOIN 后乘倍。StatsNullPartitionsAndActions 与既有纯规则用例验证。 |
| B-04 报告与审计 | [报告实现](../backend/internal/ledger/reports.go)按完整领域事务末事件还原批/组/单与当时版本；工序范围报告分页与全范围小计分开。审计支持实体/账号/系统时间筛选，凭据不进入返回内容。ClockRollbackAndHistoricalLabels、HistoricalBindingAndClosedGroup、QueriesStatisticsAndHistory、BusinessBackupRestoreReport 验证。 |
| B-05 解析与目录 | [图号维护](../backend/internal/dictionary/drawings.go)分页/完整事务导入；[解析与缓存](../backend/internal/sample/service.go)保留逐次原文，支持别名、多候选/冲突、完整确认映射、可空 Rx、目录/解析器版本失效。GET cache 只读；写闸门保护人工确认优先。SharedParsingAndImport、DrawingsBeyond500AndFullConfirmation、ManualCacheWinsConcurrentParse 验证。 |
| B-06 开发交付 | 双库全包 race/vet/build，年度规模查询计划/性能，实际镜像 HTTP 闭环，依赖故障、启动结构、会话来源、备份恢复、维护回滚/重跑和在途请求退出等待均有下述证据。运行说明已更新。 |

## 2. 第一轮衔接与结构变化

- `immutable_identity` 按 API 返回 400；版本冲突返回实体及当前版本，跨类型拒绝带 reason；占用冲突给现有单据与可采取动作。
- Reader 增加只读行查询能力，CurrentSet 接受 Reader；列表、统计和报告的多个查询在同一快照执行。跨连接写入提交后，已打开的读视图仍保持原版本集合。
- 新增双库 0008；0001～0007 文件保持不变。0008 支持图号 `is_rx=null`、非空序号唯一、单位筛选投影、常用查询索引与共享目录版本。已知 0006 的受控重建及空库全链最终同构；已知 0007 使用显式非破坏升级。
- 数量变化只读 ID 从版本所属存储条目组装，不进入可编辑内容或重写 schema 2。重复条目仍有不同且稳定的 ID；原键回放、详情及历史读取一致，客户端写 ID 拒绝。
- 字段目录输出表面编码/中文名、缺陷明确顺序。共享目录更新触发自动缓存失效；人工确认保留并标需复核。
- 共享新增接口明确区分省略与非法 null，允许的员工号/部门 null 仍保留；空分页参数、null 数组元素及退出接口的正文拒绝。SharedNullAndLogoutContracts 验证失败退出请求不撤销会话，合法退出会立即撤销；QuantityChangeEvidenceIDs 同时核对幂等回放响应头。
- 原计划评估基线与当前进度已分开标注。上述基础变化后，G1 用例随全包测试在两库重新通过，旧 draft/publish/任意计量单位入口仍为 404。

## 3. 实测与证据边界

全部数据库写入均位于测试临时文件、独占 PostgreSQL schema/恢复库或新建的 Docker 测试卷。没有对现存 `backend/data/lens.db`、已有 `.env`、已有账号或正在运行的其他服务实施变更。

| 验证 | 实际结果 |
|---|---|
| Windows Go 1.27.1 / SQLite | `go test -race ./... -count=1`、`go vet ./...`、`go build ./...` 通过。 |
| Linux Go 1.26.10 / SQLite | 同一组全包 race/vet/build 通过。 |
| Linux Go 1.26.10 / PostgreSQL 17 | 全包 race/vet/build 通过；显式临时 DSN、每例独占 schema，恢复用例使用真实 pg_dump/createdb/pg_restore。 |
| Linux CI 命令复验 | [verify-linux.ps1](../backend/scripts/verify-linux.ps1)实际执行两库全包及性能，最终输出 PASS；[Actions 配置](../.github/workflows/backend.yml)两库测试均禁用结果缓存。未推送/触发远端 Actions，不声称有远端绿色记录。 |
| 0007→0008 保全与失败回滚 | UpgradePreservesDataAndRollback：指纹错误拒绝；账号哈希/人工确认不变；null Rx 可存；重复图号序号使 DDL/数据修改整体回滚，源指纹相同；升级重跑通过。 |
| 0006/空库路径 | G1 EmptyAndLegacyPreservation、EmptyAndLegacyHaveIdenticalSchema、PreflightAndFailedRebuildRollback 在最终 0008 两库通过，保留账号/配置与维护回执。 |
| 业务备份恢复 | BusinessBackupRestoreReport：实际备份/恢复后重新装配 HTTP 服务，历史报告按同一 as_of/through_event_id 完全一致；保存版本、取消证据和审计可读取。SQLite/PostgreSQL 均通过。 |
| 安全与启动 | G1 SharedHTTPContracts/迁移完整性验证继续通过；G2 NewEndpointMatrix 逐条检查新端点的未知参数、GET 正文、未知字段及不存在资源，SharedNullAndLogoutContracts 验证共享接口 null/空分页/退出协议。健康检查在数据库关闭后返回 503。 |
| 在途退出 | ShutdownWaitsForAcceptedWrite 使用真实 HTTP listener；Shutdown 等待已接收的写请求完成，客户端收到 201，数据已提交。 |
| 开发镜像 | Dockerfile 构建成功；[verify-runtime.ps1](../backend/scripts/verify-runtime.ps1)实际运行初始化、seed、显式建账号、登录、建批/组/单、统计、报告、取消和两次启动/优雅退出；重启前后持久数据指纹一致。 |

复验脚本会清理其创建的测试业务容器、网络及卷；`lens-g2-go-cache` 为可复用的编译缓存，不是业务数据卷。没有正式部署、生产恢复目标、TLS/代理或真实现场容量结论。

## 4. 年度规模性能

工具：[TestG2AnnualScalePerformance](../backend/internal/server/performance_test.go)。硬件为 Intel Core Ultra 7 155H，16 核/22 逻辑处理器，约 32 GiB 内存；Linux 为 Docker Desktop 环境，Go 1.26.10。下面为最终 Linux 复验结果，未开启 race 计时。

夹具包含 100001 张单、110001 个内容版本及明细行；约 10% 单有两个版本，35×3 格多数未知、点伤凹面已知，数量变化含未知量。1000 个生成生产批各有 100 张单；机台 target 选中 1000 张。保存测试为 16 个并发调用方、96 次实际带认证的 HTTP 保存。查询请求按契约每页 100 条，total 为千笔完整条件范围。

| 测量 | SQLite P95 | PostgreSQL 17 P95 | 样本 |
|---|---:|---:|---|
| 单笔保存，16 并发 | 90.32ms | 165.33ms | 96 |
| 千笔条件范围、返回 100 条详情 | 57.16ms | 48.24ms | 20 |
| 10 万张当前单汇总 | 1972.48ms | 649.57ms | 5 |
| 千笔缺陷汇总 | 78.23ms | 69.89ms | 5 |
| 单批 100 张历史报告 | 13.08ms | 97.40ms | 5 |

保存 P95 <500ms、千笔条件查询 P95 <2s 均满足。SQLite 查询计划使用 `ix_revision_machine` 后按单 ID 查找及稳定排序；PostgreSQL 使用同一机台索引并与单据做 Hash Join，优化器选择扫描 10 万张轻量单据行；没有逐单 N+1 内容查询。

年度夹具通过隔离 SQL 批量生成，生成过程不是业务批量 HTTP 吞吐测试；历史夹具有一次完整灌数事务，报告时间不代表年度逐次修改的最坏回放成本。正确性中的多事务、同秒、绑定、取消、改名和时钟回退由独立端到端用例验证。以上性能结论限定于所列硬件、数据分布和测试环境。

## 5. AC-01～26 验收映射

测试简称对应 [HTTP 闭环/查询/解析](../backend/internal/server/round_two_test.go)、[契约及统计](../backend/internal/server/contracts_test.go)、[边界用例](../backend/internal/server/edge_contracts_test.go)、[存储规则](../backend/internal/ledger/foundation_test.go)与[纯规则](../backend/internal/ledger/rules_test.go)中的完整 TestG1/TestG2 名称。

| AC | 实际证据 |
|---|---|
| 01 | WriteLifecycleAndContracts、IdentityAndSlotMatrix：trim-only 身份、重复报冲突、同批两类独组。 |
| 02 | WriteLifecycleAndContracts：先建/录后序，单据按各自组和 ID 独立存在。 |
| 03 | 同上及 IndependentGroupsHeaderAndLoss：前序未录不阻止后序。 |
| 04 | WriteLifecycleAndContracts、QueriesStatisticsAndHistory：新建与 PATCH 分开、详情/列表不覆盖。 |
| 05 | ContentRules、WriteLifecycleAndContracts：省略/null/0、数组替换、连续版本及历史接口。 |
| 06 | QueriesStatisticsAndHistory、ClockRollbackAndHistoricalLabels：更正改变当前版，固定旧报告不变。 |
| 07 | WriteLifecycleAndContracts、AtomicRetryBoundaryClockAndRollback：旧版本冲突/失败无半写及成功缓存。 |
| 08 | WriteLifecycleAndContracts、StatsNullPartitionsAndActions：取消退出当前数量，内容/版本/原因保留。 |
| 09 | WriteLifecycleAndContracts、ConcurrentHTTPAndHealth：原键重试、批内冲突全回滚、竞争仅一方成功。 |
| 10 | StatsNullPartitionsAndActions、CountsComparabilityAndOverflow：未知/零、空集合、零分母、逐单不可比及安全整数。 |
| 11 | QueriesStatisticsAndHistory、DefectUnknownIsPerSubmission：35×3 输出、无行未知、跨单未知计数和重复缺陷提示。 |
| 12 | SharedParsingAndImport、ManualCacheWinsConcurrentParse：原文、别名、多候选、人工确认、未知 Rx、不猜项目。 |
| 13 | QueriesStatisticsAndHistory、StatsNullPartitionsAndActions：纸面人员筛选与账号归属/保存动作分开。 |
| 14 | ContentRules、QueriesStatisticsAndHistory：仅原因可存，数量 null 的统计单元保留 unknown，不扣表头。 |
| 15 | QueriesStatisticsAndHistory：业务日与创建时点显式，未知日单列，不以系统日期兜底。 |
| 16 | ContentRules：真实公历、时点 UTC、上海归日、精度互斥；查询使用规范投影。 |
| 17 | QueriesStatisticsAndHistory、ClockRollbackAndHistoricalLabels：同秒固定边界、中间事件拒绝、时钟回退可重放。 |
| 18 | QueriesStatisticsAndHistory、ConcurrentReadViewPinsVersions：分页不改全范围小计，跨连接更正不污染已开快照。 |
| 19 | WriteLifecycleAndContracts、StatsNullPartitionsAndActions：管理员也不能跨类型；SAMPLING LOSS 422；ALL 分区无混合总计。 |
| 20 | ClockRollbackAndHistoricalLabels、ManualCacheWinsConcurrentParse：改名/停用不改旧报告，目录变更使自动缓存失效而保留人工映射。 |
| 21 | WriteLifecycleAndContracts、IdentityAndSlotMatrix、ConcurrentHTTPAndHealth：整/分批互斥、取消重用、并发唯一。 |
| 22 | WriteLifecycleAndContracts、HistoricalBindingAndClosedGroup：缺项只提示、结束后已有单可改/取消、新增/绑定拒绝。 |
| 23 | ContentRules、SharedHTTPContracts、WriteLifecycleAndContracts：片/ID 校验，部门独立，其他单位 422。 |
| 24 | HistoricalBindingAndClosedGroup、StatsNullPartitionsAndActions：绑定前不入组，绑定后入组，旧报告不变，失败不留半笔。 |
| 25 | IndependentGroupsHeaderAndLoss、QueriesStatisticsAndHistory：表头 100/96/4 对明细 90/85/5 仍按表头统计，仅改明细不改表头。 |
| 26 | ContentRules、WriteLifecycleAndContracts、StatsNullPartitionsAndActions：表头 null 不回填，declared_counts/日期原文/派生输入 400。 |

补充协议证据：NewEndpointMatrix 覆盖本轮所有 GET/写端点的输入边界；QuantityChangeEvidenceIDs 覆盖只读条目 ID、重复条目和重试；DrawingsBeyond500AndFullConfirmation 实际导入 501 条并读取第 6 页，坏导入没有部分提交；ShutdownWaitsForAcceptedWrite 与 BusinessBackupRestoreReport 验证运行/恢复闭环。

## 6. 交接边界

开发交付可用于新建隔离环境和后续前端对接，仓库没有前端应用。使用方法、升级和复验入口见[后端说明](../backend/README.md)。现存 0006 开发库保持原状，不能直接由新 API 自动接管；后续切换应另行指定目标、停写、备份和可信配置。

未包含正式部署/切换或远端发布，未作生产容量、RPO/RTO 或长期质量记录保留承诺。导出、打印/PDF、历史检验台账导入仍按原计划后置。这些边界不改变本次 B-01～B-06 的开发验收结果。

# 历史：v2 初次施工与验收记录

> 此文件描述6d20730时的交付，旧双域/导入流程已被正式发布替代。当前以[发布记录](v2-release.md)、[需求](requirements.md)及[接口](api.md)为准。

日期：2026-11-04（Asia/Shanghai）。施工分支：`codex/v2-rebuild`。文档基线提交：`696c125`，已先推送到 main。

本文件记录本次代码施工、运行证据与采用的默认值，不承担最终文档整理/同步。README、需求、设计、API、功能清单与原重建计划的全面状态整理留在本次范围外。

## 实现范围

- 新增 0005 双库迁移；保留 0001–0004 原文件与旧账户、字典、forms、历史。产品批→按 NORMAL/SAMPLING 区分的工序组→检验单→不可覆盖内容版本。
- 草稿、未归组草稿、明确归组、发布、正式修订、取消与释放占用、工序关闭/开放；整批/具名分批互斥，批量新建整笔回滚。
- `/api/v2` 复用原登录会话；所有写入强制幂等键，更新强制 expected_version。结果与业务、审计同事务提交，回放优先于并发版本检查。
- 正式统计仅使用未取消单的 current_revision_id。支持类型隔离、维度分组、未知计数、105 格缺陷、数量变化、日期筛选、产品批报告和实际版本集合。
- 字段目录、稳定版本通用模板、版本历史和精确版本差异；不依赖旧 form_no upsert。
- `cmd/rebuild` 支持只读盘点、原文暂存、源指纹校验及人工清单导入。导入仅建立草稿，不自动推断完整身份、批次、类型或正式结果。
- `BUSINESS_WRITE_DOMAIN=v2|legacy` 控制唯一业务写入口，默认 v2；另一域的业务写入在幂等中间件之前拒绝，账户/字典继续共用。
- 修复 PostgreSQL 迁移版本 INSERT 未 Rebind 的问题，以及共享账户/字典将 bool 直接传入 INTEGER 列的问题。
- 修复容器入口在重复启动时可能调用 createuser -update 重设密码的问题，改由 API 仅在账户库为空时引导管理员。
- 容器构建工具链对齐 go.mod，并提供 rebuild 命令；镜像仅打包 CSV 参考数据，排除本地库、备份和 .env。

## 实施取舍与默认值

Q-01/02/03/04/05/06/07/08/10 按文档工作默认执行，不追记为新增业务确认：身份仅去首尾空格；重复/整分混用 409；未知数量可发布；人工关组；普通登录用户可更正但需原因/版本/审计；无复检轮次；LOSS/ADJUSTMENT/OTHER；Asia/Shanghai；报告先提供实际版本集合。

写事务采用数据库内全局 v2 写闸门（SQLite 首条 UPDATE 获取写锁、PostgreSQL 锁同一行），代替较复杂的多组行锁顺序。覆盖跨连接/跨进程竞争，代价是 v2 写入串行；锁等待/整个写事务有界，存储错误不降级放行。后续如有明确吞吐需求再收窄锁范围。

版本 content 为完整 TEXT JSON 快照；正式计数、规范业务日期/时点/归日、设备/检验人员另有查询列，明细 105 格与数量变化有独立表。全部日期/时点物理列保持 TEXT。发布只设置一次发布元信息，不覆盖内容。数量变化记录 ID 与保存人时在版本响应的只读 `quantity_change_records` 中提供。

读统计在一个一致快照内确定正式集合，关联明细与数量变化分开聚合，避免联接乘倍。当前采用应用层聚合，尚未声称进行大数据量压测或达到生产吞吐指标。

本工程交付后端。抽检 LOSS 的前端拦截要求通过字段目录中的 `sampling_loss_validation=caller` 交付；没有创建额外前端项目。后端共用结构保留直接 API 写入的原始 LOSS，但抽检损耗汇总排除这些条目。

持久报告快照、导出、产品批合并/改键、复检轮次、跨工序谱系均按本期文档边界不增加。

## 验收证据

| 范围 | 对应 AC | 已运行证据 |
|---|---|---|
| 产品批身份、五单共存、后序先录 | 01/02 | TestLifecycle；同号不同完整型号/不同号不误合并 |
| 整批空值、重复/混用、取消、关闭 | 03/04/05 | TestSlotsAndAtomicBatch、TestPatchArraysAndVersionBoundaries |
| 并发整批/分批、同键竞争、原子回滚 | 06 | TestConcurrency、TestConcurrentIdenticalRequests、TestIdempotency、TestResultStoreFailureRollsBackBusiness；SQLite 多连接与 PostgreSQL 验证 |
| 正式修订与版本集合、后序不变 | 07/08 | TestLifecycle、精确历史 diff；修订草稿不替换当前正式版 |
| 未知/零、缺陷、计数来源 | 09 | TestUnknownAndValidation、TestCatalogQueriesAndDefectCompleteness |
| TEXT 日期/时点、午夜归日、日期清洗 | 10 | TestLifecycle、TestUnknownAndValidation、TestDateClassification、TestAdditiveMigrationPreservesLegacy |
| 未知损耗、修订不重计 | 11 | TestUnknownAndValidation；数量变化随当前正式版聚合 |
| 迁移保留、明确人工核对、重跑 | 12 | TestLegacyReviewMigration、TestAdditiveMigrationPreservesLegacy；本地库副本演练 |
| 抽检类型/状态/统计隔离 | 13/14 | TestLifecycle、TestSlotsAndAtomicBatch、TestSamplingLoss |
| 抽检共用结构的后端边界 | 15 后端部分 | TestSamplingLoss；前端页面不在本仓库，未宣称完成外部前端验收 |
| 登录、复用基础服务、唯一写入口 | 通用 | TestAuthenticatedV2Routes、TestSingleBusinessWriteDomain、TestSharedAccountsAndDictionary |

执行命令（backend 目录）：

```powershell
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
# 显式指向隔离测试 PostgreSQL（测试为每个场景创建随机 schema，结束后清理）
$env:LENS_TEST_POSTGRES_DSN = 'postgres://.../lens_test?sslmode=disable'
go test -race ./internal/ledger -count=1
```

PostgreSQL 实际验证版本为隔离 Docker PostgreSQL 17。CI 工作流 `.github/workflows/backend.yml` 运行 SQLite 全量竞态测试和 PostgreSQL v2 场景；工作流本身尚未以远程运行结果冒充通过。

## 本地数据演练

只读盘点 `backend/data/lens.db`：2 张旧单、2 条明细、14 条历史、1 个账户、21 项字典、0 个单位。系统时间盘点无非规范值；纸面日期分类为 2 项日期、2 项缺失。未输出密码哈希、会话令牌或纸面明文。

原库 SHA-256：`324621b87d3efd6767786bed108a026833ad9b901b32d135c98dbe6bf341b989`。使用 SQLite backup API 生成 `backend/data/backups/v2-rebuild-20261104-082311.db`，完整性检查为 ok；再复制为演练库后执行新增迁移与暂存/幂等重跑。备份与演练库仅在忽略目录，不提交业务数据。

演练逐表比较 users/forms/form_lines/form_history/dict_items/units 全行，确认完全相同；2 条旧单仅进入 PENDING 暂存，无 slot、无新单、无正式发布。原库文件指纹仍相同。真实数据的完整身份、正常/抽检类型和批次没有充分证据，不自动导入为有效结果；生产环境及外部调用方未切换。

## 操作入口

```powershell
# 只读盘点；对 SQLite 可给 mode=ro 连接串
$env:DB_DRIVER = 'sqlite'
$env:DB_DSN = 'file:D:/path/to/lens.db?mode=ro'
go run ./cmd/rebuild

# 备份/恢复校验后，对选定目标执行迁移；唯一写入口选定 v2
$env:DB_DSN = 'D:/path/to/target.db'
$env:BUSINESS_WRITE_DOMAIN = 'v2'
go run ./cmd/migrate
go run ./cmd/rebuild -stage -actor '<existing-user-id>' -key '<unique-stage-key>'
go run ./cmd/rebuild -import-reviewed './review.json' -actor '<existing-user-id>' -key '<unique-import-key>'
```

人工核对清单格式：

```json
{"items":[{"source_id":"旧单 ID","source_fingerprint":"盘点输出的 SHA-256","shared_form_no":"共享号","sample_identity":"已核实的完整型号","operation_id":"已存在的工序字典 ID","inspection_kind":"NORMAL","batch_no":null,"counts_confirmed":true,"reason":"已核对纸单、类型、整批范围和默认零语义"}]}
```

batch_no 必须明确给出，null 才表示已核实整批；类型不能省略。导入后仍要按 v2 接口明确发布。出现冲突整批回滚，不追加假批号。不应使用 `dev.ps1 -Reset`，也不将旧备份覆盖到已产生新写入的数据库；恢复应先保全切换后新数据。

## 归档保真

发现 Git 全局 LF 规则会改写部分既有归档的 CRLF，使重新检出后不匹配原 manifest。通过 `.gitattributes` 对 legacy 归档禁用文本转换，恢复其原始字节；7 项归档 blob SHA-256 均与原 manifest 一致，未修改归档业务内容。

## 最终部署验收补充

最终 0005 结构在 `backend/data/backups/v2-final-rehearsal.db` 再次演练，SQLite integrity_check 与 foreign_key_check 均通过；逐表账户/旧单/历史完全一致，2 条待核对候选、0 个 slot，原库 SHA-256 未变。

最终执行 `go test -race ./... -count=1`、`go vet ./...`、`go build ./...`，以及独立 PostgreSQL 的 `go test -race ./internal/ledger -count=1`，均通过。另验证参考清单未匹配、声明计数差异与可能重复缺陷均为提示，不阻止发布。

镜像构建命令：`docker build --build-arg GOPROXY=https://goproxy.cn -t lens-qs:v2-acceptance backend`。首次默认代理下载被网络拒绝，改用本机已配置的镜像源后构建通过；GOPROXY 保持可由构建参数选择，未修改全局 Go 配置。修复了本机 CRLF 入口脚本导致 Linux 启动失败的问题，构建阶段显式规范换行。

实际容器连接隔离 PostgreSQL 17 验证：自动迁移、种子工序模板、管理员登录、v2 产品批新建、旧 forms 写入返回只读冲突、改密后重启仍可用新密码且原引导密码失效，全部通过。最终镜像 `/app/data` 仅有两份 CSV，不含本地业务库、备份或 .env。临时验收容器和测试网络在验收后清理；原业务库和独立备份保留。

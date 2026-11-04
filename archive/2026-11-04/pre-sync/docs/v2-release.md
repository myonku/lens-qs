# v2.0.0 发布与验收记录

日期2026-11-04，分支codex/v2-release，起点6d20730。本次用户新增：抽检账户隔离、生产批指定系统时点全工序报告、工序时间范围报告（限定/不限批）；授权销毁旧契约/清理旧设计及测试数据，正式迁到v2。

需求/设计/API/迁移/README和功能状态已更新。历史施工文档与archive仅用于追溯，不再作为当前契约。

## 交付结构

- account_type NORMAL/SAMPLING，登录/me返回账户与填报类型；所有检验变更在事务内校验目标类型，管理员无隐式越权。账户类型变更与会话撤销/幂等删除同事务，密码不变。
- 产品批报告as_of回放审计快照和不可覆盖版本，through_event_id固定精确视图；保存理由与后续发布理由分开，历史草稿不泄露未来发布信息。
- 工序范围报告支持日期/创建时间、产品批可选、分页详情与全范围正式小计，未知值/草稿/取消与类型独立。
- 旧inspection/stats/idempotency包、旧导入命令、旧脚本和业务域开关已移除。认证/用户/字典/解析也只有/api/v2；旧路径404。已发布迁移保留，通过0006删除旧表并建立正式历史起点，新增显式reset-testdata。

## 验收记录

以下均已实际运行：

| 验证 | 结果 |
|---|---|
| `go test -race ./... -count=1` | 全部通过，包括账户管理HTTP、权限隔离、历史/范围报告、SQLite连接默认参数 |
| 隔离PostgreSQL17 `go test -race ./internal/ledger -count=1` | 全部通过，场景逐个随机schema隔离并自动清理 |
| `go vet ./...`、`go build ./...` | 通过；5个Windows命令行二进制已重新构建到backend/bin |
| Docker Go1.26构建 | 通过，镜像lens-qs:v2-release，构建GOPROXY=https://goproxy.cn |
| PostgreSQL容器端到端 | 新认证路径、用户管理、sampler登录/默认标记、跨类型403、发布/取消、事件固定历史报告、工序范围分页小计、类型变更撤销会话、测试清理、改密重启保留全部通过 |
| 最终SQLite镜像 | 新建库0001–0006、seed/bootstrap、v2登录、抽检默认、越权403与新查询通过 |
| 旧v2草案升级 | 两库均从0005实测升级0006：账户密码和已有v2内容保留、基线审计可回放、旧表删除、重复迁移无变化 |
| 本地旧测试库副本演练 | 0004→0005→0006，清理测试业务，账户/密码/字典指纹一致；完整性和外键检查通过 |
| 文档/归档静态验证 | 28个本地文档链接、11段JSON示例通过；原7项归档哈希不变；旧包/导入命令/域配置不存在 |
| 本地真实工作区二进制启动 | 临时独立端口使用当前迁移库，healthz正常，根version=2.0.0，旧路径404、v2认证401；启动未重建或重置账户 |

新验收主要用例：TestAccountTypeIsolation、TestAccountTypeChangeRevokesSessionAndCache、TestHistoricalReportAndPinnedEvents、TestHistoricalGroupClosureAndTypePartitions、TestOperationRangeReports、TestResetPreservesAccountsAndReferences、TestUpgradeFromV2DraftPreservesAccountsAndSeedsHistory、TestSamplingLoginAndAdminTypeManagement、TestSQLiteURIKeepsConnectionPragmas。旧域测试移除，同批/整批/并发/修订等v2既有用例继续保留并适配两类真实账户。

## 本地切换与测试数据清理

目标仅为当前工作区 `D:/Pr/lens-qs/backend/data/lens.db`，不是生产或其他副本。迁移前SQLite backup API备份到 `backend/data/backups/v2-release-before-20261104-090407.db`，恢复副本完整性检查通过；迁移前文件SHA-256为`324621b87d3efd6767786bed108a026833ad9b901b32d135c98dbe6bf341b989`。

执行原库迁移时默认Windows SQLite临时文件创建失败，0006事务回滚。使用temp_store=MEMORY后原库迁移及清理完成；连接工厂已将内存临时存储加入默认参数（自定义URI也补齐），并新增回归测试。没有绕开事务或手工登记假成功迁移版本。

原库已实际执行0005/0006及reset-testdata；当前schema版本6，product_lots/operation_groups/inspection_submissions/submission_revisions/audit_logs/sessions均为0。旧forms/form_lines/form_history/idempotency_requests/legacy_migration_candidates表不存在。1个原账户与21项字典保留；账号ID、用户名、密码哈希、管理员标记及字典完整行SHA-256与备份一致；account_type默认为NORMAL。integrity_check=ok，foreign_key_check为空。

新的历史起点为 `2026-11-04T01:07:46Z`（上海09:07:46），早于此的查询明确history_unavailable。备份、演练库、结果JSON与二进制都在忽略目录，没有提交业务数据或凭据。测试会话已清，原账户需要重新登录，密码未改。

## 交付边界

全部新调用方都必须使用/api/v2，包含认证、用户、字典和解析；旧路径没有别名。前端根据登录/me固定账户填报类型，抽检LOSS仍由前端拦截。外部前端工程不在本仓库，未声称已部署外部前端或生产环境。

发现一个正在运行的API进程来自 `D:/Pr/lens-qs - 副本/backend/bin/lens-qs-api.exe`；没有改动或替换另一个目录中的服务。本工作区二进制已经用本库在独立端口验证，验证结束停止该临时进程。临时Docker验收容器和网络完成后清理，构建镜像保留。

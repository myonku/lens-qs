# 后端工程：第一轮基础版本

当前交付为[重建计划](../docs/rebuild-plan.md)的 A-01～A-07 / G1。业务依据仍为[需求](../docs/requirements.md)、[设计](../docs/design.md)、[公共设计](../docs/common-design.md)、[字段](../docs/fields.md)与[API](../docs/api.md)。验收范围与实测证据见[第一轮验收记录](../docs/rebuild-g1.md)。这是隔离开发版本，不用于替换现有业务服务。

## 已挂载和待接回接口

已挂载：公开 `/`、`/healthz`、`POST /api/v2/auth/login`；登录后的 auth/me、auth/logout、auth/change-password、users、dict/categories、dict/items、只读 units、form-fields、form-templates。用户管理仅管理员可用。

台账生产批、工序组、检验单、内容历史、统计、报告、audit-logs、样品解析/确认/缓存、参考图号查询/导入均未挂载，返回 404。旧 draft/publish、POST units 和未版本化业务路径也返回 404。解析仅保留纯算法与可空协议类型；参考数据仅支持显式缺项初始化。

## 普通启动

在 backend 目录使用自选的隔离配置；已有 `.env` 不覆盖。`go run ./cmd/api` 与 `scripts/dev.ps1` 只校验结构并启动，不迁移、seed、清库或创建账号。旧库、缺损结构、迁移校验不符会拒绝启动。历史 AUTO_MIGRATE、SEED_ON_START、BOOTSTRAP_ADMIN_* 不再控制启动行为。

```powershell
# 仅首次创建配置，先按实际环境修改
Copy-Item .env.example .env
# 明确选择全新隔离库之后：
go run ./cmd/migrate -initialize
go run ./cmd/seed
# 显式创建所需初始账号；密码由操作者选定，不使用固定凭据。
go run ./cmd/createuser -username <login> -password <chosen-password> -admin
go run ./cmd/api
```

初始化只接受空库或已匹配的当前结构；seed 只补缺项，不覆盖人工标签、别名、排序、停用状态、图号或已有账号密码。API 没有首次改密业务关卡。密码创建/重置为 8–128 字符，旧 PHC 哈希仍按原参数验证。

Cookie 写请求必须有配置允许的 Origin。CORS_ORIGINS 使用明确来源，不接受通配符；命令行可使用 Bearer。Cookie/Bearer 不同返回 400。请求体最多 4 MiB，HTTP 正文读取最长 15 秒、响应写入最长 30 秒；重复键、未知字段/参数、重复参数和多个 JSON 值拒绝。写事务最长 10 秒，数据库锁等待最多 5 秒，迁移最长 30 秒。

## 只读盘点、备份及受控重建

```powershell
go run ./cmd/migrate -status
```

输出驱动、无凭据目标位置、迁移、表名、数量、配置冲突、数据摘要与目标指纹，不输出密码哈希或 DSN。SQLite 状态连接使用 mode=ro，不创建缺失数据库或改变源库 journal 模式。

只支持已知完整 `0006` 输入；早期版本、未知/缺损结构、迁移名称冲突、部门悬空引用、冲突工序配置和非法人工元数据均拒绝。`0001～0006` 未改写，首次引入旧迁移摘要的 provenance 明确为名称/结构基线，不宣称过去验证过文件哈希。`0007` 与配置转换、保全摘要核对、维护回执在同事务提交。

SQLite 先停写，生成并重新打开一致备份，核对数据摘要：

```powershell
New-Item -ItemType Directory -Force backups | Out-Null
go run ./cmd/backup -out backups/before.db -receipt backups/verified.json
# 使用 status 输出的当前指纹，不要复用其他目标的指纹。
go run ./cmd/migrate -rebuild-test-data -target-fingerprint <fingerprint> -verified-backup backups/verified.json
```

backup 通过 SQLite VACUUM INTO 生成包含 WAL 数据的独立文件，源库以只读模式打开；禁止仅复制仍在写入的主文件。备份含账户哈希等私人数据，放在 Git/Docker 已排除的 backups 目录。重建会在取得数据库锁后再次核对数据摘要，源库与备份回执不一致则拒绝。

PostgreSQL 使用匹配服务端版本的 `pg_dump --format=custom` 做一致备份，再用 `pg_restore --exit-on-error` 恢复到独立空库（目标 schema 保持一致）。备份/恢复连接由环境或安全的本地凭据配置提供，不把带密码的 DSN 写入日志。设置 `LENS_BACKUP_RESTORE_DSN` 指向该恢复库后，执行 `go run ./cmd/backup -out <existing-dump-path> -receipt backups/verified.json`，工具比对源库与恢复库的数据摘要并生成同类回执，再执行显式重建命令。它拒绝把源库自身冒充恢复库。测试中的 Docker PostgreSQL 使用 pg_dump/pg_restore 真实验证这一过程。

重建保留账号 ID/密码/工号/权限/类型、部门文字、人工字典/图号、人工缓存确认、非检验旧审计及原迁移记录。旧组织目录另存 preserved_organization_units；相同工序的 ID 映射保存在 dictionary_id_mappings。人工解析确认保留并标记 legacy-unverified/needs_review，不冒充当前规则确认。清除测试检验记录、旧业务回放基线、会话、领域幂等及自动缓存。新历史起点在首个领域事务提交时建立，无虚构单据事件。

重建失败整体回滚；切换后回退依赖停写时的完整备份及匹配的旧程序/配置，不提供有损 down 迁移。新版本产生真实资料后，不得用切换前备份直接覆盖。

## 当前结构的测试数据重置

```powershell
go run ./cmd/reset-testdata  # 只读预览
go run ./cmd/reset-testdata -confirm-test-data -target-fingerprint <preview-fingerprint>
```

必须先停写和备份。仅接受匹配的 `0007`，清检验业务/领域事件、会话、领域幂等及自动缓存，保留账号、目录、模板、人工确认和共享审计。不会清除人工缓存或改密码。

## 继续第二轮的边界

- `ledger.Content / Merge / Derive / AggregateCounts / AggregateDefects`：统一可空字段、补丁、校验、提示与安全整数规则。
- `ledger.Write / Repository`：在同一事务创建批/组、保存/绑定、关闭/开放、取消及批量编排；Request 固定原始方法/路径/正文、操作者、幂等键及成功状态码。
- `store.BeginWrite / WriteTx.Event / Commit`：数据库写闸门、当前账号复查、持久逻辑时钟、事务末事件及同事务审计。
- `store.ReadView / Reader / Snapshot / Boundary`：一致读与完整事务上界；`ledger.CurrentSet` 是最小当前版本集合查询，第二轮在同一快照上扩展筛选/分页/报告。
- `field_schemas / form_templates`：持久不可变版本定义；每份保存版本与创建工序/操作者证据绑定。普通接口没有改写历史版本或模板的入口。

## 工程验证

```powershell
go test -race ./... -count=1
go vet ./...
go build ./...
# PostgreSQL 测试显式设置 LENS_TEST_POSTGRES_DSN；每个用例独占生成的临时 schema。
# 完整备份恢复案例再设置 LENS_TEST_PG_DOCKER_CONTAINER，指向一次性的 PostgreSQL 容器。
```

CI 在 Linux 运行两库全包 race 测试、vet、build。未配置 PostgreSQL 时，备份恢复专用案例明确 skip；不能把 SQLite 结果写成双库结果。业务查询、统计、历史报告完整接口和性能/部署验收属于第二轮 G2。

# 后端工程：本机开发与联调

当前后端数据库结构版本为 0010，默认在 Windows 本机运行，日常使用 SQLite，仅作开发测试。业务依据为[需求](../docs/requirements.md)、[设计](../docs/design.md)、[公共设计](../docs/common-design.md)、[字段](../docs/fields.md)与[API](../docs/api.md)。重建和本机装配已经完成，历史节点及验证证据入口见[项目历史](../docs/history.md)。真实域内前端联调及正式部署参数留待后续确定。

## 接口范围

公开 `/`、`/healthz`、`POST /api/v2/auth/login`；其余接口均位于 `/api/v2` 且要求登录。用户管理仅管理员可用；管理员写台账也受自身 NORMAL/SAMPLING 类型限制。

已接回生产批/工序组、单据新建/PATCH/绑定/取消/批量新建、详情/列表/内容历史/diff、统计维度/计数/缺陷/数量变化/两种工作量、生产批历史报告、工序范围报告及审计查询。共享能力包含认证/用户、字典、只读单位、字段/模板、图号分页/原子导入、样品 parse/confirm/只读 cache。端点、默认筛选、状态码及响应结构以 API 契约为准。

旧 draft/publish、POST units 和未版本化业务别名返回 404。Excel/CSV、打印/PDF、历史检验台账导入仍为低优先级后续能力，参考图号导入不能用来导入历史检验单。

## 本机启动与前端联调

日常在 `backend` 目录运行，本机 `.env` 配置 `APP_ENV=dev`、`APP_ADDR=:8080`、`DB_DRIVER=sqlite`、`DB_DSN=./data/lens.db`。已有 0008 或 0009 本机库须按下文显式升级至 0010，使用原有账户密码登录。

```powershell
cd D:\Pr\lens-qs\backend
./scripts/dev.ps1          # 前台运行，Ctrl+C 退出
# 或后台运行；仅选择其中一种启动方式，避免端口冲突：
./scripts/dev.ps1 -NoWait
Invoke-RestMethod http://localhost:8080/healthz
```

后台日志为 `bin/api.log` 和 `bin/api.err`，可通过 `Get-Process lens-qs-api` 查看进程，并用 `Stop-Process -Id <进程ID>` 停止。重新启动前先停止当前实例。普通启动校验结构，不迁移、seed 或清库；仅在用户库为空且配置引导凭据时创建初始管理员。已有账号不受启动配置影响。旧库、缺损结构、迁移校验不符会拒绝启动。历史 AUTO_MIGRATE、SEED_ON_START 仍不生效；可选 BOOTSTRAP_ADMIN_* 配置现已恢复，规则见下节。

后端地址为 `http://localhost:8080`，API 前缀 `/api/v2`。本机允许来源为 `http://localhost:8080,http://127.0.0.1:8080`，Cookie 使用 HTTP（`COOKIE_SECURE=false`）。Cookie 登录和后续请求须使用同一个主机名。前端后续使用其他域名或端口时，将其准确 Origin 追加到 `.env` 的 `CORS_ORIGINS` 并重启服务；同端口前端需通过开发代理转发 `/api/v2`，后端自身不提供前端应用。监听 `:8080` 可供域内连接，但浏览器来源仍须显式配置，网络可达性取决于本机防火墙和域内网络。

仅在另一台机器首次使用**空库**时执行以下装配；不覆盖已有 `.env`：

```powershell
if (-not (Test-Path .env)) { Copy-Item .env.example .env }
New-Item -ItemType Directory -Force data | Out-Null
go run ./cmd/migrate -initialize
go run ./cmd/seed
go run ./cmd/createuser -username <login> -password <chosen-password> -admin
./scripts/dev.ps1
```

## 工序映射表与启动同步

工序清单维护在 [`data/inspection_operations.csv`](data/inspection_operations.csv)，当前包含现场 13 道工序，按 `sort_no` 排序。新增工序只需追加行并重启后端，无需改代码或执行 seed。默认路径为 `./data/inspection_operations.csv`，可通过 `OPERATION_CATALOG_FILE` 指定其他文件；相对路径以进程工作目录为准，部署时须一并携带此文件。

CSV 使用 UTF-8（支持 BOM），固定表头 `code,label,sort_no,active`。`code` 是非空且唯一的稳定编码，`label` 是非空显示名称，`sort_no` 是整数，`active` 只接受 `true/false`。已有编码不能为改名而修改，请修改 label；ID 首次插入时自动生成，相同 code 重启后保留原 ID 和元数据。历史工序组名称快照保持原值。

每次启动在接受 HTTP 请求前完整校验文件，在单个事务中新增缺项、更新名称/顺序/启用状态，并保留审计。重复启动无变化时不增加审计事件。文件缺失、格式错误或数据库写入失败会拒绝启动，且不留下部分同步结果。表格中的字段优先于 API 对同一工序的维护；未列出的旧字典项保留，不自动删除或停用。停用须保留该行并设置 `active=false`，不会影响已有单据及历史。原有 `GENERAL` 保留以兼容旧数据，新空库不再自动创建该项。

查看完整目录：`GET /api/v2/dict/items?category=inspection_operation&include_inactive=true`。前端仍须使用返回的 `id` 创建工序组，不要将 code 当作 operation_id。

## 可选管理员初始化配置

`.env` 支持以下可选项，示例不包含可用的默认凭据：

| 配置项 | 规则 |
|---|---|
| `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASS` | 空用户库自动创建管理员；两项均空禁用，只填一项报错。已有用户库启动时全部忽略，绝不覆盖账号或密码 |
| `BOOTSTRAP_ADMIN_ACCOUNT_TYPE` | `NORMAL` 或 `SAMPLING`；创建时省略默认 NORMAL；显式同步已有账户时省略保持原身份 |
| `BOOTSTRAP_ADMIN_NAME` | 非空时指定显示名 |
| `BOOTSTRAP_ADMIN_EMP` | 非空时指定工号 |
| `BOOTSTRAP_ADMIN_DEPARTMENT` | 非空时指定部门文字 |

本机 `.env` 已从切换前私有配置恢复原引导登录名与密码，没有改动数据库内的账号名或密码。配置可以保留以便空库重建，但不会在每次启动时重置账户。

若要将身份（例如抽检 SAMPLING）或其他资料应用到已有管理员，修改对应非空项后显式执行：

```powershell
go run ./cmd/createuser -from-env
```

该命令按配置登录名定位管理员，保留已有 ID、登录名、密码、权限和启停状态，只同步非空资料；不变时不写入。身份改变会撤销该账号旧会话和领域幂等，需重新登录。若用户库非空但指定账号不存在，或指定的是普通用户，则拒绝，避免误建第二个管理员或提升权限。修改配置登录名不等于重命名已有账户。`-from-env` 不能与其他参数混用。

原 `-username/-password/-admin` 显式创建路径继续保留；已有账户显式改密仍需 `-update`，其权限/密码变更不属于配置同步规则。

初始化只接受空库或已匹配的当前结构；seed 只补缺项，不覆盖人工标签、别名、排序、停用状态、图号或已有账号密码。API 没有首次改密业务关卡。密码创建/重置为 8–128 字符，旧 PHC 哈希仍按原参数验证。

Cookie 写请求必须有配置允许的 Origin。CORS_ORIGINS 使用明确来源，不接受通配符；命令行可使用 Bearer。Cookie/Bearer 不同返回 400。请求体最多 4 MiB，HTTP 正文读取最长 15 秒、响应写入最长 30 秒；重复键、未知字段/参数、重复参数和多个 JSON 值拒绝。写事务最长 10 秒，数据库锁等待最多 5 秒，迁移最长 30 秒。

## 只读盘点、备份及受控重建

```powershell
go run ./cmd/migrate -status
```

输出驱动、无凭据目标位置、迁移、表名、数量、配置冲突、数据摘要与目标指纹，不输出密码哈希或 DSN。SQLite 状态连接使用 mode=ro，不创建缺失数据库或改变源库 journal 模式。

已知完整 `0006` 使用显式测试数据重建；已知 `0007`、`0008` 或 `0009` 使用非破坏性升级，最终均为 `0010`。早期版本、未知/缺损结构、迁移名称冲突、部门悬空引用、冲突工序配置和非法人工元数据拒绝。`0001～0007` 原文件不改写；首次引入旧迁移摘要的 provenance 明确为名称/结构基线，不宣称过去验证过文件哈希。0006 重建中的结构转换、账号/配置保全及维护回执同事务提交。

SQLite 先停写，生成并重新打开一致备份，核对数据摘要：

```powershell
New-Item -ItemType Directory -Force backups | Out-Null
go run ./cmd/backup -out backups/before.db -receipt backups/verified.json
# 使用 status 输出的当前指纹，不要复用其他目标的指纹。
go run ./cmd/migrate -rebuild-test-data -target-fingerprint <fingerprint> -verified-backup backups/verified.json
# 已知 0007、0008 或 0009 改用下列命令：
go run ./cmd/migrate -upgrade -target-fingerprint <fingerprint> -verified-backup backups/verified.json
```

backup 通过 SQLite VACUUM INTO 生成包含 WAL 数据的独立文件，源库以只读模式打开；禁止仅复制仍在写入的主文件。备份含账户哈希等私人数据，放在 Git/Docker 已排除的 backups 目录。重建会在取得数据库锁后再次核对数据摘要，源库与备份回执不一致则拒绝。

PostgreSQL 使用匹配服务端版本的 `pg_dump --format=custom` 做一致备份，再用 `pg_restore --exit-on-error` 恢复到独立空库（目标 schema 保持一致）。备份/恢复连接由环境或安全的本地凭据配置提供，不把带密码的 DSN 写入日志。设置 `LENS_BACKUP_RESTORE_DSN` 指向该恢复库后，执行 `go run ./cmd/backup -out <existing-dump-path> -receipt backups/verified.json`，工具比对源库与恢复库的数据摘要并生成同类回执，再执行显式重建命令。它拒绝把源库自身冒充恢复库。测试中的 Docker PostgreSQL 使用 pg_dump/pg_restore 真实验证这一过程。

重建保留账号 ID/密码/工号/权限/类型、部门文字、人工字典/图号、人工缓存确认、非检验旧审计及原迁移记录。旧组织目录另存 preserved_organization_units；相同工序的 ID 映射保存在 dictionary_id_mappings。人工解析确认保留并标记 legacy-unverified/needs_review，不冒充当前规则确认。清除测试检验记录、旧业务回放基线、会话、领域幂等及自动缓存。新历史起点在首个领域事务提交时建立，无虚构单据事件。

0008 为图号增加可空 is_rx 和序号唯一约束，并增加筛选投影/索引及共享目录版本。升级保留旧 false/true 值，不猜测其原始含义；新资料未提供 Rx 证据时保持 null。旧图号序号重复会使整次升级回滚，须先核对冲突，不能静默合并。普通 API/容器启动不会执行重建或升级；仅在空用户库按可选配置引导管理员。

0009 新增用户所属工序 `operation_id`。0010 新增 `user_operations` 多对多关联表，并将已有单工序值迁移为关联记录；管理员可在用户管理接口通过 `operation_ids` 为普通账户分配多个已启用工序，管理员默认可访问全部工序。升级会清除普通账户已有的填报幂等缓存。

重建/升级失败整体回滚；回退依赖停写时的完整备份及匹配的旧程序/配置，不提供有损 down 迁移。开发验证包含恢复后重开服务并复验历史报告。新版本产生真实资料后，不得用切换前备份直接覆盖。

## 当前结构的测试数据重置

```powershell
go run ./cmd/reset-testdata  # 只读预览
go run ./cmd/reset-testdata -confirm-test-data -target-fingerprint <preview-fingerprint>
```

必须先停写和备份。仅接受匹配的 `0010`，清检验业务/领域事件、会话、领域幂等及自动缓存，保留账号、目录、模板、人工确认和共享审计。不会清除人工缓存或改密码。

## 核心实现边界

- `ledger.Content / Merge / Derive / AggregateCounts / AggregateDefects`：统一可空字段、补丁、校验、提示与安全整数规则。
- `ledger.Write / Repository`：在同一事务创建批/组、保存/绑定、关闭/开放、取消及批量编排；Request 固定原始方法/路径/正文、操作者、幂等键及成功状态码。
- `store.BeginWrite / WriteTx.Event / Commit`：数据库写闸门、当前账号复查、持久逻辑时钟、事务末事件及同事务审计。
- `store.ReadView / Reader / Snapshot / Boundary`：一致读与完整事务上界；`ledger.Filter` 统一列表、统计及范围报告筛选，详情按页批量读取，历史报告按原子事件边界重放。
- `field_schemas / form_templates`：持久不可变版本定义；每份保存版本与创建工序/操作者证据绑定。普通接口没有改写历史版本或模板的入口。

## 本机全量验证（默认路径）

```powershell
./scripts/verify-local.ps1
# 可选：只把 PostgreSQL 作为一次性测试依赖；应用和 Go 测试仍在本机执行。
./scripts/verify-local.ps1 -WithPostgres
# 日常快速回归可跳过年度规模性能案例：
./scripts/verify-local.ps1 -SkipPerformance
```

脚本默认运行本机 SQLite 全包 race、vet、build 和年度规模性能测试。测试数据库使用临时文件或独立 schema，不向日常 `data/lens.db` 灌数；脚本暂时清除并在结束时恢复测试环境变量，防止误选数据库。`-WithPostgres` 需要 Docker Desktop，随机绑定本机端口、新建一次性 PostgreSQL 17 容器，运行原生 Go PostgreSQL 全包 race、真实备份恢复及性能测试，结束后删除容器。Windows race 检查需可用的 gcc/CGO。应用日常启动不需要 Docker。

原始命令：

```powershell
go test -race ./... -count=1
go vet ./...
go build ./...
# PostgreSQL 测试显式设置 LENS_TEST_POSTGRES_DSN；每个用例独占生成的临时 schema。
# 完整备份恢复案例再设置 LENS_TEST_PG_DOCKER_CONTAINER，指向一次性的 PostgreSQL 容器。
```

CI 在 Linux 运行两库全包 race 测试、vet、build。未配置 PostgreSQL 时，备份恢复专用案例明确 skip；不能把 SQLite 结果写成双库结果。既有本机及 Linux 验证环境、性能结果和验收映射见[阶段归档](../archive/2026-11-06/README.md)；远端 Actions 状态不冒充本地执行证据。

## 可选 Linux/容器交付复验

以下仅保留为跨平台验证入口，不作为本机后续开发/联调的默认路径。在 Docker Desktop/PowerShell 中复验 Linux 矩阵与年度规模性能：

```powershell
docker build -f Dockerfile.verify -t lens-qs:g2-verify .
./scripts/verify-linux.ps1
# 可选：若本机已有同依赖版本构建镜像，用 DEPENDENCY_IMAGE 构建参数复用模块缓存。
# 性能测试默认不随普通 go test 执行；验证脚本会显式运行。
docker build -t lens-qs:g2-development .
./scripts/verify-runtime.ps1
```

两个脚本只操作新建的测试业务容器/网络/卷，结束后清理；`lens-g2-go-cache` 仅保留可复用的编译缓存。Linux 恢复用例需将 Docker socket 交给验证容器，以操作脚本新建的 PostgreSQL 测试容器；脚本不使用现存业务数据库。年度夹具为 10 万张单、11 万版本，基于 SQL 批量生成，不能将其灌数耗时解释为业务批量接口吞吐或生产容量承诺。

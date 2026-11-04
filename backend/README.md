# lens-qs-api 后端

当前仍为旧业务实现：`forms.form_no` 全局唯一，`POST /api/forms` 按同号合并，状态 ACTIVE/VOID。**新产品批/工序组/批次/版本及 `/api/v2` 均未实现。** 本轮只更新文档，不改数据库或业务代码。

新的[需求](../docs/requirements.md)、[设计](../docs/design.md)、[API 契约](../docs/api.md)、[重建计划](../docs/rebuild-plan.md)、[迁移说明](../docs/migration.md)是后续实施目标。当前旧 API 详细说明见[归档文档](../archive/2026-11-03/legacy/docs/api.md)，以源码为准；旧运行文档完整副本见[归档 README](../archive/2026-11-03/legacy/backend/README.md)。

## 技术与运行

[go.mod](go.mod) 声明 Go 1.26.0；Gin＋sqlx；SQLite（modernc，无需 CGO）/PostgreSQL（pgx）；手写 SQL 迁移 0001–0004；本地密码与服务端会话。数据库日期/时间仍存 TEXT；v2 会统一标准格式，保留 SQLite，不强制换库。

从本目录运行：

```powershell
Copy-Item .env.example .env   # 仅首次创建，已有 .env 时不要覆盖
pwsh -File scripts/dev.ps1
```

运行前按实际环境配置 `.env`；环境变量优先于文件。脚本构建、迁移、写种子并启动服务，适用于现有旧模型。保留已有业务数据时不要使用脚本的 `-Reset` 参数，也不要执行 Makefile 的 clean 目标。

手动运行（每步分别执行）：

```powershell
go run ./cmd/migrate
go run ./cmd/seed
go run ./cmd/api
```

健康检查：`GET http://127.0.0.1:8080/healthz`。上述命令会运行当前实现，本轮未执行这些命令。Docker 部署可参照本目录 Dockerfile/docker-compose.yml 与归档运行说明，不代表 v2 已就绪。

## 配置与账户

| 配置 | 说明 |
|---|---|
| `DB_DRIVER/DB_DSN` | sqlite 或 postgres 及对应文件路径/连接串 |
| `APP_ADDR/APP_ENV` | 监听地址与环境 |
| `AUTO_MIGRATE` | 启动是否自动迁移；变更前先核对备份与目标库 |
| `SESSION_TTL/SESSION_IDLE_TTL` | 会话绝对/空闲有效期 |
| `COOKIE_NAME/COOKIE_SECURE` | Cookie 名与 HTTPS 标记 |
| `CORS_ORIGINS` | 允许的前端来源；留空不下发 CORS 头 |
| `BOOTSTRAP_ADMIN_USER/PASS` | 仅库中无用户时引导管理员，已有用户时不重置密码 |

完整示例见 [.env.example](.env.example)。账户不因文档重建而重建；已有密码和管理员标记需保留。浏览器 Cookie 请求需带 credentials，也可使用登录返回的 Bearer token。实际检验/纸面人名字段不能替代系统登录用户。

## 验证与目录

```powershell
go test ./...
go vet ./...
```

现有测试位于 internal 各模块，端到端脚本位于 scripts/e2e*.ps1。它们覆盖旧模型，不等于新产品批/整批互斥/正式版本口径验收。本轮是文档更新，未重跑业务测试。

- `cmd/`：api、migrate、seed、createuser。
- `internal/inspection/`：旧表单合并保存、明细、校验和历史。
- `internal/stats/`：旧统计；任意纸面日期的截取/比较需在 v2 改造。
- `internal/idempotency/`：旧幂等；v2 需业务同事务，不直接复用响应后缓存语义。
- `internal/formfields/`、`internal/operations/`：字段目录与工序模板，v2 需增加可空/只读/日期规范和模板版本。
- `internal/auth/`、`users/`、`dictionary/`、`sample/`：优先复用的基础能力。
- `migrations/{sqlite,postgres}/`：两套 SQL；新增迁移，保留已发布文件与历史数据。

后续开发按[重建计划](../docs/rebuild-plan.md) P1–P5 推进；不要以当前按 form_no 自动更新的 API 试录同号多工序新数据。

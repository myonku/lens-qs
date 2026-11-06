# 本机开发切换与验证记录

执行日期：2026-11-06（Asia/Shanghai）。本轮承接 G1/G2 已完成的后端，将后续开发和测试路径切换到 Windows 本机。仅作开发测试，没有部署、发布或配置域内代理。

## 本机装配结果

- 工作目录：`D:\Pr\lens-qs\backend`；Go `go1.27.1 windows/amd64`，CGO/gcc 可用。
- 应用原生运行：`bin/lens-qs-api.exe`，`APP_ENV=dev`，监听 `:8080`；访问 `http://localhost:8080`，API 前缀 `/api/v2`。
- 日常数据库仍为 `data/lens.db`（SQLite），已通过显式维护从 0006 重建到 0008。应用运行不需要 Docker。
- `.env` 保留原有有效参数，将通配来源改为 `http://localhost:8080,http://127.0.0.1:8080`，移除失效的自动迁移及旧引导账号配置。密码没有重置，原有账号可登录。
- 切换时普通启动只校验结构，不执行迁移、重建、seed 或账号创建；后续恢复的空库管理员引导例外见下节。已显式运行一次补缺 seed；18 条参考图号没有新增或更新。

## 备份与保全

切换前确认 8080 没有监听实例，旧库盘点无冲突。使用 `cmd/backup` 生成一致备份并重新打开比对摘要后，才执行带目标指纹及备份回执的 `cmd/migrate -rebuild-test-data`。

本机私有产物均位于已被 Git/Docker 排除的 `backend/backups/local-20261106/`：

- `before.db`：切换前 0006 一致备份。
- `verified.json`：备份重新打开并核对数据摘要的回执。
- `env.before`：切换前配置，包含旧本地凭据，不提交仓库。

核对结果：1 个账号的共有字段（含 ID、密码哈希及权限）逐项一致；18 条图号的共有字段逐项一致。旧字典 21 项规范化为 18 项，其中 3 个旧 `process` 项通过 `dictionary_id_mappings` 映射至工序目录；维护回执 1 条。旧检验单/组/生产批各 1 条及旧会话、领域幂等已按既有测试数据授权清理；重建后业务表为空。SQLite `integrity_check=ok`，`foreign_key_check` 无异常。

备份用于本次切换回退；产生后续联调数据后，不能直接用旧备份覆盖新库。

## 本机验证

执行入口为 `backend/scripts/verify-local.ps1 -WithPostgres`。Go 编译、测试和 HTTP 服务均在 Windows 本机运行；仅 PostgreSQL 17 为临时 Docker 测试依赖，随机绑定回环端口，验证结束已删除。测试使用临时 SQLite 文件和独立 PostgreSQL schema，没有向日常库灌入年度性能夹具。

| 项目 | 结果 |
|---|---|
| SQLite `go test -race ./... -count=1` | 通过 |
| PostgreSQL `go test -race ./... -count=1` | 通过，已提供恢复测试所需的容器变量 |
| `go vet ./...`、`go build ./...` | 本机通过 |
| SQLite/PG 年度规模性能案例 | 均显式执行并通过 |
| SQLite 一致备份及恢复后历史报告 | 测试通过 |
| PG 旧库备份恢复重建、业务备份恢复报告 | `pg_dump`/`pg_restore` 真实测试通过 |
| 真实本机服务 `/`、`/healthz` | 200，健康状态 ok |
| 现有账号登录、`auth/me` | 200，原密码仍有效 |
| 单位、字典、字段、模板、图号、生产批、检验单及统计读取 | 200 |
| 本地 Origin 的 OPTIONS 预检 | 204，允许准确来源及 credentials |
| 非允许 Origin 的 Cookie 退出请求 | 403 |
| 允许 Origin 的 Cookie 退出及退出后认证 | 200 / 401 |

普通 SQLite 全包运行中的 PostgreSQL 专用恢复案例和显式性能案例按设计跳过；前者在 PG 全包矩阵运行，后者在两库分别显式运行，未将跳过记作通过。无测试文件的包仅验证编译。

年度夹具包含 100001 张单、110001 版本及 110001 行；16 并发保存、96 次保存样本。以下为本次实测 P95，单位 ms：

| 测量 | SQLite | PostgreSQL |
|---|---:|---:|
| 保存 | 61.59 | 392.11 |
| 千笔范围查询 / 每页 100 条 | 58.45 | 52.14 |
| 十万单汇总 | 877.02 | 581.86 |
| 千笔缺陷汇总 | 86.17 | 76.14 |
| 百单生产批历史报告 | 14.26 | 85.75 |

这是本机开发环境的一轮结果，PG 经过 Docker Desktop 端口转发，不能当作生产容量承诺。原始本机输出在 `backend/bin/local-verification.log`；独立 SQLite JSON 测试输出在 `backend/bin/local-sqlite-tests.jsonl`，均为本机忽略产物。

## 后续入口与边界

日常启动：在 `backend` 执行 `./scripts/dev.ps1`，后台启动使用 `-NoWait`，现有服务重新启动前先停止旧实例。日常全量回归执行 `./scripts/verify-local.ps1`；需要双库回归时加 `-WithPostgres`，快速回归可加 `-SkipPerformance`。更多命令见[后端说明](../backend/README.md)。

当前仅验证本机 HTTP 和配置允许的本机 Origin；尚无前端应用接入，也未验证其他域内机器、防火墙或实际域名。后续域内联调需将真实前端 Origin 加入 `.env` 后重启，并从目标客户端验证网络与浏览器请求。

## 管理员初始化配置补回

同日按用户要求恢复可选 `BOOTSTRAP_ADMIN_*` 配置。本机引导登录名及密码从 `env.before` 原值追回，未写入文档、日志或版本控制；数据库内已有账号与密码保持。新增 ACCOUNT_TYPE、NAME、EMP、DEPARTMENT 可选资料配置，未指定时不改变现有身份或资料。

普通启动只对空用户库自动引导；用户库非空时全部忽略，保护既有账号。初始化的空库判断在写闸门内再次核对，防止多实例并发创建多个管理员。`cmd/createuser -from-env` 提供显式资料同步，已有登录名和密码不覆盖；原显式创建/改密路径保留。详见[后端说明](../backend/README.md)与[公共设计](common-design.md)。此前移除所有引导配置属于重建时收紧启动副作用的实现选择，并非必须删除配置入口。

补回后再次运行 `verify-local.ps1 -WithPostgres -SkipPerformance`：两库全包 race、vet、build 均通过，包含新增的空库/非空库、并发引导、资料同步密码保全和身份变更会话撤销用例；本轮未重复性能测试。日志为 `backend/bin/bootstrap-verification.log`，临时 PG 容器已清理。本机已执行 `createuser -from-env` 并重启 8080 服务，健康检查通过；对照切换前备份，账号 ID、登录名及密码哈希完全一致，恢复的原配置凭据登录及退出通过。

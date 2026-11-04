# lens-qs 后端 v2

Go 1.26+，Gin/sqlx，SQLite及PostgreSQL。SQLite连接默认内存临时存储，避免Windows临时文件创建失败。所有API前缀 `/api/v2`，健康检查 `/healthz`，根路径显示2.0.0。接口详见[正式契约](../docs/api.md)。

```powershell
Copy-Item .env.example .env  # 首次建立配置；已有.env不要覆盖
go run ./cmd/migrate
go run ./cmd/seed -skip-migrate
go run ./cmd/api
```

已有账户和密码保持，空用户库才按BOOTSTRAP_ADMIN_USER/PASS建立管理员。创建抽检账户：

```powershell
go run ./cmd/createuser -username sampler -password '<your-password>' -account-type SAMPLING
```

管理员也可通过POST /api/v2/users或PATCH /api/v2/users/{id}维护account_type。NORMAL/SAMPLING是填报权限，is_admin是账户管理权限，二者独立。前端使用登录/me的submission_inspection_kind固定填报标记；服务端对目标组/单逐笔校验。修改账户类型使该用户会话及幂等缓存失效。

新增报告：

- `GET /api/v2/reports/product-lots/{id}?as_of=<RFC3339-seconds>&through_event_id=<optional>`：系统时点全工序状态/数据/良率，事件序号可固定同秒视图。
- `GET /api/v2/reports/operations/{operation_id}?from=2026-11-01&to=2026-11-30&product_lot_id=<optional>`：工序范围明细与完整筛选范围小计。

默认NORMAL，SAMPLING单独查询，ALL分类型。时点报告不混入后续修订；范围报告分页不截断小计。

旧测试数据可清除，但必须先备份停写：

```powershell
go run ./cmd/migrate                         # 0006删除旧业务表
go run ./cmd/reset-testdata -confirm-test-data # 只在v2内容全为测试时执行
```

保留账户密码与字典，清理后需重新登录。没有旧接口别名、旧按号upsert、旧迁移导入命令或双域开关。迁移不可恢复旧业务数据，详见[切换说明](../docs/migration.md)。

```powershell
go test -race ./...
go vet ./...
go build ./...
# 指向可创建随机schema的隔离PostgreSQL测试库
go test -race ./internal/ledger -count=1 # 先设置 LENS_TEST_POSTGRES_DSN 才启用PostgreSQL用例
```

容器：`docker compose up -d --build`。可用 `docker build --build-arg GOPROXY=https://goproxy.cn -t lens-qs:v2 .` 选择构建代理。镜像只带CSV参考资料，不包含本地库/备份/.env，重启不会重设已有管理员密码。

本地脚本 `pwsh -File scripts/dev.ps1 -NoWait`；可选 `-ResetTestData`显式清测试业务并保留账户，旧删库 `-Reset` 已移除。CI覆盖SQLite和PostgreSQL，主分支阶段与验收记录见[全局进度](../docs/progress.md)，调用方变更见[发布说明](../docs/v2-release.md)。

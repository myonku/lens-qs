# 后端工程

本目录是现有Go工程。v2的业务与接口目标由[需求](../docs/requirements.md)、[系统设计](../docs/design.md)和[API](../docs/api.md)定义；现有实现可能滞后，不能以代码行为补充或覆盖文档规则。本轮仅整理文档，未验证运行、结构迁移或部署。

## 本地开发命令

以下是工程入口备忘，在backend目录、隔离的开发数据库上使用；不是部署或数据升级方案。

```powershell
Copy-Item .env.example .env  # 仅首次配置，已有文件不要覆盖
# 配置隔离开发库后，再选择执行结构迁移和参考资料初始化
# go run ./cmd/migrate
# go run ./cmd/seed -skip-migrate
go run ./cmd/api
```

数据库结构和工具适配须依据目标设计单独核验，不根据文档版本假定现有迁移已完成。账户、密码及检验历史需保全，业务数据不随服务启动清空。

## 工程检查

```powershell
go test ./...
go vet ./...
go build ./...
```

这些命令通过只说明相应工程检查结果；业务验收仍须覆盖需求中的保存即生效、固定“片”单位、抽检LOSS拒绝及独立工序填报等规则。安全、会话、双库一致性、备份和性能要求见[公共设计](../docs/common-design.md)。

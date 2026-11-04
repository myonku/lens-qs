# lens-qs — 无纸化检验台账 v2

Go 后端，SQLite/PostgreSQL 双库。按产品批→工序/检验类型填报组→检验单→不可覆盖版本组织资料，支持普通/抽检账户权限隔离、独立填报、修订、正式统计、历史时点及工序时间范围报告。

所有认证、账户、字典、解析、业务及报告接口统一 `/api/v2`；旧 `/api` 契约与业务实现已移除。`GET /healthz` 独立提供健康检查。普通账户只能提交 NORMAL，抽检账户只能提交 SAMPLING，管理员也受填报类型限制。

- [需求](docs/requirements.md)
- [设计](docs/design.md)
- [正式 API](docs/api.md)
- [迁移与旧数据清理](docs/migration.md)
- [功能状态](docs/features.md)
- [本次发布与验收](docs/v2-release.md)
- [后端启动](backend/README.md)

历史文件保留在 archive 和历史施工记录中，仅用于追溯，不代表当前接口。日期继续 TEXT，业务时区 Asia/Shanghai；未填计数不补零，不自动推导库存/损耗。

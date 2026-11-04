# v2 正式切换与数据清理

2026-11-04，版本2.0.0。当前选择为正式切换，旧测试业务可清除；不再提供旧协议兼容或人工旧单导入工具。

1. 停止目标环境旧服务和调用方写入；确认目标库及备份路径。本地库不代表生产。SQLite用backup API，PostgreSQL用pg_dump，验证备份可读。保留账户ID、密码哈希、管理员标记和字典。
2. 用新版本执行 `go run ./cmd/migrate`，顺序应用0001–0006。已发布0001–0005不改，0006新增account_type（旧账户默认NORMAL）、保存理由字段、历史起点/基线审计，删除旧forms/form_lines/form_history/旧幂等/迁移候选表。这一步会丢弃旧业务数据，不能靠down迁移恢复。
3. 如目标库的v2内容也全部是测试数据，显式执行 `go run ./cmd/reset-testdata -confirm-test-data`。命令保留账户及字典，清业务、审计、幂等、会话与解析缓存，并更新历史起点；服务启动不会自动清库。账号需要重新登录，密码不变。维护命令要求先停写、先备份，不能用在需要保留的正式资料上。
4. 前端全部调用 `/api/v2`，含auth/users/dict/units/sample；删除旧forms/upsert、旧统计与client_request_id等兼容分支。账户创建/维护可传account_type=SAMPLING；登录/me的submission_inspection_kind决定前端固定填报标记。
5. 验证正常/抽检跨类型写入403、同类型全生命周期、统计分区、历史时点/事件序号与工序范围报告。历史查询从history_started_at开始，之前返回history_unavailable；不能伪造旧模型历史。
6. 新服务启动并验证healthz及根版本2.0.0。旧路径404，不再有BUSINESS_WRITE_DOMAIN/旧幂等关闭开关。若已有新正式写入，恢复前先保全这些新数据，禁止直接覆盖为旧备份。

SQLite与PostgreSQL均继续TEXT日期/时点；不切换原生日期库。归档和历史施工记录保留，但不再指导当前接口。当前本地实际迁移/清理结果见[发布记录](v2-release.md)，不得推断其他环境已执行。

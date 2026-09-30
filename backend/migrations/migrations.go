// Package migrations 内嵌 SQL 迁移脚本。
//
// 目录结构：
//
//	migrations/sqlite/0001_init.sqlite.up.sql
//	migrations/postgres/0001_init.postgres.up.sql
//
// 命名约定：<版本号>_<描述>.<驱动>.up.sql / .down.sql
// 迁移执行器见 internal/store/migrate.go。
package migrations

import "embed"

//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS

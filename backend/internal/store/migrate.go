// Package store 提供数据库迁移执行器。
package store

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/migrations"
)

// Migrate 应用尚未执行的迁移。幂等：已应用过的版本会跳过。
func Migrate(ctx context.Context, db *sqlx.DB, driver string) (applied []string, err error) {
	return MigrateFrom(ctx, db, driver, "")
}

// MigrateFrom 与 Migrate 相同，但允许指定迁移文件所在目录
// （默认 migrations/<driver>；传 dir 可覆盖，便于测试或在异目录运行）。
func MigrateFrom(ctx context.Context, db *sqlx.DB, driver, dir string) (applied []string, err error) {
	if driver != "sqlite" && driver != "postgres" {
		return nil, fmt.Errorf("不支持的迁移驱动: %q", driver)
	}
	if err := ensureMigrationTable(ctx, db); err != nil {
		return nil, err
	}
	done, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	if dir == "" {
		// 注意：go:embed 会把"包含 embed 指令的目录"作为根，
		// 因此 migrations.FS 里是 "sqlite/..."、"postgres/..."，不带 migrations 前缀。
		dir = driver
	}
	entries, err := fs.ReadDir(migrations.FS, dir)
	if err != nil {
		return nil, fmt.Errorf("读取迁移目录失败: %w", err)
	}

	type item struct {
		version int
		name    string
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		v, err := versionOf(e.Name())
		if err != nil {
			return nil, err
		}
		items = append(items, item{version: v, name: e.Name()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].version < items[j].version })

	for _, it := range items {
		if done[it.version] {
			continue
		}
		raw, err := migrations.FS.ReadFile(path.Join(dir, it.name))
		if err != nil {
			return nil, fmt.Errorf("读取迁移 %s 失败: %w", it.name, err)
		}
		statements := splitStatements(string(raw))
		if err := applyMigration(ctx, db, it.version, it.name, statements); err != nil {
			return applied, err
		}
		applied = append(applied, it.name)
	}
	return applied, nil
}

func ensureMigrationTable(ctx context.Context, db *sqlx.DB) error {
	ddl := `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  applied_at TEXT NOT NULL
)`
	_, err := db.ExecContext(ctx, ddl)
	return err
}

func appliedVersions(ctx context.Context, db *sqlx.DB) (map[int]bool, error) {
	var versions []int
	if err := db.SelectContext(ctx, &versions, `SELECT version FROM schema_migrations`); err != nil {
		return nil, fmt.Errorf("读取已应用迁移失败: %w", err)
	}
	out := make(map[int]bool, len(versions))
	for _, v := range versions {
		out[v] = true
	}
	return out, nil
}

func applyMigration(ctx context.Context, db *sqlx.DB, version int, name string, statements []string) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("执行迁移 %s 失败: %w\n语句: %s", name, err, truncate(stmt, 200))
		}
	}
	if _, err := tx.ExecContext(ctx,
		db.Rebind(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`),
		version, name, nowText()); err != nil {
		return fmt.Errorf("记录迁移版本失败: %w", err)
	}
	return tx.Commit()
}

func versionOf(filename string) (int, error) {
	i := strings.IndexByte(filename, '_')
	if i <= 0 {
		return 0, fmt.Errorf("迁移文件名不符合 <版本号>_<描述> 约定: %s", filename)
	}
	v, err := strconv.Atoi(filename[:i])
	if err != nil {
		return 0, fmt.Errorf("迁移文件版本号不是数字: %s", filename)
	}
	return v, nil
}

// splitStatements 按分号切分 SQL，跳过注释行；够用即可（本项目迁移不含存储过程）。
func splitStatements(sqlText string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || trimmed == "" {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			if stmt != "" {
				out = append(out, stmt)
			}
			cur.Reset()
		}
	}
	if stmt := strings.TrimSpace(cur.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

package store

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// AppliedMigration 描述一条已应用的迁移。
type AppliedMigration struct {
	Version   int    `db:"version" json:"version"`
	Name      string `db:"name" json:"name"`
	AppliedAt string `db:"applied_at" json:"applied_at"`
}

// AppliedMigrations 返回已应用的迁移列表（按版本升序）。
func AppliedMigrations(ctx context.Context, db *sqlx.DB) ([]AppliedMigration, error) {
	var exists int
	q := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`
	if db.DriverName() == "pgx" {
		q = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='schema_migrations'`
	}
	if err := db.GetContext(ctx, &exists, q); err != nil {
		return nil, err
	}
	if exists == 0 {
		return []AppliedMigration{}, nil
	}
	var out []AppliedMigration
	if err := db.SelectContext(ctx, &out,
		`SELECT version, name, applied_at FROM schema_migrations ORDER BY version`); err != nil {
		return nil, fmt.Errorf("读取迁移版本失败: %w", err)
	}
	return out, nil
}

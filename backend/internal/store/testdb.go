package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// NewTestDB 创建一个临时 SQLite 库并执行全部迁移，用于测试。
func NewTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sqlx.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if err := Initialize(context.Background(), db, "sqlite"); err != nil {
		t.Fatalf("执行迁移失败: %v", err)
	}
	return db
}

// Package database 负责打开数据库连接。
//
// 支持的驱动：
//   - sqlite   ：modernc.org/sqlite（纯 Go，无需 CGO），适合开发与单机部署
//   - postgres ：github.com/jackc/pgx/v5/stdlib
//
// 说明：两套驱动都把"时间"当作文本处理（写入 RFC3339，读取为 string），
// 以保证 SQLite 与 PostgreSQL 行为一致。需要按时间范围查询时用字符串比较即可
// （RFC3339 在本项目的写入口径下可安全字典序比较，见时间统一为同一时区/UTC）。
package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres 驱动
	_ "modernc.org/sqlite"             // sqlite 驱动（纯 Go）
)

// Open 打开数据库并验证连通性。
func Open(driver, dsn string, maxOpen, maxIdle int) (*sqlx.DB, error) {
	switch driver {
	case "sqlite":
		return openSQLite(dsn, maxOpen, maxIdle)
	case "postgres", "postgresql":
		return openPostgres(dsn, maxOpen, maxIdle)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %q", driver)
	}
}

func openSQLite(path string, maxOpen, maxIdle int) (*sqlx.DB, error) {
	// 纯 Go 驱动不支持多写连接；限制为单写连接避免 SQLITE_BUSY。
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite 失败: %w", err)
	}
	if maxOpen > 1 {
		maxOpen = 1
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(0)
	if err := ping(db); err != nil {
		return nil, err
	}
	return db, nil
}

func openPostgres(dsn string, maxOpen, maxIdle int) (*sqlx.DB, error) {
	db, err := sqlx.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 postgres 失败: %w", err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(time.Hour)
	if err := ping(db); err != nil {
		return nil, err
	}
	return db, nil
}

func ping(db *sqlx.DB) error {
	done := make(chan error, 1)
	go func() { done <- db.Ping() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("数据库连接不可用: %w", err)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("数据库连接超时")
	}
}

// Tx 是事务句柄的别名，便于各 store 使用统一签名。
type Tx = sqlx.Tx

// DB 是数据库句柄的别名。
type DB = sqlx.DB

// 保证 database/sql 被引用（各 store 会用到 sql.ErrNoRows 等）。
var _ = sql.ErrNoRows

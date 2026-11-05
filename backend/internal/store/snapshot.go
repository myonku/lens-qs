package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

type Snapshot struct {
	AsofTime         string  `json:"asof_time"`
	AsofEventID      int64   `json:"asof_event_id"`
	HistoryStartedAt *string `json:"history_started_at"`
}

// ReadView fixes metadata and subsequent selections on one database snapshot.
// PostgreSQL uses REPEATABLE READ; SQLite's first SELECT fixes its read view.
// Consumers receive a reader only and cannot accidentally issue writes.
type Reader interface {
	GetContext(context.Context, any, string, ...any) error
	SelectContext(context.Context, any, string, ...any) error
	QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error)
	Rebind(string) string
}

func ReadView(ctx context.Context, db *sqlx.DB, fn func(Reader, Snapshot) error) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	options := &sql.TxOptions{ReadOnly: true}
	if db.DriverName() == "pgx" {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, e := db.BeginTxx(ctx, options)
	if e != nil {
		return Snapshot{}, e
	}
	defer tx.Rollback()
	var snapshot Snapshot
	var gate string
	if e = tx.QueryRowxContext(ctx, `SELECT g.last_event_time,r.history_started_at FROM write_gate g JOIN rebuild_state r ON g.id=r.id WHERE g.id=1`).Scan(&gate, &snapshot.HistoryStartedAt); e != nil {
		return snapshot, e
	}
	snapshot.AsofTime = time.Now().UTC().Format(time.RFC3339)
	if gate > snapshot.AsofTime {
		snapshot.AsofTime = gate
	}
	if e = tx.GetContext(ctx, &snapshot.AsofEventID, `SELECT COALESCE(MAX(last_event_id),0) FROM event_transactions WHERE domain=1`); e != nil {
		return snapshot, e
	}
	if e = fn(tx, snapshot); e != nil {
		return snapshot, e
	}
	return snapshot, tx.Commit()
}

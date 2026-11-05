package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
)

type Actor struct {
	ID     string `db:"id"`
	Name   string `db:"display_name"`
	Kind   string `db:"account_type"`
	Status string `db:"status"`
	Admin  bool   `db:"is_admin"`
}
type actorKey struct{}

func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

type WriteTx struct {
	*sqlx.Tx
	Context  context.Context
	cancel   context.CancelFunc
	ID, Time string
	Actor    Actor
	domain   bool
	last     int64
}

func BeginWrite(ctx context.Context, db *sqlx.DB, actor string, domain bool, clock func() time.Time) (*WriteTx, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	tx, e := db.BeginTxx(ctx, nil)
	if e != nil {
		cancel()
		return nil, StorageError(e)
	}
	fail := func(e error) (*WriteTx, error) { tx.Rollback(); cancel(); return nil, StorageError(e) }
	if db.DriverName() == "pgx" {
		if _, e = tx.ExecContext(ctx, `SET LOCAL lock_timeout = '5s'`); e != nil {
			return fail(e)
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE write_gate SET last_event_time=last_event_time WHERE id=1`); e != nil {
		return fail(e)
	}
	w := &WriteTx{Tx: tx, Context: ctx, cancel: cancel, ID: id.New(), domain: domain}
	if actor == "" {
		actor, _ = ctx.Value(actorKey{}).(string)
	}
	if actor != "" {
		if e = tx.GetContext(ctx, &w.Actor, tx.Rebind(`SELECT id,display_name,account_type,status,is_admin FROM users WHERE id=?`), actor); e != nil {
			return fail(httpx.Unauthorized(""))
		}
		if w.Actor.Status != "active" {
			return fail(httpx.Unauthorized(""))
		}
	}
	if clock == nil {
		clock = time.Now
	}
	w.Time = clock().UTC().Format(time.RFC3339)
	var last string
	if e = tx.GetContext(ctx, &last, `SELECT last_event_time FROM write_gate WHERE id=1`); e != nil {
		return fail(e)
	}
	if last > w.Time {
		w.Time = last
	}
	return w, nil
}
func (w *WriteTx) Close() { _ = w.Tx.Rollback(); w.cancel() }
func (w *WriteTx) Event(action, entity, entityID string, before, after any, reason *string) error {
	if w.last == 0 {
		if _, e := w.ExecContext(w.Context, w.Rebind(`INSERT INTO event_transactions(id,domain,created_at) VALUES(?,?,?)`), w.ID, BoolInt(w.domain), w.Time); e != nil {
			return e
		}
	}
	encode := func(v any) any {
		if v == nil {
			return nil
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	q := `INSERT INTO business_events(transaction_id,actor_id,actor_name,action,entity,entity_id,before_json,after_json,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING event_id`
	return w.GetContext(w.Context, &w.last, w.Rebind(q), w.ID, w.Actor.ID, w.Actor.Name, action, entity, entityID, encode(before), encode(after), reason, w.Time)
}
func (w *WriteTx) Commit() error {
	if w.last > 0 {
		if w.domain {
			if _, e := w.ExecContext(w.Context, w.Rebind(`UPDATE rebuild_state SET history_started_at=COALESCE(history_started_at,?) WHERE id=1`), w.Time); e != nil {
				return e
			}
		}
		if _, e := w.ExecContext(w.Context, w.Rebind(`UPDATE event_transactions SET last_event_id=? WHERE id=?`), w.last, w.ID); e != nil {
			return e
		}
		if _, e := w.ExecContext(w.Context, w.Rebind(`UPDATE write_gate SET last_event_time=? WHERE id=1`), w.Time); e != nil {
			return e
		}
	}
	return StorageError(w.Tx.Commit())
}
func StorageError(err error) error {
	if err == nil {
		return nil
	}
	var h *httpx.Error
	if errors.As(err, &h) {
		return err
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "unique constraint") || strings.Contains(s, "duplicate key") {
		return httpx.Conflict("唯一值冲突", map[string]string{"reason": "unique_conflict"})
	}
	if strings.Contains(s, "locked") || strings.Contains(s, "busy") || strings.Contains(s, "lock timeout") || strings.Contains(s, "connection refused") || errors.Is(err, context.DeadlineExceeded) {
		return httpx.NewError(503, "temporarily_unavailable", "存储暂不可用", nil)
	}
	return fmt.Errorf("storage operation: %w", err)
}

// Boundary rejects intermediate and shared-maintenance event IDs. It can be
// called inside a repeatable read transaction by the round-two snapshot selector.
func Boundary(ctx context.Context, q sqlx.QueryerContext, eventID int64, at string) error {
	var n int
	e := sqlx.GetContext(ctx, q, &n, sqlx.Rebind(sqlx.BindType(driverOf(q)), `SELECT COUNT(*) FROM event_transactions WHERE domain=1 AND last_event_id=? AND created_at<=?`), eventID, at)
	if e != nil {
		return e
	}
	if n != 1 {
		return httpx.Validation("invalid_history_boundary", map[string]string{"reason": "invalid_history_boundary"})
	}
	return nil
}
func driverOf(q sqlx.QueryerContext) string {
	switch x := q.(type) {
	case *sqlx.DB:
		return x.DriverName()
	case *sqlx.Tx:
		return x.DriverName()
	}
	return "sqlite"
}

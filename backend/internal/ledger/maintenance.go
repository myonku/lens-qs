package ledger

import (
	"context"
	"net/url"
	"time"
)

func (u *unit) auditList(q url.Values) (any, error) {
	if e := queryAllowed(q, "entity", "entity_id", "page", "page_size"); e != nil {
		return nil, e
	}
	if _, _, e := pagination(q); e != nil {
		return nil, e
	}
	query := `SELECT id,actor_id,action,entity,entity_id,"before","after",detail,created_at FROM audit_logs WHERE 1=1`
	args := []any{}
	for _, key := range []string{"entity", "entity_id"} {
		if q.Has(key) {
			query += " AND " + key + " = ?"
			args = append(args, q.Get(key))
		}
	}
	return page(u.rows(query+" ORDER BY id DESC", args...), q)
}

// ResetTestData is an explicit maintenance operation. It is never called at startup.
// It preserves users/password hashes and reference dictionaries. Sessions are revoked
// because old tokens and idempotent successes must not refer to erased business data.
func (s *Service) ResetTestData(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	u := &unit{tx: tx, ctx: ctx}
	u.exec("UPDATE v2_write_lock SET version = version + 1 WHERE id = 1")
	u.exec("UPDATE inspection_submissions SET current_revision_id = NULL, draft_revision_id = NULL")
	for _, table := range []string{"quantity_changes", "revision_lines", "submission_slots", "submission_revisions", "inspection_submissions", "operation_groups", "product_lots", "v2_idempotency_requests", "audit_logs", "sessions", "sample_parse_cache"} {
		u.exec("DELETE FROM " + table)
	}
	u.exec("UPDATE v2_release_state SET history_started_at = ? WHERE id = 1", now())
	if u.err != nil {
		return u.err
	}
	return tx.Commit()
}

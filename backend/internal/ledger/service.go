// Package ledger implements the v2 inspection domain independently of legacy form upserts.
package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
)

type Service struct{ db *sqlx.DB }

func New(db *sqlx.DB) *Service { return &Service{db: db} }

type unit struct {
	tx    *sqlx.Tx
	ctx   context.Context
	err   error
	actor string
}

func (u *unit) exec(q string, args ...any) {
	if u.err != nil {
		return
	}
	_, u.err = u.tx.ExecContext(u.ctx, u.tx.Rebind(q), args...)
}
func (u *unit) rows(q string, args ...any) []Object {
	out := []Object{}
	if u.err != nil {
		return out
	}
	rs, e := u.tx.QueryxContext(u.ctx, u.tx.Rebind(q), args...)
	if e != nil {
		u.err = e
		return out
	}
	defer rs.Close()
	for rs.Next() {
		m := Object{}
		if e = rs.MapScan(m); e != nil {
			u.err = e
			return out
		}
		for k, v := range m {
			if b, ok := v.([]byte); ok {
				m[k] = string(b)
			}
		}
		out = append(out, m)
	}
	u.err = rs.Err()
	return out
}
func (u *unit) get(q string, args ...any) Object {
	r := u.rows(q, args...)
	if len(r) == 0 {
		return nil
	}
	return r[0]
}
func (u *unit) must(table, key string) (Object, error) {
	m := u.get("SELECT * FROM "+table+" WHERE id = ?", key)
	if u.err != nil {
		return nil, u.err
	}
	if m == nil {
		return nil, httpx.NotFound("")
	}
	return m, nil
}
func (u *unit) insert(table string, m Object) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]any, len(keys))
	ph := make([]string, len(keys))
	for i, k := range keys {
		args[i] = m[k]
		ph[i] = "?"
	}
	u.exec("INSERT INTO "+table+" ("+strings.Join(keys, ",")+") VALUES ("+strings.Join(ph, ",")+")", args...)
}
func (u *unit) update(table string, m Object) {
	keys := []string{}
	for k := range m {
		if k != "id" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	args := []any{}
	set := []string{}
	for _, k := range keys {
		set = append(set, k+" = ?")
		args = append(args, m[k])
	}
	args = append(args, m["id"])
	u.exec("UPDATE "+table+" SET "+strings.Join(set, ",")+" WHERE id = ?", args...)
}
func (u *unit) audit(entity, action string, before, after Object, reason string) {
	var key any
	if after != nil {
		key = after["id"]
	} else if before != nil {
		key = before["id"]
	}
	u.exec(`INSERT INTO audit_logs(actor_id,action,entity,entity_id,"before","after",detail,created_at) VALUES(?,?,?,?,?,?,?,?)`, u.actor, action, entity, key, stringify(before), stringify(after), reason, now())
}
func version(m, req Object) error {
	n, ok := integer(req["expected_version"])
	if !ok || n < 1 {
		return httpx.BadRequest("expected_version 必须为正整数")
	}
	if n != num(m["version"]) {
		return conflict("version_conflict", Object{"current_version": m["version"], "id": m["id"]})
	}
	return nil
}
func bump(m Object) { m["version"] = num(m["version"]) + 1 }
func storageError(err error) error {
	var e *httpx.Error
	if errors.As(err, &e) {
		return err
	}
	return httpx.NewError(503, "temporarily_unavailable", "数据库暂不可用，请使用原幂等键重试", nil)
}

// Write holds a database-wide v2 write gate before any business read. This is a
// deliberate conservative strategy for both databases: it works across processes,
// bounds lock waits and includes the idempotency result in the same commit.
func (s *Service) Write(ctx context.Context, actor, key, fingerprint string, fn func(*unit) (any, int, error)) ([]byte, int, bool, error) {
	if strings.TrimSpace(key) == "" || len(key) > 200 {
		return nil, 0, false, httpx.BadRequest("必须提供长度不超过 200 的 X-Idempotency-Key")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return nil, 0, false, storageError(e)
	}
	defer tx.Rollback()
	u := &unit{tx: tx, ctx: ctx, actor: actor}
	if s.db.DriverName() == "pgx" {
		u.exec("SET LOCAL lock_timeout = '5s'")
	}
	u.exec("UPDATE v2_write_lock SET version = version + 1 WHERE id = 1")
	if u.err != nil {
		return nil, 0, false, storageError(u.err)
	}
	old := u.get("SELECT * FROM v2_idempotency_requests WHERE actor_id = ? AND idempotency_key = ?", actor, key)
	if u.err != nil {
		return nil, 0, false, storageError(u.err)
	}
	if old != nil && str(old["expires_at"]) > now() {
		if old["fingerprint"] != fingerprint {
			return nil, 0, false, conflict("idempotency_key_reused", nil)
		}
		return []byte(str(old["response_body"])), int(num(old["response_status"])), true, nil
	}
	if old != nil {
		u.exec("DELETE FROM v2_idempotency_requests WHERE actor_id = ? AND idempotency_key = ?", actor, key)
	}
	data, status, e := fn(u)
	if u.err != nil {
		return nil, 0, false, storageError(u.err)
	}
	if e != nil {
		return nil, 0, false, e
	}
	b, e := json.Marshal(Object{"code": "ok", "message": "", "data": data})
	if e != nil {
		return nil, 0, false, e
	}
	u.insert("v2_idempotency_requests", Object{"actor_id": actor, "idempotency_key": key, "fingerprint": fingerprint, "response_status": status, "response_body": string(b), "created_at": now(), "expires_at": time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)})
	if u.err != nil {
		return nil, 0, false, storageError(u.err)
	}
	if e = tx.Commit(); e != nil {
		return nil, 0, false, storageError(e)
	}
	return b, status, false, nil
}
func fingerprint(method, path string, b []byte) string {
	h := sha256.New()
	h.Write([]byte(method + "\n" + path + "\n"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Service) read(ctx context.Context, fn func(*unit) (any, error)) (any, error) {
	opts := &sql.TxOptions{ReadOnly: true}
	if s.db.DriverName() == "pgx" {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, e := s.db.BeginTxx(ctx, opts)
	if e != nil {
		return nil, storageError(e)
	}
	defer tx.Rollback()
	u := &unit{tx: tx, ctx: ctx}
	v, e := fn(u)
	if u.err != nil {
		return nil, storageError(u.err)
	}
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, storageError(e)
	}
	return v, nil
}

func (u *unit) createLot(req Object) (any, error) {
	if e := allowed(req, "shared_form_no", "sample_identity", "sample_raw", "sample_project", "sample_model", "sample_spec", "sample_size", "sample_side"); e != nil {
		return nil, e
	}
	for _, f := range []string{"shared_form_no", "sample_identity"} {
		if e := requiredString(req, f); e != nil {
			return nil, e
		}
		req[f] = strings.TrimSpace(str(req[f]))
	}
	for k, v := range req {
		if v != nil {
			if _, ok := v.(string); !ok {
				return nil, formatErr(k)
			}
		}
	}
	if old := u.get("SELECT id FROM product_lots WHERE shared_form_no = ? AND sample_identity_key = ?", req["shared_form_no"], req["sample_identity"]); old != nil {
		return nil, conflict("product_lot_exists", Object{"product_lot_id": old["id"]})
	}
	m := clone(req)
	m["id"] = id.New()
	m["sample_identity_key"] = m["sample_identity"]
	m["version"] = 1
	m["created_at"] = now()
	m["updated_at"] = now()
	for _, f := range []string{"sample_raw", "sample_project", "sample_model", "sample_spec", "sample_size", "sample_side"} {
		if _, ok := m[f]; !ok {
			m[f] = nil
		}
	}
	u.insert("product_lots", m)
	u.audit("product_lot", "create", nil, m, "")
	return m, nil
}
func (u *unit) createGroup(lotID string, req Object) (any, error) {
	if e := allowed(req, "operation_id", "inspection_kind"); e != nil {
		return nil, e
	}
	if _, e := u.must("product_lots", lotID); e != nil {
		return nil, e
	}
	if e := requiredString(req, "operation_id"); e != nil {
		return nil, e
	}
	kind := "NORMAL"
	if v, ok := req["inspection_kind"]; ok {
		kind = str(v)
	}
	if !contains([]string{"NORMAL", "SAMPLING"}, kind) {
		return nil, formatErr("inspection_kind")
	}
	op, e := u.must("dict_items", str(req["operation_id"]))
	if e != nil {
		return nil, e
	}
	if !contains([]string{"inspection_operation", "process"}, str(op["category"])) {
		return nil, validation("operation_id 必须引用工序字典")
	}
	if op["active"] == false || num(op["active"]) == 0 && op["active"] != true {
		return nil, validation("工序已停用")
	}
	if old := u.get("SELECT id FROM operation_groups WHERE product_lot_id = ? AND operation_id = ? AND inspection_kind = ?", lotID, req["operation_id"], kind); old != nil {
		return nil, conflict("operation_group_exists", Object{"operation_group_id": old["id"]})
	}
	m := Object{"id": id.New(), "product_lot_id": lotID, "operation_id": req["operation_id"], "operation_code": op["code"], "inspection_kind": kind, "template_version": "v2.1", "reporting_state": "OPEN", "batch_mode": "EMPTY", "version": 1}
	u.insert("operation_groups", m)
	u.audit("operation_group", "create", nil, m, "")
	return m, nil
}
func (u *unit) groupAction(key, action string, req Object) (any, error) {
	if e := allowed(req, "expected_version", "reason"); e != nil {
		return nil, e
	}
	g, e := u.must("operation_groups", key)
	if e != nil {
		return nil, e
	}
	if e = version(g, req); e != nil {
		return nil, e
	}
	if e = requiredString(req, "reason"); e != nil {
		return nil, e
	}
	target := "CLOSED"
	if action == "reopen" {
		target = "OPEN"
	}
	if g["reporting_state"] == target {
		return nil, conflict("group_state_conflict", nil)
	}
	if target == "CLOSED" {
		rows := u.rows("SELECT id FROM inspection_submissions WHERE operation_group_id = ? AND state = 'DRAFT'", key)
		if len(rows) > 0 {
			ids := []any{}
			for _, r := range rows {
				ids = append(ids, r["id"])
			}
			return nil, conflict("unfinished_submissions", Object{"submission_ids": ids})
		}
	}
	before := clone(g)
	g["reporting_state"] = target
	bump(g)
	u.update("operation_groups", g)
	u.audit("operation_group", action, before, g, str(req["reason"]))
	return g, nil
}
func (u *unit) reserve(sub Object, g Object) error {
	if g["reporting_state"] != "OPEN" {
		return conflict("operation_group_closed", nil)
	}
	scope := "WHOLE"
	if sub["batch_no"] != nil {
		scope = "SPLIT"
	}
	slots := u.rows("SELECT batch_key,submission_id FROM submission_slots WHERE operation_group_id = ? ORDER BY batch_key", g["id"])
	existing := []any{}
	for _, s := range slots {
		existing = append(existing, s["submission_id"])
	}
	extra := Object{"operation_group_id": g["id"], "existing_submission_ids": existing, "existing_scope": g["batch_mode"], "allowed_action": "edit_existing"}
	if len(slots) > 0 {
		if g["batch_mode"] != scope {
			return conflict("batch_scope_conflict", extra)
		}
		for _, s := range slots {
			if s["batch_key"] == str(sub["batch_no"]) {
				reason := "batch_no_exists"
				if scope == "WHOLE" {
					reason = "whole_submission_exists"
				}
				return conflict(reason, extra)
			}
		}
	}
	u.insert("submission_slots", Object{"operation_group_id": g["id"], "batch_key": str(sub["batch_no"]), "submission_id": sub["id"]})
	g["batch_mode"] = scope
	bump(g)
	u.update("operation_groups", g)
	return nil
}
func (u *unit) revision(sub, content Object, reason string) Object {
	previous := sub["draft_revision_id"]
	if previous == nil {
		previous = sub["current_revision_id"]
	}
	n := u.get("SELECT COALESCE(MAX(revision_no),0) AS n FROM submission_revisions WHERE submission_id = ?", sub["id"])
	template := "v2.1"
	if sub["operation_group_id"] != nil {
		g, _ := u.must("operation_groups", str(sub["operation_group_id"]))
		if g != nil {
			template = str(g["template_version"])
		}
	}
	r := Object{"id": id.New(), "submission_id": sub["id"], "revision_no": num(n["n"]) + 1, "base_revision_id": previous, "template_version": template, "inspection_kind": sub["inspection_kind"], "content": stringify(content), "saved_by": u.actor, "saved_at": now(), "reason": reason, "published_by": nil, "published_at": nil}
	for _, p := range []string{"paper_inspected", "paper_submitted"} {
		r[p+"_date"] = content[p+"_date"]
		r[p+"_at"] = content[p+"_at"]
		r[p+"_day"] = day(content[p+"_date"], content[p+"_at"])
	}
	for _, f := range countFields {
		r[f] = obj(content["counts"])[f]
	}
	for _, f := range []string{"machine_no", "paper_inspector"} {
		r[f] = content[f]
	}
	u.insert("submission_revisions", r)
	for _, v := range content["lines"].([]any) {
		line := obj(v)
		row := Object{"revision_id": r["id"], "seq": line["seq"], "remark": line["remark"]}
		for _, f := range countFields {
			row[f] = line[f]
		}
		for d, cells := range obj(line["defects"]) {
			for s, n := range obj(cells) {
				row[d+"_"+s] = n
			}
		}
		u.insert("revision_lines", row)
	}
	for _, v := range content["quantity_changes"].([]any) {
		q := clone(obj(v))
		q["id"] = id.New()
		q["revision_id"] = r["id"]
		u.insert("quantity_changes", q)
	}
	sub["draft_revision_id"] = r["id"]
	return r
}
func (u *unit) createSubmission(req Object) (any, error) {
	if e := allowed(req, "operation_group_id", "inspection_kind", "batch_no", "content"); e != nil {
		return nil, e
	}
	batch, e := normalizeBatch(req["batch_no"])
	if e != nil {
		return nil, e
	}
	kind := "NORMAL"
	if v, ok := req["inspection_kind"]; ok {
		kind = str(v)
		if !contains([]string{"NORMAL", "SAMPLING"}, kind) {
			return nil, formatErr("inspection_kind")
		}
	}
	var group Object
	if req["operation_group_id"] != nil {
		if e := requiredString(req, "operation_group_id"); e != nil {
			return nil, e
		}
		group, e = u.must("operation_groups", str(req["operation_group_id"]))
		if e != nil {
			return nil, e
		}
		if _, ok := req["inspection_kind"]; ok && kind != group["inspection_kind"] {
			return nil, httpx.Validation("检验类型与工序组不一致", Object{"reason": "inspection_kind_mismatch"})
		}
		kind = str(group["inspection_kind"])
	}
	patch := Object{}
	if v, ok := req["content"]; ok {
		patch = obj(v)
		if patch == nil {
			return nil, formatErr("content")
		}
	}
	content, e := mergeContent(emptyContent(), patch)
	if e != nil {
		return nil, e
	}
	sub := Object{"id": id.New(), "operation_group_id": req["operation_group_id"], "inspection_kind": kind, "batch_no": batch, "state": "DRAFT", "version": 1, "current_revision_id": nil, "draft_revision_id": nil, "created_by": u.actor, "created_at": now(), "cancelled_by": nil, "cancelled_at": nil, "cancel_reason": nil}
	u.insert("inspection_submissions", sub)
	if group != nil {
		if e = u.reserve(sub, group); e != nil {
			return nil, e
		}
	}
	u.revision(sub, content, "")
	u.update("inspection_submissions", sub)
	u.audit("inspection_submission", "create", nil, sub, "")
	return u.detail(sub), nil
}
func (u *unit) saveDraft(key string, req Object) (any, error) {
	if e := allowed(req, "expected_version", "patch", "reason", "operation_group_id", "batch_no"); e != nil {
		return nil, e
	}
	sub, e := u.must("inspection_submissions", key)
	if e != nil {
		return nil, e
	}
	if e = version(sub, req); e != nil {
		return nil, e
	}
	if sub["state"] == "CANCELLED" {
		return nil, conflict("submission_cancelled", nil)
	}
	_, hasGroup := req["operation_group_id"]
	_, hasBatch := req["batch_no"]
	if sub["operation_group_id"] != nil && (hasGroup || hasBatch) {
		return nil, httpx.BadRequest("归组后不能修改工序组或批次")
	}
	patch := Object{}
	if v, ok := req["patch"]; ok {
		patch = obj(v)
		if patch == nil {
			return nil, formatErr("patch")
		}
	}
	if len(patch) == 0 && !hasGroup && !hasBatch {
		return nil, httpx.BadRequest("空补丁")
	}
	if req["reason"] != nil {
		if _, ok := req["reason"].(string); !ok {
			return nil, formatErr("reason")
		}
	}
	if e = u.checkSubmission(sub); e != nil {
		return nil, e
	}
	before := clone(sub)
	if hasBatch {
		sub["batch_no"], e = normalizeBatch(req["batch_no"])
		if e != nil {
			return nil, e
		}
	}
	if hasGroup {
		if e := requiredString(req, "operation_group_id"); e != nil {
			return nil, e
		}
		g, e := u.must("operation_groups", str(req["operation_group_id"]))
		if e != nil {
			return nil, e
		}
		if sub["inspection_kind"] != g["inspection_kind"] {
			return nil, httpx.Validation("检验类型与工序组不一致", Object{"reason": "inspection_kind_mismatch"})
		}
		sub["operation_group_id"] = g["id"]
		// Bind the parent row before inserting its composite-FK slot; failures roll both back.
		u.update("inspection_submissions", sub)
		if e = u.reserve(sub, g); e != nil {
			return nil, e
		}
	}
	baseID := sub["draft_revision_id"]
	if baseID == nil {
		baseID = sub["current_revision_id"]
	}
	r, e := u.must("submission_revisions", str(baseID))
	if e != nil {
		return nil, e
	}
	base, _ := decode([]byte(str(r["content"])))
	content, e := mergeContent(base, patch)
	if e != nil {
		return nil, e
	}
	u.revision(sub, content, str(req["reason"]))
	bump(sub)
	u.update("inspection_submissions", sub)
	u.audit("inspection_submission", "save_draft", before, sub, str(req["reason"]))
	return u.detail(sub), nil
}
func (u *unit) checkSubmission(sub Object) error {
	for _, field := range []string{"current_revision_id", "draft_revision_id"} {
		if sub[field] == nil {
			continue
		}
		r, e := u.must("submission_revisions", str(sub[field]))
		if e != nil {
			return e
		}
		if r["submission_id"] != sub["id"] || r["inspection_kind"] != sub["inspection_kind"] {
			return conflict("submission_integrity_conflict", nil)
		}
	}
	if sub["operation_group_id"] == nil {
		return nil
	}
	g, e := u.must("operation_groups", str(sub["operation_group_id"]))
	if e != nil {
		return e
	}
	if g["inspection_kind"] != sub["inspection_kind"] {
		return conflict("submission_integrity_conflict", nil)
	}
	if sub["state"] != "CANCELLED" {
		slot := u.get("SELECT * FROM submission_slots WHERE submission_id = ?", sub["id"])
		if slot == nil || slot["operation_group_id"] != sub["operation_group_id"] || slot["batch_key"] != str(sub["batch_no"]) {
			return conflict("submission_integrity_conflict", nil)
		}
	}
	return nil
}
func (u *unit) submissionAction(key, action string, req Object) (any, error) {
	names := []string{"expected_version", "reason"}
	if action == "publish" {
		names = append(names, "draft_revision_id")
	}
	if e := allowed(req, names...); e != nil {
		return nil, e
	}
	sub, e := u.must("inspection_submissions", key)
	if e != nil {
		return nil, e
	}
	if e = version(sub, req); e != nil {
		return nil, e
	}
	if sub["state"] == "CANCELLED" {
		return nil, conflict("submission_cancelled", nil)
	}
	if action == "cancel" || sub["current_revision_id"] != nil {
		if e = requiredString(req, "reason"); e != nil {
			return nil, e
		}
	} else if req["reason"] != nil {
		if _, ok := req["reason"].(string); !ok {
			return nil, formatErr("reason")
		}
	}
	if e = u.checkSubmission(sub); e != nil {
		return nil, e
	}
	before := clone(sub)
	if action == "publish" {
		if sub["operation_group_id"] == nil {
			return nil, validation("发布前必须归组")
		}
		if sub["draft_revision_id"] == nil {
			return nil, conflict("no_draft", nil)
		}
		if e = requiredString(req, "draft_revision_id"); e != nil {
			return nil, e
		}
		if req["draft_revision_id"] != sub["draft_revision_id"] {
			return nil, conflict("draft_revision_conflict", nil)
		}
		r, e := u.must("submission_revisions", str(sub["draft_revision_id"]))
		if e != nil {
			return nil, e
		}
		u.exec("UPDATE submission_revisions SET published_by = ?, published_at = ?, reason = ? WHERE id = ?", u.actor, now(), str(req["reason"]), r["id"])
		sub["current_revision_id"] = r["id"]
		sub["draft_revision_id"] = nil
		sub["state"] = "PUBLISHED"
	} else {
		sub["state"] = "CANCELLED"
		sub["cancelled_by"] = u.actor
		sub["cancelled_at"] = now()
		sub["cancel_reason"] = req["reason"]
		if sub["operation_group_id"] != nil {
			u.exec("DELETE FROM submission_slots WHERE submission_id = ?", sub["id"])
			g, e := u.must("operation_groups", str(sub["operation_group_id"]))
			if e != nil {
				return nil, e
			}
			slots := u.rows("SELECT batch_key FROM submission_slots WHERE operation_group_id = ?", g["id"])
			g["batch_mode"] = "EMPTY"
			if len(slots) > 0 {
				g["batch_mode"] = "SPLIT"
				if slots[0]["batch_key"] == "" {
					g["batch_mode"] = "WHOLE"
				}
			}
			bump(g)
			u.update("operation_groups", g)
		}
	}
	bump(sub)
	u.update("inspection_submissions", sub)
	u.audit("inspection_submission", action, before, sub, str(req["reason"]))
	return u.detail(sub), nil
}
func (u *unit) revisionView(key any) any {
	if key == nil {
		return nil
	}
	r, e := u.must("submission_revisions", str(key))
	if e != nil {
		u.err = e
		return nil
	}
	content, e := decode([]byte(str(r["content"])))
	if e != nil {
		u.err = e
		return nil
	}
	changes := u.rows("SELECT id,kind,direction,quantity,reason,occurred_date,occurred_at FROM quantity_changes WHERE revision_id = ? ORDER BY id", r["id"])
	for _, row := range changes {
		row["saved_by"] = r["saved_by"]
		row["saved_at"] = r["saved_at"]
	}
	r["quantity_change_records"] = changes
	r["content"] = content
	for k, v := range derived(content, true) {
		if k != "issues" && k != "is_complete" && k != "missing_fields" {
			content[k] = v
		}
	}
	return r
}
func submissionView(sub Object) Object {
	m := clone(sub)
	m["scope"] = "WHOLE"
	if sub["batch_no"] != nil {
		m["scope"] = "SPLIT"
	}
	return m
}
func (u *unit) detail(sub Object) Object {
	current := u.revisionView(sub["current_revision_id"])
	draft := u.revisionView(sub["draft_revision_id"])
	v := current
	if draft != nil {
		v = draft
	}
	d := Object{"submission": submissionView(sub), "current_revision": current, "draft_revision": draft}
	if sub["operation_group_id"] != nil {
		q := "SELECT id,cancelled_at,cancel_reason FROM inspection_submissions WHERE operation_group_id = ? AND state = 'CANCELLED' AND id <> ?"
		args := []any{sub["operation_group_id"], sub["id"]}
		if sub["batch_no"] == nil {
			q += " AND batch_no IS NULL"
		} else {
			q += " AND batch_no = ?"
			args = append(args, sub["batch_no"])
		}
		d["cancelled_predecessors"] = u.rows(q+" ORDER BY cancelled_at,id", args...)
	}

	if v != nil {
		for k, x := range derived(obj(obj(v)["content"]), sub["operation_group_id"] != nil) {
			d[k] = x
		}
		c := obj(obj(v)["content"])
		known := true
		args := []any{}
		for _, f := range []string{"sample_project", "sample_model", "sample_spec", "sample_size", "sample_side"} {
			if str(c[f]) == "" {
				known = false
			}
			args = append(args, c[f])
		}
		if known {
			r := u.get("SELECT COUNT(*) AS n FROM sample_drawings WHERE active = 1 AND project_code = ? AND model_code = ? AND spec_code = ? AND size_code = ? AND side_code = ?", args...)
			if num(r["n"]) == 0 {
				d["issues"] = append(d["issues"].([]any), Object{"rule": "sample_reference_unmatched", "level": "warning", "message": "样品核心项未匹配参考部件号，仅作提示"})
			}
		}

	}
	return d
}

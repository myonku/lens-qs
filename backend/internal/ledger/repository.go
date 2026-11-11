package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

type ProductLot struct {
	ID                string  `db:"id" json:"id"`
	SharedFormNo      string  `db:"shared_form_no" json:"shared_form_no"`
	SampleIdentity    string  `db:"sample_identity" json:"sample_identity"`
	SampleIdentityKey string  `db:"sample_identity_key" json:"sample_identity_key"`
	SampleRaw         *string `db:"sample_raw" json:"sample_raw"`
	SampleProject     *string `db:"sample_project" json:"sample_project"`
	SampleModel       *string `db:"sample_model" json:"sample_model"`
	SampleSpec        *string `db:"sample_spec" json:"sample_spec"`
	SampleSize        *string `db:"sample_size" json:"sample_size"`
	SampleSide        *string `db:"sample_side" json:"sample_side"`
	Version           int64   `db:"version" json:"version"`
	CreatedBy         string  `db:"created_by" json:"created_by"`
	CreatedAt         string  `db:"created_at" json:"created_at"`
}
type Group struct {
	ID             string `db:"id" json:"id"`
	LotID          string `db:"product_lot_id" json:"product_lot_id"`
	OperationID    string `db:"operation_id" json:"operation_id"`
	OperationCode  string `db:"operation_code" json:"operation_code"`
	OperationLabel string `db:"operation_label" json:"operation_label"`
	Kind           string `db:"inspection_kind" json:"inspection_kind"`
	Template       string `db:"template_version" json:"template_version"`
	State          string `db:"reporting_state" json:"reporting_state"`
	Mode           string `db:"batch_mode" json:"batch_mode"`
	Version        int64  `db:"version" json:"version"`
	CreatedBy      string `db:"created_by" json:"created_by"`
	CreatedAt      string `db:"created_at" json:"created_at"`
}
type Submission struct {
	ID                string  `db:"id" json:"id"`
	GroupID           *string `db:"operation_group_id" json:"operation_group_id"`
	BatchNo           *string `db:"batch_no" json:"batch_no"`
	Scope             string  `db:"scope" json:"scope"`
	Kind              string  `db:"inspection_kind" json:"inspection_kind"`
	State             string  `db:"state" json:"state"`
	Version           int64   `db:"version" json:"version"`
	CurrentRevisionID string  `db:"current_revision_id" json:"current_revision_id"`
	CreatedBy         string  `db:"created_by" json:"created_by"`
	CreatedAt         string  `db:"created_at" json:"created_at"`
	LastSavedBy       string  `db:"last_saved_by" json:"last_saved_by"`
	LastSavedAt       string  `db:"last_saved_at" json:"last_saved_at"`
	CancelledBy       *string `db:"cancelled_by" json:"cancelled_by"`
	CancelledAt       *string `db:"cancelled_at" json:"cancelled_at"`
	CancelReason      *string `db:"cancel_reason" json:"cancel_reason"`
}
type Revision struct {
	changeIDs    []string
	ID           string  `json:"id"`
	SubmissionID string  `json:"submission_id"`
	No           int64   `json:"revision_no"`
	BaseID       *string `json:"base_revision_id"`
	Template     string  `json:"template_version"`
	Schema       string  `json:"content_schema_version"`
	Content      Content `json:"content"`
	SavedBy      string  `json:"saved_by"`
	SavedName    string  `json:"saved_actor_name"`
	SavedAt      string  `json:"saved_at"`
	Reason       *string `json:"reason"`
}
type Detail struct {
	Submission Submission `json:"submission"`
	Revision   Revision   `json:"current_revision"`
	Derived    Derived    `json:"derived"`
	Issues     []Issue    `json:"issues"`
}
type Mutation struct {
	Lot      *ProductLot `json:"product_lot,omitempty"`
	Group    *Group      `json:"group,omitempty"`
	Details  []Detail    `json:"details,omitempty"`
	Progress *Progress   `json:"progress,omitempty"`
}
type Request struct {
	SuccessStatus              int
	ActorID, Key, Method, Path string
	Raw                        []byte
}
type Repository struct{ w *store.WriteTx }

func reasonConflict(reason string) error {
	return httpx.Conflict(reason, map[string]string{"reason": reason})
}
func versionConflict(id string, current int64) error {
	return httpx.Conflict("数据已被修改，请刷新核对", map[string]any{"reason": "version_conflict", "entity_id": id, "current_version": current})
}
func typeForbidden(expected, target string) error {
	return httpx.NewError(403, "forbidden", "检验类型不允许", map[string]string{"reason": "inspection_kind_forbidden", "expected_kind": expected, "target_kind": target})
}
func Write(ctx context.Context, db *sqlx.DB, req Request, clock func() time.Time, fn func(*Repository) (Mutation, error)) (Mutation, bool, error) {
	if req.SuccessStatus == 0 {
		req.SuccessStatus = 200
	}
	if req.SuccessStatus < 200 || req.SuccessStatus > 299 {
		return Mutation{}, false, httpx.BadRequest("only successful responses can be cached")
	}
	if strings.TrimSpace(req.Key) == "" {
		return Mutation{}, false, httpx.BadRequest("X-Idempotency-Key required")
	}
	w, e := store.BeginWrite(ctx, db, req.ActorID, true, clock)
	if e != nil {
		return Mutation{}, false, e
	}
	defer w.Close()
	fail := func(e error) (Mutation, bool, error) { return Mutation{}, false, store.StorageError(e) }
	if w.Actor.ID == "" {
		return fail(httpx.Unauthorized(""))
	}
	h := sha256.Sum256(append([]byte(req.Method+"\x00"+req.Path+"\x00"+w.Actor.Kind+"\x00"), req.Raw...))
	fingerprint := hex.EncodeToString(h[:])
	var cached struct {
		Fingerprint string `db:"fingerprint"`
		Body        string `db:"response_body"`
		Expires     string `db:"expires_at"`
	}
	e = w.GetContext(w.Context, &cached, w.Rebind(`SELECT fingerprint,response_body,expires_at FROM v2_idempotency_requests WHERE actor_id=? AND idempotency_key=?`), req.ActorID, req.Key)
	if e == nil && cached.Expires > w.Time {
		if cached.Fingerprint != fingerprint {
			return fail(reasonConflict("idempotency_key_reused"))
		}
		var m Mutation
		if e = json.Unmarshal([]byte(cached.Body), &m); e != nil {
			return fail(e)
		}
		return m, true, nil
	}
	if e != nil && e != sql.ErrNoRows {
		return fail(e)
	}
	if _, e = w.ExecContext(w.Context, w.Rebind(`DELETE FROM v2_idempotency_requests WHERE actor_id=? AND idempotency_key=?`), req.ActorID, req.Key); e != nil {
		return fail(e)
	}
	result, e := fn(&Repository{w: w})
	if e != nil {
		return fail(e)
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return fail(e)
	}
	t, _ := time.Parse(time.RFC3339, w.Time)
	if _, e = w.ExecContext(w.Context, w.Rebind(`INSERT INTO v2_idempotency_requests VALUES(?,?,?,?,?,?,?)`), req.ActorID, req.Key, fingerprint, req.SuccessStatus, string(raw), w.Time, t.Add(24*time.Hour).Format(time.RFC3339)); e != nil {
		return fail(e)
	}
	if e = w.Commit(); e != nil {
		return fail(e)
	}
	return result, false, nil
}
func (r *Repository) CreateLot(shared, identity string, reference Content) (ProductLot, error) {
	shared = strings.TrimSpace(shared)
	identity = strings.TrimSpace(identity)
	if shared == "" || identity == "" {
		return ProductLot{}, invalid("identity_required", "shared_form_no")
	}
	w := r.w
	l := ProductLot{id.New(), shared, identity, identity, reference.SampleRaw, reference.SampleProject, reference.SampleModel, reference.SampleSpec, reference.SampleSize, reference.SampleSide, 1, w.Actor.ID, w.Time}
	var existing string
	e := w.GetContext(w.Context, &existing, w.Rebind(`SELECT id FROM product_lots WHERE shared_form_no=? AND sample_identity_key=?`), shared, identity)
	if e == nil {
		return l, httpx.Conflict("product_lot_exists", map[string]string{"reason": "product_lot_exists", "product_lot_id": existing})
	}
	if e != sql.ErrNoRows {
		return l, e
	}
	_, e = w.NamedExecContext(w.Context, `INSERT INTO product_lots(id,shared_form_no,sample_identity,sample_identity_key,sample_raw,sample_project,sample_model,sample_spec,sample_size,sample_side,version,created_by,created_at) VALUES(:id,:shared_form_no,:sample_identity,:sample_identity_key,:sample_raw,:sample_project,:sample_model,:sample_spec,:sample_size,:sample_side,:version,:created_by,:created_at)`, l)
	if e == nil {
		e = w.Event("create", "product_lot", l.ID, nil, l, nil)
	}
	return l, e
}
func (r *Repository) CreateGroup(lotID, operationID string) (Group, error) {
	w := r.w
	if e := r.checkOperation(operationID); e != nil {
		return Group{}, e
	}
	if _, e := readOne[ProductLot](w.Context, w, `SELECT * FROM product_lots WHERE id=?`, lotID); e != nil {
		return Group{}, e
	}
	var existing string
	lookupErr := w.GetContext(w.Context, &existing, w.Rebind(`SELECT id FROM operation_groups WHERE product_lot_id=? AND operation_id=? AND inspection_kind=?`), lotID, operationID, w.Actor.Kind)
	if lookupErr == nil {
		return Group{}, httpx.Conflict("工序组已存在", map[string]string{"reason": "operation_group_exists", "operation_group_id": existing})
	}
	if lookupErr != sql.ErrNoRows {
		return Group{}, lookupErr
	}
	g := Group{ID: id.New(), LotID: lotID, OperationID: operationID, Kind: w.Actor.Kind, Template: "general-1", State: "OPEN", Mode: "EMPTY", Version: 1, CreatedBy: w.Actor.ID, CreatedAt: w.Time}
	var op struct {
		Code   string `db:"code"`
		Label  string `db:"label"`
		Active bool   `db:"active"`
	}
	if e := w.GetContext(w.Context, &op, w.Rebind(`SELECT code,label,active FROM dict_items WHERE id=? AND category='inspection_operation'`), operationID); e != nil {
		return g, invalid("invalid_operation", "operation_id")
	}
	if !op.Active {
		return g, invalid("inactive_operation", "operation_id")
	}
	g.OperationCode = op.Code
	g.OperationLabel = op.Label
	_, e := w.NamedExecContext(w.Context, `INSERT INTO operation_groups(id,product_lot_id,operation_id,operation_code,operation_label,inspection_kind,template_version,reporting_state,batch_mode,version,created_by,created_at) VALUES(:id,:product_lot_id,:operation_id,:operation_code,:operation_label,:inspection_kind,:template_version,:reporting_state,:batch_mode,:version,:created_by,:created_at)`, g)
	if e == nil {
		e = w.Event("create", "operation_group", g.ID, nil, g, nil)
	}
	return g, e
}
func (r *Repository) group(id string) (Group, error) {
	var g Group
	e := r.w.GetContext(r.w.Context, &g, r.w.Rebind(`SELECT * FROM operation_groups WHERE id=?`), id)
	if e == sql.ErrNoRows {
		return g, httpx.NotFound("group")
	}
	if e != nil {
		return g, e
	}
	if g.Kind != r.w.Actor.Kind {
		return g, typeForbidden(r.w.Actor.Kind, g.Kind)
	}
	return g, r.checkOperation(g.OperationID)
}

func (r *Repository) checkOperation(operationID string) error {
	actor := r.w.Actor
	if actor.Admin || actor.OperationID != nil && *actor.OperationID == operationID {
		return nil
	}
	return operationForbidden()
}

func operationForbidden() error {
	return httpx.NewError(403, "forbidden", "当前账户无权填报此工序或记录", map[string]string{"reason": "operation_forbidden"})
}

func (r *Repository) checkUngrouped(createdBy string) error {
	actor := r.w.Actor
	if actor.Admin || actor.OperationID != nil && actor.ID == createdBy {
		return nil
	}
	return operationForbidden()
}
func (r *Repository) GroupState(id string, expected int64, closed bool, reason string) (Group, error) {
	if expected < 1 || expected > MaxQuantity {
		return Group{}, httpx.BadRequest("expected_version must be a positive safe integer")
	}
	g, e := r.group(id)
	if e != nil {
		return g, e
	}
	if expected != g.Version {
		return g, versionConflict(g.ID, g.Version)
	}
	if strings.TrimSpace(reason) == "" {
		return g, invalid("reason_required", "reason")
	}
	before := g
	g.State = "OPEN"
	if closed {
		g.State = "CLOSED"
	}
	if before.State == g.State {
		return g, reasonConflict("group_state_conflict")
	}
	g.Version++
	_, e = r.w.ExecContext(r.w.Context, r.w.Rebind(`UPDATE operation_groups SET reporting_state=?,version=? WHERE id=? AND version=?`), g.State, g.Version, id, expected)
	if e == nil {
		e = r.w.Event("state", "operation_group", id, before, g, &reason)
	}
	return g, e
}
func (r *Repository) slot(s Submission, release bool) error {
	if s.GroupID == nil {
		return nil
	}
	g, e := r.group(*s.GroupID)
	if e != nil {
		return e
	}
	before := g
	w := r.w
	conflict := func(reason string) error {
		ids := []string{}
		query := `SELECT submission_id FROM submission_slots WHERE operation_group_id=?`
		args := []any{g.ID}
		if reason == "batch_no_exists" {
			query += ` AND batch_key=?`
			args = append(args, *s.BatchNo)
		}
		if e := w.SelectContext(w.Context, &ids, w.Rebind(query+` ORDER BY submission_id`), args...); e != nil {
			return e
		}
		return httpx.Conflict("检验范围已占用", map[string]any{"reason": reason, "operation_group_id": g.ID, "existing_submission_ids": ids, "existing_scope": g.Mode, "batch_no": s.BatchNo, "allowed_action": "edit_existing"})
	}
	if release {
		_, e = w.ExecContext(w.Context, w.Rebind(`DELETE FROM submission_slots WHERE submission_id=?`), s.ID)
	} else {
		if g.State == "CLOSED" {
			return httpx.Conflict("工序组已结束填报", map[string]string{"reason": "operation_group_closed", "operation_group_id": g.ID})
		}
		if g.Mode == "WHOLE" && s.Scope == "WHOLE" {
			return conflict("whole_submission_exists")
		}
		if g.Mode != "EMPTY" && g.Mode != s.Scope {
			return conflict("batch_scope_conflict")
		}
		key := ""
		if s.BatchNo != nil {
			key = *s.BatchNo
		}
		var n int
		if e = w.GetContext(w.Context, &n, w.Rebind(`SELECT COUNT(*) FROM submission_slots WHERE operation_group_id=? AND batch_key=?`), g.ID, key); e != nil {
			return e
		}
		if n > 0 {
			return conflict("batch_no_exists")
		}
		_, e = w.ExecContext(w.Context, w.Rebind(`INSERT INTO submission_slots VALUES(?,?,?)`), g.ID, key, s.ID)
	}
	if e != nil {
		return e
	}
	var n int
	var whole int
	if e = w.GetContext(w.Context, &n, w.Rebind(`SELECT COUNT(*) FROM submission_slots WHERE operation_group_id=?`), g.ID); e != nil {
		return e
	}
	if e = w.GetContext(w.Context, &whole, w.Rebind(`SELECT COUNT(*) FROM submission_slots WHERE operation_group_id=? AND batch_key=''`), g.ID); e != nil {
		return e
	}
	g.Mode = "EMPTY"
	if n > 0 {
		g.Mode = "SPLIT"
	}
	if whole > 0 {
		g.Mode = "WHOLE"
	}
	g.Version++
	if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE operation_groups SET batch_mode=?,version=? WHERE id=?`), g.Mode, g.Version, g.ID); e != nil {
		return e
	}
	return w.Event("occupancy", "operation_group", g.ID, before, g, nil)
}

type SaveInput struct {
	ID              string
	ExpectedVersion int64
	GroupID         *string
	GroupSet        bool
	BatchNo         *string
	BatchSet        bool
	Patch           json.RawMessage
	Reason          *string
}

func normalizeBatch(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}
func (r *Repository) Save(in SaveInput) (Detail, error) {
	w := r.w
	s := Submission{ID: id.New(), Kind: w.Actor.Kind, State: "ACTIVE", Version: 1, CreatedBy: w.Actor.ID, CreatedAt: w.Time, LastSavedBy: w.Actor.ID, LastSavedAt: w.Time}
	base := EmptyContent()
	template := "general-1"
	no := int64(1)
	var baseID *string
	var before any
	create := in.ID == ""
	if !create {
		if in.ExpectedVersion < 1 || in.ExpectedVersion > MaxQuantity {
			return Detail{}, httpx.BadRequest("expected_version must be a positive safe integer")
		}
		if e := w.GetContext(w.Context, &s, w.Rebind(`SELECT * FROM inspection_submissions WHERE id=?`), in.ID); e != nil {
			if e == sql.ErrNoRows {
				return Detail{}, httpx.NotFound("submission")
			}
			return Detail{}, e
		}
		before = s
		if s.Kind != w.Actor.Kind {
			return Detail{}, typeForbidden(w.Actor.Kind, s.Kind)
		}
		if s.GroupID != nil {
			g, e := r.group(*s.GroupID)
			if e != nil {
				return Detail{}, e
			}
			template = g.Template
		} else if e := r.checkUngrouped(s.CreatedBy); e != nil {
			return Detail{}, e
		}
		if s.State != "ACTIVE" {
			return Detail{}, reasonConflict("submission_cancelled")
		}
		if in.ExpectedVersion != s.Version {
			return Detail{}, versionConflict(s.ID, s.Version)
		}
		var raw string
		var revno int64
		if e := w.QueryRowxContext(w.Context, w.Rebind(`SELECT content_json,revision_no FROM submission_revisions WHERE id=? AND submission_id=?`), s.CurrentRevisionID, s.ID).Scan(&raw, &revno); e != nil {
			return Detail{}, e
		}
		if e := json.Unmarshal([]byte(raw), &base); e != nil {
			return Detail{}, e
		}
		v := s.CurrentRevisionID
		baseID = &v
		no = revno + 1
		s.Version++
	}
	if s.GroupID != nil && (in.GroupSet || in.GroupID != nil || in.BatchSet) {
		return Detail{}, httpx.BadRequest("归组身份不可修改", map[string]string{"reason": "immutable_identity"})
	}
	if !create && in.GroupSet && (in.GroupID == nil || strings.TrimSpace(*in.GroupID) == "") {
		return Detail{}, httpx.BadRequest("绑定组必须非空")
	}
	needSlot := create || s.GroupID == nil && in.GroupID != nil
	if s.GroupID == nil {
		if in.BatchSet || create {
			s.BatchNo = normalizeBatch(in.BatchNo)
		}
		if in.GroupID != nil {
			s.GroupID = in.GroupID
		}
	}
	s.Scope = "WHOLE"
	if s.BatchNo != nil {
		s.Scope = "SPLIT"
	}
	if s.GroupID != nil && needSlot {
		g, e := r.group(*s.GroupID)
		if e != nil {
			return Detail{}, e
		}
		template = g.Template
	} else if create && s.GroupID == nil {
		if e := r.checkUngrouped(s.CreatedBy); e != nil {
			return Detail{}, e
		}
	}
	if len(in.Patch) == 0 {
		in.Patch = []byte(`{}`)
	}
	var patchObject map[string]json.RawMessage
	if e := httpx.DecodeStrict(in.Patch, &patchObject); e != nil {
		return Detail{}, e
	}
	if !create && len(patchObject) == 0 && in.GroupID == nil && !in.BatchSet {
		return Detail{}, httpx.BadRequest("empty patch")
	}
	content, e := Merge(base, in.Patch, s.Kind)
	if e != nil {
		return Detail{}, e
	}
	d, issues, e := Derive(content, s.GroupID != nil, time.Now())
	if e != nil {
		return Detail{}, e
	}
	rev := Revision{ID: id.New(), SubmissionID: s.ID, No: no, BaseID: baseID, Template: template, Schema: "2", Content: content, SavedBy: w.Actor.ID, SavedName: w.Actor.Name, SavedAt: w.Time, Reason: in.Reason}
	s.CurrentRevisionID = rev.ID
	s.LastSavedBy = w.Actor.ID
	s.LastSavedAt = w.Time
	if create {
		_, e = w.NamedExecContext(w.Context, `INSERT INTO inspection_submissions(id,operation_group_id,batch_no,scope,inspection_kind,state,version,current_revision_id,created_by,created_at,last_saved_by,last_saved_at) VALUES(:id,:operation_group_id,:batch_no,:scope,:inspection_kind,:state,:version,:current_revision_id,:created_by,:created_at,:last_saved_by,:last_saved_at)`, s)
	} else {
		var res sql.Result
		res, e = w.ExecContext(w.Context, w.Rebind(`UPDATE inspection_submissions SET operation_group_id=?,batch_no=?,scope=?,version=?,current_revision_id=?,last_saved_by=?,last_saved_at=? WHERE id=? AND version=?`), s.GroupID, s.BatchNo, s.Scope, s.Version, s.CurrentRevisionID, s.LastSavedBy, s.LastSavedAt, s.ID, in.ExpectedVersion)
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				e = reasonConflict("version_conflict")
			}
		}
	}
	if e != nil {
		return Detail{}, e
	}
	if e = r.insertRevision(rev, d); e != nil {
		return Detail{}, e
	}
	output := []Revision{rev}
	if e = attachChangeIDs(w.Context, w, output); e != nil {
		return Detail{}, e
	}
	rev = output[0]
	if needSlot {
		if e = r.slot(s, false); e != nil {
			return Detail{}, e
		}
	}
	if e = w.Event("content_saved", "submission", s.ID, before, s, in.Reason); e != nil {
		return Detail{}, e
	}
	return Detail{s, rev, d, issues}, nil
}
func (r *Repository) insertRevision(rev Revision, d Derived) error {
	w := r.w
	c := rev.Content
	raw, _ := json.Marshal(c)
	missing, _ := json.Marshal(d.Missing)
	q := `INSERT INTO submission_revisions(id,submission_id,revision_no,base_revision_id,template_version,content_schema_version,content_json,saved_by,saved_actor_name,saved_at,reason,total_qty,qualified_qty,defect_qty,paper_inspected_day,paper_submitted_day,machine_no,paper_inspector,paper_submitter,paper_machine_operator,paper_handover_by,sample_raw,sample_project,sample_model,sample_spec,sample_size,sample_side,is_complete,missing_fields,paper_unit_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if _, e := w.ExecContext(w.Context, w.Rebind(q), rev.ID, rev.SubmissionID, rev.No, rev.BaseID, rev.Template, rev.Schema, string(raw), rev.SavedBy, rev.SavedName, rev.SavedAt, rev.Reason, c.Counts.Total, c.Counts.Qualified, c.Counts.Defect, d.InspectedDay, d.SubmittedDay, c.MachineNo, c.PaperInspector, c.PaperSubmitter, c.PaperMachineOperator, c.PaperHandoverBy, c.SampleRaw, c.SampleProject, c.SampleModel, c.SampleSpec, c.SampleSize, c.SampleSide, store.BoolInt(d.Complete), string(missing), c.PaperUnitText); e != nil {
		return e
	}
	for _, l := range c.Lines {
		cols := []string{"revision_id", "seq", "total_qty", "qualified_qty", "defect_qty", "remark"}
		args := []any{rev.ID, l.Seq, l.Total, l.Qualified, l.Defect, l.Remark}
		for _, code := range formfields.DefectCodes() {
			v := l.Defects[code]
			for j, surface := range formfields.Surfaces {
				cols = append(cols, code+"_"+surface)
				args = append(args, []*int64{v.Concave, v.Convex, v.Both}[j])
			}
		}
		qs := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
		if _, e := w.ExecContext(w.Context, w.Rebind(`INSERT INTO revision_lines(`+strings.Join(cols, ",")+`) VALUES(`+qs+`)`), args...); e != nil {
			return e
		}
	}
	for _, q := range c.Changes {
		if _, e := w.ExecContext(w.Context, w.Rebind(`INSERT INTO quantity_changes(id,revision_id,kind,direction,quantity,reason,occurred_date,occurred_at) VALUES(?,?,?,?,?,?,?,?)`), id.New(), rev.ID, q.Kind, q.Direction, q.Quantity, q.Reason, q.Date, q.At); e != nil {
			return e
		}
	}
	return nil
}
func (r *Repository) Cancel(id string, expected int64, reason string) (Submission, error) {
	if expected < 1 || expected > MaxQuantity {
		return Submission{}, httpx.BadRequest("expected_version must be a positive safe integer")
	}
	w := r.w
	var s Submission
	if e := w.GetContext(w.Context, &s, w.Rebind(`SELECT * FROM inspection_submissions WHERE id=?`), id); e != nil {
		if e == sql.ErrNoRows {
			return s, httpx.NotFound("单据不存在")
		}
		return s, e
	}
	if s.Kind != w.Actor.Kind {
		return s, typeForbidden(w.Actor.Kind, s.Kind)
	}
	if s.GroupID != nil {
		if _, e := r.group(*s.GroupID); e != nil {
			return s, e
		}
	} else if e := r.checkUngrouped(s.CreatedBy); e != nil {
		return s, e
	}
	if s.State != "ACTIVE" {
		return s, reasonConflict("submission_cancelled")
	}
	if s.Version != expected {
		return s, versionConflict(s.ID, s.Version)
	}
	if strings.TrimSpace(reason) == "" {
		return s, invalid("reason_required", "reason")
	}
	before := s
	s.State = "CANCELLED"
	s.Version++
	s.CancelledBy = &w.Actor.ID
	s.CancelledAt = &w.Time
	s.CancelReason = &reason
	if e := r.slot(s, true); e != nil {
		return s, e
	}
	if _, e := w.ExecContext(w.Context, w.Rebind(`UPDATE inspection_submissions SET state=?,version=?,cancelled_by=?,cancelled_at=?,cancel_reason=? WHERE id=? AND version=?`), s.State, s.Version, s.CancelledBy, s.CancelledAt, s.CancelReason, id, expected); e != nil {
		return s, e
	}
	return s, w.Event("cancel", "submission", id, before, s, &reason)
}

// CurrentSet fixes a version collection in one SQL statement. Later corrections
// cannot change the selected immutable contents. Round two adds typed filters.
type SelectedRevision struct {
	SubmissionID string `db:"submission_id"`
	RevisionID   string `db:"revision_id"`
	ContentJSON  string `db:"content_json"`
}

func CurrentSet(ctx context.Context, db store.Reader, groupID string) ([]SelectedRevision, error) {
	out := []SelectedRevision{}
	e := db.SelectContext(ctx, &out, db.Rebind(`SELECT s.id AS submission_id,r.id AS revision_id,r.content_json FROM inspection_submissions s JOIN submission_revisions r ON r.id=s.current_revision_id AND r.submission_id=s.id WHERE s.state='ACTIVE' AND s.operation_group_id=? ORDER BY s.id`), groupID)
	return out, e
}

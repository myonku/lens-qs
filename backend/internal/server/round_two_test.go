package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

type apiFixture struct {
	t                    *testing.T
	db                   *sqlx.DB
	r                    http.Handler
	token, sampling, uid string
	n                    int
}
type apiResponse struct {
	Replay  bool            `json:"-"`
	Code    string          `json:"code"`
	Data    json.RawMessage `json:"data"`
	Details map[string]any  `json:"details"`
}

func newAPI(t *testing.T) *apiFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, driver := store.FixtureDB(t, true)
	us := users.NewStore(db, driver)
	u, e := us.Create(context.Background(), users.CreateInput{Username: "normal", Password: "normal-password", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	_, e = us.Create(context.Background(), users.CreateInput{Username: "sampling", Password: "sampling-password", AccountType: "SAMPLING", IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	svc := auth.NewService(db, us, auth.Config{SessionTTL: time.Hour, SessionIdleTTL: time.Hour}, driver)
	f := &apiFixture{t: t, db: db, uid: u.ID}
	f.r = Build(Deps{Cfg: &config.Config{CookieName: "sid", CORSOrigins: []string{"https://allowed.example"}}, Auth: svc, UserStore: us, Dict: dictionary.NewStore(db, driver)})
	for _, name := range []string{"normal", "sampling"} {
		var result struct {
			Token string `json:"token"`
		}
		r := f.request("POST", "/api/v2/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, name, name+"-password"), "", "", 200)
		decode(t, r.Data, &result)
		if name == "normal" {
			f.token = result.Token
		} else {
			f.sampling = result.Token
		}
	}
	return f
}
func decode(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if e := json.Unmarshal(raw, dst); e != nil {
		t.Fatal(string(raw), e)
	}
}
func (f *apiFixture) request(method, path, body, token, key string, status int) apiResponse {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("X-Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	if w.Code != status {
		f.t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body)
	}
	var r apiResponse
	decode(f.t, w.Body.Bytes(), &r)
	r.Replay = w.Header().Get("X-Idempotent-Replay") == "true"
	return r
}
func (f *apiFixture) call(method, path, body string, status int) apiResponse {
	f.n++
	return f.request(method, "/api/v2"+path, body, f.token, fmt.Sprint(f.n), status)
}
func (f *apiFixture) lot() ledger.ProductLot {
	var lot ledger.ProductLot
	decode(f.t, f.call("POST", "/product-lots", `{"shared_form_no":" QS-001 ","sample_identity":" Model  X ","sample_raw":"  reference  "}`, 201).Data, &lot)
	return lot
}

func TestUserOperationPermissions(t *testing.T) {
	f := newAPI(t)
	a, b := f.op("A"), f.op("B")
	lot := f.lot()
	groupA, groupB := f.group(lot.ID, a, f.token), f.group(lot.ID, b, f.token)
	sampling := f.group(lot.ID, a, f.sampling)
	f.call("POST", "/users", `{"username":"invalid-op","password":"valid-password","operation_id":"missing"}`, 422)

	var user users.User
	decode(t, f.call("POST", "/users", fmt.Sprintf(`{"username":"inspector","password":"valid-password","operation_id":%q}`, a), 201).Data, &user)
	if user.OperationID == nil || *user.OperationID != a {
		t.Fatal("missing operation assignment", user.OperationID)
	}
	login := func() string {
		var res struct {
			Token string     `json:"token"`
			User  users.User `json:"user"`
		}
		decode(t, f.call("POST", "/auth/login", `{"username":"inspector","password":"valid-password"}`, 200).Data, &res)
		return res.Token
	}
	token := login()
	staff := func(method, path, body string, status int) apiResponse {
		f.n++
		return f.request(method, "/api/v2"+path, body, token, fmt.Sprint(f.n), status)
	}
	create := func(groupID string) string {
		return fmt.Sprintf(`{"operation_group_id":%q,"content":{}}`, groupID)
	}
	staff("POST", "/product-lots/"+lot.ID+"/operation-groups", fmt.Sprintf(`{"operation_id":%q}`, b), 403)
	staff("POST", "/submissions", create(groupB.ID), 403)
	staff("POST", "/submissions", create(sampling.ID), 403)
	f.call("POST", "/submissions", create(sampling.ID), 403)
	var own ledger.Detail
	decode(t, staff("POST", "/submissions", create(groupA.ID), 201).Data, &own)
	other := f.save(groupB.ID, "", "{}")
	staff("PATCH", "/submissions/"+other.Submission.ID, `{"expected_version":1,"patch":{"remark":"forbidden"}}`, 403)
	staff("POST", "/submissions/"+other.Submission.ID+"/cancel", `{"expected_version":1,"reason":"forbidden"}`, 403)
	staff("POST", "/submissions/batch", fmt.Sprintf(`{"items":[%s,%s]}`, create(groupB.ID), create(groupA.ID)), 403)

	var draft ledger.Detail
	decode(t, staff("POST", "/submissions", `{"content":{}}`, 201).Data, &draft)
	staff("PATCH", "/submissions/"+draft.Submission.ID, fmt.Sprintf(`{"expected_version":1,"operation_group_id":%q}`, groupB.ID), 403)
	f.call("PATCH", "/users/"+user.ID, fmt.Sprintf(`{"operation_ids":[%q,"missing"]}`, a), 422)
	staff("GET", "/auth/me", "", 200)
	var updated users.User
	decode(t, f.call("PATCH", "/users/"+user.ID, fmt.Sprintf(`{"operation_ids":[%q,%q]}`, a, b), 200).Data, &updated)
	if len(updated.OperationIDs) != 2 {
		t.Fatal("expected two assigned operations", updated.OperationIDs)
	}
	token = login()
	staff("PATCH", "/submissions/"+own.Submission.ID, `{"expected_version":1,"patch":{"remark":"allowed in A"}}`, 200)
	staff("PATCH", "/submissions/"+other.Submission.ID, `{"expected_version":1,"patch":{"remark":"allowed in B"}}`, 200)
	f.call("PATCH", "/users/"+user.ID, `{"operation_ids":[]}`, 200)
	staff("GET", "/auth/me", "", 401)
	token = login()
	staff("PATCH", "/submissions/"+own.Submission.ID, `{"expected_version":2,"patch":{"remark":"forbidden"}}`, 403)
	staff("POST", "/submissions", `{"content":{}}`, 403)
}
func (f *apiFixture) op(code string) string {
	var d dictionary.Item
	decode(f.t, f.call("POST", "/dict/items", fmt.Sprintf(`{"category":"inspection_operation","code":%q,"label":%q}`, code, "工序"+code), 200).Data, &d)
	return d.ID
}
func (f *apiFixture) group(lot, op, token string) ledger.Group {
	var g ledger.Group
	f.n++
	decode(f.t, f.request("POST", "/api/v2/product-lots/"+lot+"/operation-groups", fmt.Sprintf(`{"operation_id":%q}`, op), token, fmt.Sprint(f.n), 201).Data, &g)
	return g
}
func (f *apiFixture) save(group, batch, content string) ledger.Detail {
	var d ledger.Detail
	g := "null"
	if group != "" {
		g = fmt.Sprintf("%q", group)
	}
	decode(f.t, f.call("POST", "/submissions", fmt.Sprintf(`{"operation_group_id":%s,"batch_no":%q,"content":%s}`, g, batch, content), 201).Data, &d)
	return d
}

const record = `{"paper_unit_text":"片","sample_raw":"  原文  ","sample_project":"M2","paper_inspector":"纸面甲","paper_submitter":"送检甲","paper_machine_operator":"操机甲","paper_handover_by":"交接甲","paper_inspected_date":"2026-11-05","machine_no":"M01","counts":{"total_qty":100,"qualified_qty":96,"defect_qty":4},"lines":[{"seq":1,"total_qty":90,"qualified_qty":85,"defect_qty":5,"defects":{"prick":{"concave":2}}}],"quantity_changes":[{"kind":"LOSS","direction":"DECREASE","quantity":null,"reason":"待核对"} ]}`

func TestG2WriteLifecycleAndContracts(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	a, b := f.op("A"), f.op("B")
	gb := f.group(lot.ID, b, f.token)
	ga := f.group(lot.ID, a, f.token)
	gs := f.group(lot.ID, a, f.sampling)
	if ga.LotID != gs.LotID {
		t.Fatal("types duplicated lot")
	}
	d := f.save(gb.ID, "same", record)
	if d.Revision.Content.SampleRaw == nil || *d.Revision.Content.SampleRaw != "  原文  " || *d.Derived.PassRate != 96 {
		t.Fatal(d)
	}
	f.call("POST", "/product-lots", `{"shared_form_no":"QS-001","sample_identity":"Model  X"}`, 409)
	f.call("POST", "/product-lots/"+lot.ID+"/operation-groups", fmt.Sprintf(`{"operation_id":%q}`, a), 409)
	body := fmt.Sprintf(`{"operation_group_id":%q,"batch_no":"a","content":%s}`, ga.ID, record)
	original := f.request("POST", "/api/v2/submissions", body, f.token, "retry-original", 201)
	var originalDetail ledger.Detail
	decode(t, original.Data, &originalDetail)
	again := f.request("POST", "/api/v2/submissions", body, f.token, "retry-original", 201)
	if string(original.Data) != string(again.Data) {
		t.Fatal("replay changed")
	}
	f.request("POST", "/api/v2/submissions", body+" ", f.token, "retry-original", 409)
	f.call("POST", "/submissions", body, 409)
	f.call("POST", "/submissions", fmt.Sprintf(`{"operation_group_id":%q,"content":{}}`, ga.ID), 409)
	f.request("PATCH", "/api/v2/submissions/"+originalDetail.Submission.ID, `{"expected_version":1,"patch":{"remark":"cross"}}`, f.sampling, "cross", 403)
	f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":1,"patch":{"lines":[]}}`, 200)
	f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":1,"patch":{"remark":"stale"}}`, 409)
	var changed ledger.Detail
	decode(t, f.call("GET", "/submissions/"+d.Submission.ID, "", 200).Data, &changed)
	if changed.Revision.No != 2 || *changed.Revision.Content.Counts.Total != 100 || len(changed.Revision.Content.Lines) != 0 {
		t.Fatal(changed)
	}
	for _, patch := range []string{`{"declared_counts":{}}`, `{"paper_inspected_raw":"today"}`, `{"derived":{}}`, `{"lines":null}`, `{"counts":{"total_qty":1.5}}`, `{"counts":{"total_qty":0,"total_qty":1}}`} {
		f.call("PATCH", "/submissions/"+d.Submission.ID, fmt.Sprintf(`{"expected_version":2,"patch":%s}`, patch), 400)
	}
	for _, patch := range []string{`{"paper_unit_text":"一厂"}`, `{"paper_inspected_date":"2025-02-29"}`, `{"counts":{"total_qty":-1}}`, `{"lines":[{"seq":1},{"seq":1}]}`} {
		f.call("PATCH", "/submissions/"+d.Submission.ID, fmt.Sprintf(`{"expected_version":2,"patch":%s}`, patch), 422)
	}
	f.call("PATCH", "/submissions/"+d.Submission.ID, fmt.Sprintf(`{"expected_version":2,"operation_group_id":%q}`, gb.ID), 400)
	f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":2,"patch":{}}`, 400)
	f.request("POST", "/api/v2/submissions", fmt.Sprintf(`{"operation_group_id":%q,"content":%s}`, gs.ID, record), f.sampling, "sampling-loss", 422)
	var sampled ledger.Detail
	decode(t, f.request("POST", "/api/v2/submissions", fmt.Sprintf(`{"operation_group_id":%q,"batch_no":"a","content":{}}`, gs.ID), f.sampling, "sampling-a", 201).Data, &sampled)
	unassigned := f.save("", "u", `{"sample_raw":"independent"}`)
	f.call("PATCH", "/submissions/"+unassigned.Submission.ID, fmt.Sprintf(`{"expected_version":1,"operation_group_id":%q}`, ga.ID), 200)
	var current ledger.GroupDetail
	decode(t, f.call("GET", "/operation-groups/"+ga.ID, "", 200).Data, &current)
	f.call("POST", "/operation-groups/"+ga.ID+"/close", fmt.Sprintf(`{"expected_version":%d,"reason":"结束"}`, current.Group.Version), 200)
	f.call("POST", "/submissions", fmt.Sprintf(`{"operation_group_id":%q,"batch_no":"new","content":{}}`, ga.ID), 409)
	f.call("PATCH", "/submissions/"+originalDetail.Submission.ID, `{"expected_version":1,"patch":{"remark":"closed correction"}}`, 200)
	f.call("POST", "/submissions/"+originalDetail.Submission.ID+"/cancel", `{"expected_version":2,"reason":"录错"}`, 200)
	var cancelled ledger.Detail
	decode(t, f.call("GET", "/submissions/"+originalDetail.Submission.ID, "", 200).Data, &cancelled)
	if cancelled.Revision.No != 2 || cancelled.Submission.State != "CANCELLED" {
		t.Fatal(cancelled)
	}
	f.call("POST", "/submissions/"+originalDetail.Submission.ID+"/cancel", `{"expected_version":3,"reason":"重复"}`, 409)
	var gd ledger.GroupDetail
	decode(t, f.call("GET", "/operation-groups/"+ga.ID, "", 200).Data, &gd)
	f.call("POST", "/operation-groups/"+ga.ID+"/reopen", fmt.Sprintf(`{"expected_version":%d,"reason":"继续"}`, gd.Group.Version), 200)
	f.save(ga.ID, "a", `{}`)
	var n int
	f.db.Get(&n, `SELECT COUNT(*) FROM inspection_submissions`)
	before := n
	f.call("POST", "/submissions/batch", fmt.Sprintf(`{"items":[{"operation_group_id":%q,"batch_no":"rollback","content":{}},{"operation_group_id":%q,"batch_no":"rollback","content":{}}]}`, ga.ID, ga.ID), 409)
	f.db.Get(&n, `SELECT COUNT(*) FROM inspection_submissions`)
	if n != before {
		t.Fatal("batch partially committed")
	}
	f.call("POST", "/submissions/batch", fmt.Sprintf(`{"items":[{"operation_group_id":%q,"batch_no":"batch1","content":{}},{"operation_group_id":%q,"batch_no":"batch2","content":{}}]}`, ga.ID, ga.ID), 201)
	for _, p := range []string{"/submissions/x/draft", "/submissions/x/publish", "/units"} {
		f.call("POST", p, `{}`, 404)
	}
}
func TestG2QueriesStatisticsAndHistory(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	d := f.save(g.ID, "a", record)
	unknown := f.save(g.ID, "b", `{"sample_raw":"unparsed","sample_project":null,"counts":{"total_qty":0,"qualified_qty":0}}`)
	for _, suffix := range []string{"paper_inspector=" + url.QueryEscape("纸面甲"), "paper_submitter=" + url.QueryEscape("送检甲"), "paper_machine_operator=" + url.QueryEscape("操机甲"), "paper_handover_by=" + url.QueryEscape("交接甲"), "sample_project=M2", "machine_no=M01", "keyword=" + url.QueryEscape("原文"), "created_by=" + f.uid + "&batch_no=a", "last_saved_by=" + f.uid + "&batch_no=a", "from=2026-11-05&to=2026-11-05", "scope=SPLIT&batch_no=a"} {
		var l struct {
			Items []ledger.Detail `json:"items"`
			Total int             `json:"total"`
		}
		decode(t, f.call("GET", "/submissions?"+suffix, "", 200).Data, &l)
		if l.Total != 1 || l.Items[0].Submission.ID != d.Submission.ID {
			t.Fatal(suffix, l)
		}
	}
	for _, suffix := range []string{"unassigned=true", "incomplete=false&batch_no=b", "sample_project_missing=false&batch_no=b"} {
		var l struct {
			Total int `json:"total"`
		}
		decode(t, f.call("GET", "/submissions?"+suffix, "", 200).Data, &l)
		if l.Total != 0 {
			t.Fatal(suffix, l)
		}
	}
	var summary struct {
		Total struct {
			Counts struct {
				Total struct {
					Value *int64 `json:"value"`
				} `json:"total_qty"`
			} `json:"counts"`
			Rate *float64 `json:"pass_rate"`
		} `json:"total"`
		Included []string `json:"included_revision_ids"`
	}
	decode(t, f.call("GET", "/stats/summary?operation_group_id="+g.ID, "", 200).Data, &summary)
	if summary.Total.Counts.Total.Value == nil || *summary.Total.Counts.Total.Value != 100 || *summary.Total.Rate != 96 || len(summary.Included) != 2 {
		t.Fatal(summary)
	}
	f.call("GET", "/stats/summary?group_by=sample_project,sample_project", "", 400)
	f.call("GET", "/stats/summary?state=CANCELLED", "", 400)
	f.call("GET", "/stats/summary?inspection_kind=SAMPLING&operation_group_id="+g.ID, "", 422)
	var defects struct {
		Defects []struct {
			Code     string `json:"code"`
			Surfaces map[string]struct {
				Known   int64 `json:"known_sum"`
				Unknown int   `json:"unknown_count"`
			} `json:"surfaces"`
		} `json:"defects"`
	}
	decode(t, f.call("GET", "/stats/defects", "", 200).Data, &defects)
	if len(defects.Defects) != 35 || defects.Defects[0].Surfaces["concave"].Known != 2 || defects.Defects[0].Surfaces["concave"].Unknown != 1 {
		t.Fatal(defects)
	}
	var changes struct {
		Groups []struct {
			Count    int `json:"item_count"`
			Quantity struct {
				Value   *int64 `json:"value"`
				Known   int64  `json:"known_sum"`
				Unknown int    `json:"unknown_count"`
			} `json:"quantity"`
		} `json:"groups"`
	}
	decode(t, f.call("GET", "/stats/quantity-changes", "", 200).Data, &changes)
	if len(changes.Groups) != 1 || changes.Groups[0].Count != 1 || changes.Groups[0].Quantity.Value != nil || changes.Groups[0].Quantity.Known != 0 || changes.Groups[0].Quantity.Unknown != 1 {
		t.Fatal("unknown change quantity lost", changes)
	}
	f.call("GET", "/stats/dimensions", "", 200)
	f.call("GET", "/stats/workload?by=created_by", "", 200)
	f.call("GET", "/stats/workload?metric=save_actions&by=saved_by&from=2020-01-01T00:00:00Z&to=2090-01-01T00:00:00Z", "", 200)
	var snapshot struct {
		AsOf    string `json:"as_of"`
		Through int64  `json:"through_event_id"`
		Groups  []struct {
			Submissions []ledger.Detail `json:"submissions"`
		} `json:"groups"`
	}
	decode(t, f.call("GET", "/reports/product-lots/"+lot.ID, "", 200).Data, &snapshot)
	cutoff := fmt.Sprintf("?as_of=%s&through_event_id=%d", url.QueryEscape(snapshot.AsOf), snapshot.Through)
	original := f.call("GET", "/reports/product-lots/"+lot.ID+cutoff, "", 200)
	f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":1,"patch":{"counts":{"qualified_qty":80},"lines":[]}}`, 200)
	f.call("POST", "/submissions/"+unknown.Submission.ID+"/cancel", `{"expected_version":1,"reason":"录错"}`, 200)
	if string(f.call("GET", "/reports/product-lots/"+lot.ID+cutoff, "", 200).Data) != string(original.Data) {
		t.Fatal("past report changed")
	}
	f.call("GET", "/reports/product-lots/"+lot.ID+"?as_of=2000-01-01T00:00:00Z", "", 422)
	var intermediate int64
	f.db.Get(&intermediate, `SELECT MIN(event_id) FROM business_events WHERE action='occupancy'`)
	f.call("GET", "/reports/product-lots/"+lot.ID+fmt.Sprintf("?as_of=%s&through_event_id=%d", url.QueryEscape(snapshot.AsOf), intermediate), "", 422)
	rangePath := "/reports/operations/" + op + "?from=2026-11-05&to=2026-11-05&page_size=1"
	var report struct {
		Total   int             `json:"total"`
		Unknown int             `json:"unknown_day_count"`
		Summary json.RawMessage `json:"summary"`
	}
	decode(t, f.call("GET", rangePath, "", 200).Data, &report)
	if report.Total != 1 || report.Unknown != 1 {
		t.Fatal(report)
	}
	var report2 struct {
		Summary json.RawMessage `json:"summary"`
	}
	decode(t, f.call("GET", rangePath+"&page=2", "", 200).Data, &report2)
	if string(report.Summary) != string(report2.Summary) {
		t.Fatal("page changed summary")
	}
	f.call("GET", "/submissions/"+d.Submission.ID+"/revisions", "", 200)
	f.call("GET", "/submissions/"+d.Submission.ID+"/revisions/"+d.Revision.ID, "", 200)
	f.call("GET", "/submissions/"+d.Submission.ID+"/revisions/"+unknown.Revision.ID, "", 404)
	var current ledger.Detail
	decode(t, f.call("GET", "/submissions/"+d.Submission.ID, "", 200).Data, &current)
	f.call("GET", "/submissions/"+d.Submission.ID+"/diff?from="+d.Revision.ID+"&to="+current.Revision.ID, "", 200)
	f.call("GET", "/audit-logs?entity=submission&actor_id="+f.uid, "", 200)
	for _, p := range []string{"/submissions?state=bad", "/submissions?unassigned=1", "/submissions?from=invalid", "/submissions?page_size=101", "/stats/summary?group_by=not_a_column", "/submissions?machine_no=M01&machine_no=M02"} {
		status := 400
		if strings.Contains(p, "from=invalid") {
			status = 422
		}
		f.call("GET", p, "", status)
	}
}
func TestG2SharedParsingAndImport(t *testing.T) {
	f := newAPI(t)
	var n int
	f.db.Get(&n, `SELECT COUNT(*) FROM sample_parse_cache`)
	f.call("GET", "/sample/cache?sample_raw=absent", "", 200)
	var after int
	f.db.Get(&after, `SELECT COUNT(*) FROM sample_parse_cache`)
	if after != n {
		t.Fatal("cache GET wrote")
	}
	f.call("POST", "/dict/drawings/import", `{"items":[{"drawing_no":"D1","seq_no":"01","model_code":"VID2","spec_code":"S1","size_code":"MD","side_code":"R"},{"drawing_no":"D2","seq_no":"02","model_code":"VID2","spec_code":"S1","size_code":"MD","side_code":"R"}]}`, 200)
	f.call("POST", "/dict/drawings/import", `{"items":[{"drawing_no":"D3","seq_no":"03"},{"drawing_no":"D4","seq_no":"01"}]}`, 422)
	f.db.Get(&after, `SELECT COUNT(*) FROM sample_drawings`)
	if after != 2 {
		t.Fatal("import partially committed")
	}
	f.call("POST", "/dict/drawings/import", `{"rows":["bad"]}`, 400)
	f.call("GET", "/dict/drawings?page_size=1&page=2", "", 200)
	var parsed struct {
		Parsed struct {
			Raw     string  `json:"raw"`
			Project *string `json:"project"`
			IsRx    *bool   `json:"is_rx"`
		} `json:"parsed"`
		Candidates []dictionary.Drawing `json:"candidates"`
		Matched    bool                 `json:"matched"`
		Verified   bool                 `json:"verified"`
		FromCache  bool                 `json:"from_cache"`
	}
	raw := " M2 S1 VID2 MD R "
	decode(t, f.call("POST", "/sample/parse", fmt.Sprintf(`{"sample_raw":%q}`, raw), 200).Data, &parsed)
	if parsed.Parsed.Raw != raw || parsed.Parsed.IsRx != nil || parsed.Matched || len(parsed.Candidates) != 2 {
		t.Fatal(parsed)
	}
	f.call("POST", "/sample/confirm", fmt.Sprintf(`{"sample_raw":%q,"project":"M1","is_rx":null}`, raw), 200)
	decode(t, f.call("POST", "/sample/parse", `{"sample_raw":"ｍ２　ｓ１　ｖｉｄ２　ｍｄ　ｒ"}`, 200).Data, &parsed)
	if !parsed.Verified || !parsed.FromCache || *parsed.Parsed.Project != "M1" || parsed.Parsed.Raw != "ｍ２　ｓ１　ｖｉｄ２　ｍｄ　ｒ" {
		t.Fatal(parsed)
	}
	f.call("POST", "/dict/items", `{"category":"sample_model","code":"VID2","meta":{"aliases":["ND2"]}}`, 200)
	f.call("POST", "/sample/parse", `{"sample_raw":"M2 S2 ND2 SMR"}`, 200)
	f.call("GET", "/sample/cache?sample_raw=M2%20S2%20ND2%20SMR", "", 200)
	var conflict struct {
		Parsed struct {
			Project   *string             `json:"project"`
			Conflicts map[string][]string `json:"conflicts"`
		} `json:"parsed"`
	}
	decode(t, f.call("POST", "/sample/parse", `{"sample_raw":"M1 M2 S1"}`, 200).Data, &conflict)
	if conflict.Parsed.Project != nil || len(conflict.Parsed.Conflicts["project"]) != 2 {
		t.Fatal(conflict)
	}
	var missing struct {
		Parsed struct {
			Project *string `json:"project"`
			IsRx    *bool   `json:"is_rx"`
		} `json:"parsed"`
		Reason string `json:"unmatched_reason"`
	}
	decode(t, f.call("POST", "/sample/parse", `{"sample_raw":"S1 MD R VID2"}`, 200).Data, &missing)
	if missing.Parsed.Project != nil || missing.Parsed.IsRx != nil || missing.Reason != "partial" {
		t.Fatal(missing)
	}
	f.db.Get(&after, `SELECT COUNT(*) FROM inspection_submissions`)
	if after != 0 {
		t.Fatal("parse mutated ledger")
	}
	f.call("GET", "/healthz", "", 404)
}
func TestG2ConcurrentHTTPAndHealth(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v2/submissions", strings.NewReader(fmt.Sprintf(`{"operation_group_id":%q,"batch_no":"same","content":{}}`, g.ID)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.token)
			req.Header.Set("X-Idempotency-Key", fmt.Sprintf("concurrent-%d", i))
			w := httptest.NewRecorder()
			f.r.ServeHTTP(w, req)
			statuses <- w.Code
		}(i)
	}
	wg.Wait()
	close(statuses)
	sum := 0
	for s := range statuses {
		sum += s
	}
	if sum != 201+409 {
		t.Fatal("occupancy competition", sum)
	}
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	f.db.Close()
	w = httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatal("dependency failure health", w.Code, w.Body)
	}
}

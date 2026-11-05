package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/myonku/lens-qs-api/internal/ledger"
)

func TestG2CatalogAndWhitespaceQuery(t *testing.T) {
	f := newAPI(t)
	var catalog struct {
		Surfaces []struct {
			Code  string `json:"code"`
			Label string `json:"label"`
		} `json:"surfaces"`
		Defects []struct {
			Code  string `json:"code"`
			Order int    `json:"order"`
		} `json:"defect_categories"`
	}
	decode(t, f.call("GET", "/form-fields", "", 200).Data, &catalog)
	if len(catalog.Surfaces) != 3 || catalog.Surfaces[0].Code != "concave" || catalog.Surfaces[0].Label != "凹面" || len(catalog.Defects) != 35 || catalog.Defects[34].Order != 35 {
		t.Fatal(catalog)
	}
	f.save("", "", `{"sample_project":"\t　 ","sample_raw":"CaseSensitive"}`)
	var list struct {
		Total int `json:"total"`
	}
	decode(t, f.call("GET", "/submissions?sample_project_missing=true", "", 200).Data, &list)
	if list.Total != 1 {
		t.Fatal("Unicode whitespace not missing", list)
	}
	decode(t, f.call("GET", "/submissions?keyword=CaseSensitive", "", 200).Data, &list)
	if list.Total != 1 {
		t.Fatal(list)
	}
	decode(t, f.call("GET", "/submissions?keyword=casesensitive", "", 200).Data, &list)
	if list.Total != 0 {
		t.Fatal("keyword case semantics differ", list)
	}
	f.call("GET", "/audit-logs?entity=auth%7Cuser", "", 400)
}

func TestG2DrawingsBeyond500AndFullConfirmation(t *testing.T) {
	f := newAPI(t)
	items := make([]map[string]any, 501)
	for i := range items {
		items[i] = map[string]any{"drawing_no": fmt.Sprintf("D%03d", i), "seq_no": fmt.Sprintf("%03d", i)}
	}
	raw, e := json.Marshal(map[string]any{"items": items})
	if e != nil {
		t.Fatal(e)
	}
	f.call("POST", "/dict/drawings/import", string(raw), 200)
	var page struct {
		Items []struct {
			No   string `json:"drawing_no"`
			IsRx *bool  `json:"is_rx"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decode(t, f.call("GET", "/dict/drawings?page=6&page_size=100", "", 200).Data, &page)
	if page.Total != 501 || len(page.Items) != 1 || page.Items[0].No != "D500" || page.Items[0].IsRx != nil {
		t.Fatal("reference catalog truncated or unknown Rx lost", page)
	}
	response := f.call("POST", "/dict/drawings/import", `{"items":[{"drawing_no":"first"},{"drawing_no":"duplicate","seq_no":"000"}]}`, 422)
	if response.Details["index"] != float64(1) {
		t.Fatal(response)
	}
	var count int
	f.db.Get(&count, `SELECT COUNT(*) FROM sample_drawings`)
	if count != 501 {
		t.Fatal("bad import partially committed")
	}
	f.call("POST", "/sample/confirm", `{"sample_raw":"example","project":"M1","model":"VID2","is_rx":true}`, 200)
	f.call("POST", "/sample/confirm", `{"sample_raw":"example","project":"M2"}`, 200)
	var cache struct {
		Parsed struct {
			Project *string `json:"project"`
			Model   *string `json:"model"`
			IsRx    *bool   `json:"is_rx"`
		} `json:"parsed"`
		Verified bool `json:"verified"`
	}
	decode(t, f.call("GET", "/sample/cache?sample_raw=example", "", 200).Data, &cache)
	if !cache.Verified || cache.Parsed.Project == nil || *cache.Parsed.Project != "M2" || cache.Parsed.Model != nil || cache.Parsed.IsRx != nil {
		t.Fatal("confirmation merged instead of replacing", cache)
	}
}
func TestG2HistoricalBindingAndClosedGroup(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	u := f.save("", "a", record)
	before := f.call("GET", "/reports/product-lots/"+lot.ID, "", 200)
	var snap struct {
		At       string   `json:"as_of"`
		Through  int64    `json:"through_event_id"`
		Included []string `json:"included_revision_ids"`
	}
	decode(t, before.Data, &snap)
	if len(snap.Included) != 0 {
		t.Fatal("unassigned counted")
	}
	fixed := fmt.Sprintf("/reports/product-lots/%s?as_of=%s&through_event_id=%d", lot.ID, url.QueryEscape(snap.At), snap.Through)
	f.call("PATCH", "/submissions/"+u.Submission.ID, fmt.Sprintf(`{"expected_version":1,"operation_group_id":%q}`, g.ID), 200)
	var gd ledger.GroupDetail
	decode(t, f.call("GET", "/operation-groups/"+g.ID, "", 200).Data, &gd)
	f.call("POST", "/operation-groups/"+g.ID+"/close", fmt.Sprintf(`{"expected_version":%d,"reason":"结束"}`, gd.Group.Version), 200)
	after := f.call("GET", fixed, "", 200)
	if string(before.Data) != string(after.Data) {
		t.Fatal("binding or closing leaked into past")
	}
	unbound := f.save("", "b", `{}`)
	f.call("PATCH", "/submissions/"+unbound.Submission.ID, fmt.Sprintf(`{"expected_version":1,"operation_group_id":%q}`, g.ID), 409)
	f.call("POST", "/submissions/"+u.Submission.ID+"/cancel", `{"expected_version":2,"reason":"取消"}`, 200)
	var final ledger.Detail
	decode(t, f.call("GET", "/submissions/"+unbound.Submission.ID, "", 200).Data, &final)
	if final.Submission.GroupID != nil || final.Revision.No != 1 {
		t.Fatal("failed binding partially changed record")
	}
}

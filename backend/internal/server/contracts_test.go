package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/myonku/lens-qs-api/internal/ledger"
)

func TestG2ClockRollbackAndHistoricalLabels(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	if _, e := f.db.Exec(`UPDATE write_gate SET last_event_time='2030-01-01T00:00:00Z' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	d := f.save(g.ID, "a", record)
	before := f.call("GET", "/reports/product-lots/"+lot.ID, "", 200)
	var s struct {
		At      string `json:"as_of"`
		Through int64  `json:"through_event_id"`
	}
	decode(t, before.Data, &s)
	if s.At != "2030-01-01T00:00:00Z" {
		t.Fatal(s)
	}
	path := fmt.Sprintf("/reports/product-lots/%s?as_of=%s&through_event_id=%d", lot.ID, url.QueryEscape(s.At), s.Through)
	f.call("PATCH", "/dict/items/"+op, `{"label":"新工序名","active":false}`, 200)
	f.call("PATCH", "/users/"+f.uid, `{"display_name":"后来改名"}`, 200)
	f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":1,"patch":{"remark":"更正"}}`, 200)
	after := f.call("GET", path, "", 200)
	if string(after.Data) != string(before.Data) {
		t.Fatal("clock rollback or rename contaminated saved report")
	}
	f.call("GET", "/reports/product-lots/"+lot.ID+"?as_of=2031-01-01T00:00:00Z", "", 422)
}

func TestG2NewEndpointMatrix(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	d := f.save(g.ID, "a", record)
	reads := []string{"/product-lots", "/product-lots/" + lot.ID, "/product-lots/" + lot.ID + "/operation-groups", "/operation-groups/" + g.ID, "/submissions", "/submissions/" + d.Submission.ID, "/submissions/" + d.Submission.ID + "/revisions", "/submissions/" + d.Submission.ID + "/revisions/" + d.Revision.ID, "/submissions/" + d.Submission.ID + "/diff?from=" + d.Revision.ID + "&to=" + d.Revision.ID, "/stats/dimensions", "/stats/summary", "/stats/defects", "/stats/quantity-changes", "/stats/workload", "/reports/product-lots/" + lot.ID, "/reports/operations/" + op + "?from=2026-11-05&to=2026-11-05", "/audit-logs", "/dict/drawings", "/sample/cache?sample_raw="}
	for _, path := range reads {
		t.Run(path, func(t *testing.T) {
			f.call("GET", path, "", 200)
			separator := "?"
			if strings.Contains(path, "?") {
				separator = "&"
			}
			f.call("GET", path+separator+"unexpected=1", "", 400)
			f.call("GET", path, `{}`, 400)
		})
	}
	writes := []struct{ method, path string }{{"POST", "/product-lots"}, {"POST", "/product-lots/" + lot.ID + "/operation-groups"}, {"POST", "/operation-groups/" + g.ID + "/close"}, {"POST", "/operation-groups/" + g.ID + "/reopen"}, {"POST", "/submissions"}, {"POST", "/submissions/batch"}, {"PATCH", "/submissions/" + d.Submission.ID}, {"POST", "/submissions/" + d.Submission.ID + "/cancel"}, {"POST", "/sample/parse"}, {"POST", "/sample/confirm"}, {"POST", "/dict/drawings/import"}}
	for _, w := range writes {
		f.call(w.method, w.path, `{"unexpected":1}`, 400)
	}
	for _, path := range []string{"/product-lots/absent", "/operation-groups/absent", "/submissions/absent", "/submissions/absent/revisions", "/submissions/absent/revisions/absent", "/reports/product-lots/absent", "/reports/operations/absent?from=2026-11-05&to=2026-11-05"} {
		f.call("GET", path, "", 404)
	}
	f.call("GET", "/reports/product-lots/"+lot.ID+"?as_of=", "", 422)
	f.call("GET", "/reports/operations/"+op, "", 400)
	f.request("POST", "/api/v2/submissions", `{"content":{}}`, f.token, "", 400)
	f.request("POST", "/api/v2/submissions", `{"inspection_kind":"SAMPLING","content":{}}`, f.token, "kind-forbidden", 403)
	var info apiResponse = f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":99,"patch":{"remark":"stale"}}`, 409)
	if info.Details["reason"] != "version_conflict" || info.Details["current_version"] != float64(1) {
		t.Fatal(info)
	}
	info = f.call("PATCH", "/submissions/"+d.Submission.ID, `{"expected_version":1,"operation_group_id":null}`, 400)
	if info.Details["reason"] != "immutable_identity" {
		t.Fatal(info)
	}
}
func TestG2StatsNullPartitionsAndActions(t *testing.T) {
	f := newAPI(t)
	lot := f.lot()
	op := f.op("A")
	g := f.group(lot.ID, op, f.token)
	gs := f.group(lot.ID, op, f.sampling)
	a := f.save(g.ID, "a", `{"counts":{"total_qty":10,"qualified_qty":11}}`)
	f.save(g.ID, "b", `{"counts":{"total_qty":100,"qualified_qty":90}}`)
	unassigned := f.save("", "a", `{"counts":{"total_qty":999,"qualified_qty":999}}`)
	var summary struct {
		Total struct {
			Count  int      `json:"submission_count"`
			Rate   *float64 `json:"pass_rate"`
			Reason *string  `json:"pass_rate_reason"`
		} `json:"total"`
		Totals map[string]json.RawMessage `json:"totals"`
		Groups []json.RawMessage          `json:"groups"`
	}
	decode(t, f.call("GET", "/stats/summary?group_by=operation_id", "", 200).Data, &summary)
	if summary.Total.Count != 2 || summary.Total.Rate != nil || summary.Total.Reason == nil || *summary.Total.Reason != "incomparable_counts" {
		t.Fatal(summary)
	}
	f.request("POST", "/api/v2/submissions", fmt.Sprintf(`{"operation_group_id":%q,"batch_no":"a","content":{"counts":{"total_qty":20,"qualified_qty":20}}}`, gs.ID), f.sampling, "sample-valid", 201)
	decode(t, f.call("GET", "/stats/summary?inspection_kind=ALL&group_by=operation_id&page_size=1", "", 200).Data, &summary)
	if len(summary.Groups) != 1 || len(summary.Totals) != 2 {
		t.Fatal("type partitions lost", summary)
	}
	f.call("PATCH", "/submissions/"+a.Submission.ID, `{"expected_version":1,"patch":{"counts":null}}`, 200)
	var counts struct {
		Total struct {
			Counts struct {
				Total struct {
					Value   *int64 `json:"value"`
					Known   int64  `json:"known_sum"`
					Unknown int    `json:"unknown_count"`
				} `json:"total_qty"`
			} `json:"counts"`
		} `json:"total"`
	}
	decode(t, f.call("GET", "/stats/summary", "", 200).Data, &counts)
	if counts.Total.Counts.Total.Value != nil || counts.Total.Counts.Total.Known != 100 || counts.Total.Counts.Total.Unknown != 1 {
		t.Fatal(counts)
	}
	f.call("POST", "/submissions/"+a.Submission.ID+"/cancel", `{"expected_version":2,"reason":"取消"}`, 200)
	f.call("PATCH", "/submissions/"+unassigned.Submission.ID, fmt.Sprintf(`{"expected_version":1,"operation_group_id":%q,"batch_no":"c"}`, g.ID), 200)
	type actions struct {
		Items []struct {
			Count    int `json:"save_count"`
			Distinct int `json:"distinct_submission_count"`
		} `json:"items"`
	}
	var result actions
	query := "/stats/workload?metric=save_actions&from=2000-01-01T00:00:00Z&to=2090-01-01T00:00:00Z"
	decode(t, f.call("GET", query, "", 200).Data, &result)
	if len(result.Items) != 1 || result.Items[0].Count != 5 || result.Items[0].Distinct != 3 {
		t.Fatal("actions confused with current submissions", result)
	}
	decode(t, f.call("GET", query+"&product_lot_id="+lot.ID, "", 200).Data, &result)
	if result.Items[0].Count != 4 {
		t.Fatal("binding leaked into previous saves", result)
	}
	f.call("GET", query+"&machine_no=M01", "", 400)
	f.call("GET", "/stats/summary?paper_unit_text="+url.QueryEscape("一厂"), "", 422)
	var d ledger.Detail
	decode(t, f.call("GET", "/submissions/"+unassigned.Submission.ID, "", 200).Data, &d)
	if d.Revision.Content.SampleRaw != nil {
		t.Fatal("binding copied lot reference sample")
	}
}

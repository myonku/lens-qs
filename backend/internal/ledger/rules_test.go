package ledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }
func TestG1ContentRules(t *testing.T) {
	base, e := Merge(EmptyContent(), []byte(paper), "NORMAL")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		raw, kind string
		ok        bool
	}{
		{`{"counts":{}}`, "NORMAL", true}, {`{"counts":null}`, "NORMAL", true}, {`{"counts":{"total_qty":0}}`, "NORMAL", true},
		{`{"lines":[]}`, "NORMAL", true}, {`{"lines":null}`, "NORMAL", false}, {`{"quantity_changes":null}`, "NORMAL", false},
		{`{"declared_counts":{}}`, "NORMAL", false}, {`{"paper_inspected_raw":"yesterday"}`, "NORMAL", false}, {`{"pass_rate":96}`, "NORMAL", false},
		{`{"paper_unit_text":"一厂"}`, "NORMAL", false}, {`{"paper_unit_text":"片 "}`, "NORMAL", false}, {`{"paper_unit_id":"missing"}`, "NORMAL", false}, {`{"paper_unit_id":"unit-piece"}`, "NORMAL", true},
		{`{"counts":{"total_qty":-1}}`, "NORMAL", false}, {`{"counts":{"total_qty":9007199254740992}}`, "NORMAL", false}, {`{"counts":{"total_qty":1.0}}`, "NORMAL", false},
		{`{"counts":{"total_qty":null,"total_qty":0}}`, "NORMAL", false}, {`{"lines":[{"seq":1},{"seq":1}]}`, "NORMAL", false}, {`{"lines":[{"seq":1,"defects":{"unknown":{"both":0}}}]}`, "NORMAL", false},
		{`{"paper_inspected_date":"2025-02-29"}`, "NORMAL", false}, {`{"paper_inspected_date":"2024-02-29"}`, "NORMAL", true}, {`{"paper_inspected_at":"2026-11-04T16:30:00Z"}`, "NORMAL", true}, {`{"paper_inspected_at":"2026-11-05T00:00:00.1Z"}`, "NORMAL", false}, {`{"paper_inspected_at":"2026-11-05T00:00:00+24:00"}`, "NORMAL", false},
		{`{"quantity_changes":[{"kind":"LOSS","direction":"DECREASE","quantity":null,"reason":"只写原因"}]}`, "NORMAL", true}, {`{"quantity_changes":[{"kind":"LOSS","direction":"DECREASE","reason":"原因"}]}`, "SAMPLING", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			c, e := Merge(base, []byte(tc.raw), tc.kind)
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
			if e == nil && c.SampleRaw != nil && *c.SampleRaw != "  原始 型号  " {
				t.Fatal("raw changed")
			}
		})
	}
	cleared, e := Merge(base, []byte(`{"counts":null}`), "NORMAL")
	if e != nil || cleared.Counts.Total != nil || len(cleared.Lines) != 1 {
		t.Fatal("header backfilled", e)
	}
	c, e := Merge(EmptyContent(), []byte(`{"paper_inspected_at":"2026-11-05T00:30:00+08:00"}`), "NORMAL")
	if e != nil || *c.InspectedAt != "2026-11-04T16:30:00Z" || *Day(c.InspectedDate, c.InspectedAt) != "2026-11-05" {
		t.Fatal(c, e)
	}
	d, _, e := Derive(EmptyContent(), false, time.Now())
	if e != nil || strings.Join(d.Missing, ",") != "operation_id,paper_unit,paper_inspector,sample_raw,lines" || d.LineTotals["total_qty"].Value != nil {
		t.Fatal(d, e)
	}
	full, e := Merge(EmptyContent(), []byte(`{"lines":[{"seq":1,"defects":{"prick":{"concave":0}}}]}`), "NORMAL")
	if e != nil || len(full.Lines[0].Defects) != 35 || full.Lines[0].Defects["prick"].Concave == nil || full.Lines[0].Defects["scratch"].Both != nil {
		t.Fatal("105 unknown cells", e)
	}
}
func TestG1CountsComparabilityAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		contents []Content
		reason   string
		overflow bool
	}{
		{nil, "no_records", false}, {[]Content{{Counts: Counts{Total: ptr(int64(0)), Qualified: ptr(int64(0))}}}, "zero_denominator", false},
		{[]Content{{Counts: Counts{Total: ptr(int64(10)), Qualified: ptr(int64(11))}}, {Counts: Counts{Total: ptr(int64(100)), Qualified: ptr(int64(90))}}}, "incomparable_counts", false},
		{[]Content{{Counts: Counts{Qualified: ptr(int64(0))}}}, "incomplete_denominator", false},
		{[]Content{{Counts: Counts{Total: ptr(MaxQuantity)}}, {Counts: Counts{Total: ptr(int64(1))}}}, "", true},
	} {
		s, e := AggregateCounts(tc.contents)
		if tc.overflow {
			if e == nil {
				t.Fatal("overflow accepted")
			}
			continue
		}
		if e != nil || s.Reason == nil || *s.Reason != tc.reason {
			t.Fatal(s, e)
		}
	}
	c := EmptyContent()
	c.Lines = []Line{{Seq: 1, Total: ptr(MaxQuantity)}, {Seq: 2, Total: ptr(int64(1))}}
	if _, _, e := Derive(c, true, time.Now()); e == nil {
		t.Fatal("line overflow")
	}
	// Date precision switches must explicitly clear the old date.
	c = EmptyContent()
	c.InspectedDate = ptr("2026-11-05")
	if _, e := Merge(c, []byte(`{"paper_inspected_at":"2026-11-05T00:00:00Z"}`), "NORMAL"); e == nil {
		t.Fatal("implicit precision switch")
	}
	var roundtrip Content
	raw, _ := json.Marshal(c)
	if e := json.Unmarshal(raw, &roundtrip); e != nil {
		t.Fatal(e)
	}
}

func TestG1DefectUnknownIsPerSubmission(t *testing.T) {
	contents := []Content{{Lines: []Line{{Seq: 1, Defects: map[string]Cells{"prick": {Concave: ptr(int64(2))}}}, {Seq: 2, Defects: map[string]Cells{"prick": {Concave: nil}}}}}, {Lines: []Line{}}}
	out, e := AggregateDefects(contents)
	if e != nil {
		t.Fatal(e)
	}
	u := out["prick"]["concave"]
	if u.KnownSum != 2 || u.UnknownCount != 2 || u.Value != nil || u.Complete {
		t.Fatal(u)
	}
	empty, e := AggregateCounts(nil)
	if e != nil || !empty.Total.Complete || empty.Total.Value == nil || *empty.Total.Value != 0 {
		t.Fatal(empty, e)
	}
}

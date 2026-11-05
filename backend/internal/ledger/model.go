// Package ledger contains the round-one domain and storage foundation. No HTTP
// business routes are registered until round two.
package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

const MaxQuantity int64 = 9007199254740991
const PieceID = "unit-piece"

type Counts struct {
	Total     *int64 `json:"total_qty"`
	Qualified *int64 `json:"qualified_qty"`
	Defect    *int64 `json:"defect_qty"`
}
type Cells struct {
	Concave *int64 `json:"concave"`
	Convex  *int64 `json:"convex"`
	Both    *int64 `json:"both"`
}
type Line struct {
	Seq       int64            `json:"seq"`
	Total     *int64           `json:"total_qty"`
	Qualified *int64           `json:"qualified_qty"`
	Defect    *int64           `json:"defect_qty"`
	Defects   map[string]Cells `json:"defects"`
	Remark    *string          `json:"remark"`
}
type QuantityChange struct {
	Kind      string  `json:"kind"`
	Direction string  `json:"direction"`
	Quantity  *int64  `json:"quantity"`
	Reason    string  `json:"reason"`
	Date      *string `json:"occurred_date"`
	At        *string `json:"occurred_at"`
}
type Content struct {
	PaperUnitID          *string          `json:"paper_unit_id"`
	PaperUnitText        *string          `json:"paper_unit_text"`
	PaperSubmitter       *string          `json:"paper_submitter"`
	PaperInspector       *string          `json:"paper_inspector"`
	PaperMachineOperator *string          `json:"paper_machine_operator"`
	PaperHandoverBy      *string          `json:"paper_handover_by"`
	SubmittedDate        *string          `json:"paper_submitted_date"`
	SubmittedAt          *string          `json:"paper_submitted_at"`
	InspectedDate        *string          `json:"paper_inspected_date"`
	InspectedAt          *string          `json:"paper_inspected_at"`
	MachineNo            *string          `json:"machine_no"`
	SampleRaw            *string          `json:"sample_raw"`
	SampleProject        *string          `json:"sample_project"`
	SampleModel          *string          `json:"sample_model"`
	SampleSpec           *string          `json:"sample_spec"`
	SampleSize           *string          `json:"sample_size"`
	SampleSide           *string          `json:"sample_side"`
	Remark               *string          `json:"remark"`
	Counts               Counts           `json:"counts"`
	Lines                []Line           `json:"lines"`
	Changes              []QuantityChange `json:"quantity_changes"`
}

func EmptyContent() Content { return Content{Lines: []Line{}, Changes: []QuantityChange{}} }
func invalid(reason, path string) error {
	return httpx.Validation(reason, map[string]any{"reason": reason, "path": path})
}

// Merge is the sole patch boundary. Objects merge recursively; arrays replace.
// Typed content leaves this boundary; raw objects are never persisted or returned.
func Merge(base Content, raw []byte, kind string) (Content, error) {
	var patch map[string]json.RawMessage
	if err := httpx.DecodeStrict(raw, &patch); err != nil {
		return Content{}, err
	}
	if patch == nil {
		return Content{}, httpx.BadRequest("patch must be an object")
	}
	b, _ := json.Marshal(base)
	var values map[string]json.RawMessage
	_ = json.Unmarshal(b, &values)
	for key, value := range patch {
		if _, ok := values[key]; !ok {
			return Content{}, httpx.BadRequest("未知字段: " + key)
		}
		if (key == "lines" || key == "quantity_changes") && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Content{}, httpx.BadRequest(key + " must be an array")
		}
		if key == "counts" && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var old, part map[string]json.RawMessage
			_ = json.Unmarshal(values[key], &old)
			if err := json.Unmarshal(value, &part); err != nil || part == nil {
				return Content{}, httpx.BadRequest("counts must be an object or null")
			}
			for k, v := range part {
				if _, ok := old[k]; !ok {
					return Content{}, httpx.BadRequest("未知字段: counts." + k)
				}
				old[k] = v
			}
			value, _ = json.Marshal(old)
		}
		values[key] = value
	}
	b, _ = json.Marshal(values)
	c := EmptyContent()
	if err := httpx.DecodeStrict(b, &c); err != nil {
		return Content{}, err
	}
	if err := c.Validate(kind); err != nil {
		return Content{}, err
	}
	return c, nil
}
func validQuantity(n *int64, path string) error {
	if n != nil && (*n < 0 || *n > MaxQuantity) {
		return invalid("quantity_out_of_range", path)
	}
	return nil
}

var instantRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)

func eventDate(date, at **string, path string) error {
	if *date != nil && *at != nil {
		return invalid("date_precision_conflict", path)
	}
	if *date != nil {
		t, e := time.Parse("2006-01-02", **date)
		if e != nil || t.Year() < 1 || t.Format("2006-01-02") != **date {
			return invalid("invalid_date", path)
		}
	}
	if *at != nil {
		v := **at
		t, e := time.Parse(time.RFC3339, v)
		if e != nil || !instantRE.MatchString(v) || t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
			return invalid("invalid_instant", path)
		}
		if len(v) == 25 && (v[20:22] > "23" || v[23:25] > "59") {
			return invalid("invalid_instant", path)
		}
		s := t.UTC().Format(time.RFC3339)
		*at = &s
	}
	return nil
}
func (c *Content) Validate(kind string) error {
	if kind != "NORMAL" && kind != "SAMPLING" {
		return invalid("invalid_inspection_kind", "inspection_kind")
	}
	if (c.PaperUnitID != nil && *c.PaperUnitID != PieceID) || (c.PaperUnitText != nil && *c.PaperUnitText != "片") {
		return invalid("invalid_measurement_unit", "paper_unit")
	}
	if c.PaperUnitID != nil && c.PaperUnitText == nil {
		v := "片"
		c.PaperUnitText = &v
	}
	if e := eventDate(&c.SubmittedDate, &c.SubmittedAt, "paper_submitted"); e != nil {
		return e
	}
	if e := eventDate(&c.InspectedDate, &c.InspectedAt, "paper_inspected"); e != nil {
		return e
	}
	for i, n := range []*int64{c.Counts.Total, c.Counts.Qualified, c.Counts.Defect} {
		if e := validQuantity(n, "counts."+[]string{"total_qty", "qualified_qty", "defect_qty"}[i]); e != nil {
			return e
		}
	}
	if c.Lines == nil {
		c.Lines = []Line{}
	}
	if c.Changes == nil {
		c.Changes = []QuantityChange{}
	}
	seen := map[int64]bool{}
	codes := map[string]bool{}
	for _, d := range formfields.DefectCodes() {
		codes[d] = true
	}
	for i := range c.Lines {
		l := &c.Lines[i]
		path := fmt.Sprintf("lines[%d]", i)
		if l.Seq < 1 || l.Seq > MaxQuantity {
			return invalid("invalid_line_seq", path+".seq")
		}
		if seen[l.Seq] {
			return invalid("duplicate_line_seq", path+".seq")
		}
		seen[l.Seq] = true
		for _, n := range []*int64{l.Total, l.Qualified, l.Defect} {
			if e := validQuantity(n, path); e != nil {
				return e
			}
		}
		if l.Defects == nil {
			l.Defects = map[string]Cells{}
		}
		for k, v := range l.Defects {
			if !codes[k] {
				return httpx.BadRequest("未知缺陷: " + k)
			}
			for _, n := range []*int64{v.Concave, v.Convex, v.Both} {
				if e := validQuantity(n, path+".defects."+k); e != nil {
					return e
				}
			}
		}
		for k := range codes {
			if _, ok := l.Defects[k]; !ok {
				l.Defects[k] = Cells{}
			}
		}
	}
	for i := range c.Changes {
		q := &c.Changes[i]
		path := fmt.Sprintf("quantity_changes[%d]", i)
		if q.Kind != "LOSS" && q.Kind != "ADJUSTMENT" && q.Kind != "OTHER" {
			return invalid("invalid_change_kind", path)
		}
		if q.Direction != "DECREASE" && q.Direction != "INCREASE" && q.Direction != "UNKNOWN" {
			return invalid("invalid_change_direction", path)
		}
		if q.Kind == "LOSS" {
			if kind == "SAMPLING" {
				return invalid("sampling_loss_forbidden", path)
			}
			if q.Direction != "DECREASE" {
				return invalid("invalid_loss_direction", path)
			}
		}
		if strings.TrimSpace(q.Reason) == "" {
			return invalid("change_reason_required", path)
		}
		if e := validQuantity(q.Quantity, path); e != nil {
			return e
		}
		if e := eventDate(&q.Date, &q.At, path); e != nil {
			return e
		}
	}
	return nil
}
func Add(a, b int64) (int64, error) {
	if b < 0 || a < 0 || a > MaxQuantity-b {
		return 0, invalid("quantity_overflow", "quantity")
	}
	return a + b, nil
}

type CountUnit struct {
	Value        *int64 `json:"value"`
	KnownSum     int64  `json:"known_sum"`
	UnknownCount int    `json:"unknown_count"`
	Complete     bool   `json:"complete"`
}
type Difference struct {
	HeaderValue *int64 `json:"header_value"`
	LineValue   *int64 `json:"line_value"`
	Difference  *int64 `json:"difference"`
	Comparable  bool   `json:"comparable"`
}
type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}
type Derived struct {
	PassRate       *float64              `json:"pass_rate"`
	PassRateReason *string               `json:"pass_rate_reason"`
	LineTotals     map[string]CountUnit  `json:"line_totals"`
	Differences    map[string]Difference `json:"count_differences"`
	Complete       bool                  `json:"is_complete"`
	Missing        []string              `json:"missing_fields"`
	SubmittedDay   *string               `json:"paper_submitted_day"`
	InspectedDay   *string               `json:"paper_inspected_day"`
}

func Day(date, at *string) *string {
	if date != nil {
		return date
	}
	if at == nil {
		return nil
	}
	t, _ := time.Parse(time.RFC3339, *at)
	zone, _ := time.LoadLocation("Asia/Shanghai")
	v := t.In(zone).Format("2006-01-02")
	return &v
}
func blank(s *string) bool { return s == nil || strings.TrimSpace(*s) == "" }
func Derive(c Content, grouped bool, now time.Time) (Derived, []Issue, error) {
	d := Derived{LineTotals: map[string]CountUnit{}, Differences: map[string]Difference{}, Missing: []string{}, SubmittedDay: Day(c.SubmittedDate, c.SubmittedAt), InspectedDay: Day(c.InspectedDate, c.InspectedAt)}
	issues := []Issue{}
	warn := func(code, path string) { issues = append(issues, Issue{code, "warning", path, code}) }
	if !grouped {
		d.Missing = append(d.Missing, "operation_id")
	}
	if c.PaperUnitID == nil && c.PaperUnitText == nil {
		d.Missing = append(d.Missing, "paper_unit")
	}
	if blank(c.PaperInspector) {
		d.Missing = append(d.Missing, "paper_inspector")
	}
	if blank(c.SampleRaw) {
		d.Missing = append(d.Missing, "sample_raw")
	}
	if len(c.Lines) == 0 {
		d.Missing = append(d.Missing, "lines")
	}
	d.Complete = len(d.Missing) == 0
	for _, p := range d.Missing {
		warn("missing_information", p)
	}
	if blank(c.SampleProject) {
		warn("sample_project_missing", "sample_project")
	}
	reason := ""
	t, q, x := c.Counts.Total, c.Counts.Qualified, c.Counts.Defect
	if t == nil {
		reason = "incomplete_denominator"
	} else if q == nil {
		reason = "incomplete_numerator"
	} else if *q > *t {
		reason = "incomparable_counts"
	} else if *t == 0 {
		reason = "zero_denominator"
	} else {
		r := math.Round(float64(*q)/float64(*t)*10000) / 100
		d.PassRate = &r
	}
	if reason != "" {
		d.PassRateReason = &reason
	}
	if t != nil && q != nil && x != nil && *q+*x != *t {
		warn("counts_unbalanced", "counts")
	}
	names := []string{"total_qty", "qualified_qty", "defect_qty"}
	headers := []*int64{t, q, x}
	for j, name := range names {
		u := CountUnit{}
		for _, l := range c.Lines {
			n := []*int64{l.Total, l.Qualified, l.Defect}[j]
			if n == nil {
				u.UnknownCount++
			} else {
				var e error
				u.KnownSum, e = Add(u.KnownSum, *n)
				if e != nil {
					return d, nil, e
				}
			}
		}
		u.Complete = len(c.Lines) > 0 && u.UnknownCount == 0
		if u.Complete {
			v := u.KnownSum
			u.Value = &v
		}
		d.LineTotals[name] = u
		diff := Difference{HeaderValue: headers[j], LineValue: u.Value, Comparable: headers[j] != nil && u.Complete}
		if diff.Comparable {
			v := *headers[j] - u.KnownSum
			diff.Difference = &v
			if v != 0 {
				warn("line_counts_differ", "counts."+name)
			}
		}
		d.Differences[name] = diff
	}
	if t != nil {
		for j, n := range headers {
			if n != nil && *n > *t {
				warn("quantity_exceeds_total", "counts."+names[j])
			}
		}
	}
	for i, l := range c.Lines {
		if l.Total != nil {
			for j, n := range []*int64{l.Qualified, l.Defect} {
				if n != nil && *n > *l.Total {
					warn("quantity_exceeds_total", fmt.Sprintf("lines[%d].%s", i, []string{"qualified_qty", "defect_qty"}[j]))
				}
			}
		}
		sum := int64(0)
		complete := true
		for k, cell := range l.Defects {
			for _, n := range []*int64{cell.Concave, cell.Convex, cell.Both} {
				if n == nil {
					complete = false
					continue
				}
				var e error
				sum, e = Add(sum, *n)
				if e != nil {
					return d, nil, e
				}
				if l.Total != nil && *n > *l.Total {
					warn("quantity_exceeds_total", fmt.Sprintf("lines[%d].defects.%s", i, k))
				}
			}
		}
		if l.Defect != nil && (sum > *l.Defect || complete && sum != *l.Defect) {
			warn("defect_counts_overlap", fmt.Sprintf("lines[%d].defects", i))
		}
	}
	for _, pair := range []struct {
		date, at *string
		path     string
	}{{c.SubmittedDate, c.SubmittedAt, "paper_submitted"}, {c.InspectedDate, c.InspectedAt, "paper_inspected"}} {
		if pair.at != nil {
			t, _ := time.Parse(time.RFC3339, *pair.at)
			if t.After(now) {
				warn("paper_time_in_future", pair.path+"_at")
			}
		}
		if pair.date != nil {
			zone, _ := time.LoadLocation("Asia/Shanghai")
			if *pair.date > now.In(zone).Format("2006-01-02") {
				warn("paper_time_in_future", pair.path+"_date")
			}
		}
	}
	if c.SubmittedDate != nil && c.InspectedDate != nil && *c.SubmittedDate > *c.InspectedDate || c.SubmittedAt != nil && c.InspectedAt != nil && *c.SubmittedAt > *c.InspectedAt {
		warn("paper_time_order", "paper_inspected")
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].Path < issues[j].Path
	})
	return d, issues, nil
}

// AggregateCounts uses per-submission comparability before summing, so a bad
// numerator cannot be hidden by a second submission's denominator.
type Summary struct {
	Total     CountUnit
	Qualified CountUnit
	Defect    CountUnit
	PassRate  *float64
	Reason    *string
}

func AggregateCounts(contents []Content) (Summary, error) {
	s := Summary{}
	units := []*CountUnit{&s.Total, &s.Qualified, &s.Defect}
	bad := false
	for _, c := range contents {
		if c.Counts.Total != nil && c.Counts.Qualified != nil && *c.Counts.Qualified > *c.Counts.Total {
			bad = true
		}
		for j, n := range []*int64{c.Counts.Total, c.Counts.Qualified, c.Counts.Defect} {
			if n == nil {
				units[j].UnknownCount++
			} else {
				v, e := Add(units[j].KnownSum, *n)
				if e != nil {
					return s, e
				}
				units[j].KnownSum = v
			}
		}
	}
	for _, u := range units {
		u.Complete = u.UnknownCount == 0
		if u.Complete {
			v := u.KnownSum
			u.Value = &v
		}
	}
	r := ""
	switch {
	case len(contents) == 0:
		r = "no_records"
	case !s.Total.Complete:
		r = "incomplete_denominator"
	case !s.Qualified.Complete:
		r = "incomplete_numerator"
	case bad:
		r = "incomparable_counts"
	case s.Total.KnownSum == 0:
		r = "zero_denominator"
	default:
		v := math.Round(float64(s.Qualified.KnownSum)/float64(s.Total.KnownSum)*10000) / 100
		s.PassRate = &v
	}
	if r != "" {
		s.Reason = &r
	}
	return s, nil
}

func AggregateDefects(contents []Content) (map[string]map[string]CountUnit, error) {
	out := map[string]map[string]CountUnit{}
	for _, code := range formfields.DefectCodes() {
		out[code] = map[string]CountUnit{}
		for j, surface := range formfields.Surfaces {
			unit := CountUnit{}
			for _, c := range contents {
				unknown := len(c.Lines) == 0
				for _, l := range c.Lines {
					cell := l.Defects[code]
					n := []*int64{cell.Concave, cell.Convex, cell.Both}[j]
					if n == nil {
						unknown = true
					} else {
						v, e := Add(unit.KnownSum, *n)
						if e != nil {
							return nil, e
						}
						unit.KnownSum = v
					}
				}
				if unknown {
					unit.UnknownCount++
				}
			}
			unit.Complete = unit.UnknownCount == 0
			if unit.Complete {
				v := unit.KnownSum
				unit.Value = &v
			}
			out[code][surface] = unit
		}
	}
	return out, nil
}

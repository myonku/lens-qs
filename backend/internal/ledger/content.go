package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

type Object = map[string]any

var countFields = []string{"total_qty", "qualified_qty", "defect_qty"}
var textFields = strings.Fields("paper_unit_id paper_unit_text paper_submitter paper_inspector paper_machine_operator paper_handover_by machine_no paper_submitted_date paper_inspected_date paper_submitted_at paper_inspected_at paper_submitted_raw paper_inspected_raw sample_raw sample_project sample_model sample_spec sample_size sample_side remark")
var businessZone, _ = time.LoadLocation("Asia/Shanghai")
var instantRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)
var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func now() string      { return time.Now().UTC().Format(time.RFC3339) }
func str(v any) string { s, _ := v.(string); return s }
func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, e := n.Int64()
		return i, e == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == math.Trunc(n) && n >= -9007199254740991 && n <= 9007199254740991 {
			return int64(n), true
		}
	}
	return 0, false
}
func num(v any) int64       { n, _ := integer(v); return n }
func obj(v any) Object      { m, _ := v.(map[string]any); return m }
func clone(v Object) Object { b, _ := json.Marshal(v); m, _ := decode(b); return m }
func decode(b []byte) (Object, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v Object
	if e := d.Decode(&v); e != nil || v == nil {
		return nil, httpx.BadRequest("请求必须是 JSON 对象")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, httpx.BadRequest("请求只能包含一个 JSON 对象")
	}
	return v, nil
}
func allowed(m Object, names ...string) error {
	set := map[string]bool{}
	for _, s := range names {
		set[s] = true
	}
	for k := range m {
		if !set[k] {
			return httpx.BadRequest("未知字段: " + k)
		}
	}
	return nil
}
func validation(msg string) error { return httpx.Validation(msg, nil) }
func conflict(reason string, extra Object) error {
	if extra == nil {
		extra = Object{}
	}
	extra["reason"] = reason
	return httpx.Conflict(reason, extra)
}
func normalizeDate(s string) (string, error) {
	t, e := time.Parse("2006-01-02", s)
	if e != nil || !dateRE.MatchString(s) || t.Year() < 1 {
		return "", validation("非法日期: " + s)
	}
	return s, nil
}
func normalizeInstant(s string) (string, error) {
	if !instantRE.MatchString(s) {
		return "", validation("时点必须带时区并精确到秒")
	}
	if len(s) == 25 {
		h, _ := strconv.Atoi(s[20:22])
		m, _ := strconv.Atoi(s[23:25])
		if h > 23 || m > 59 {
			return "", validation("非法时区偏移")
		}
	}
	t, e := time.Parse(time.RFC3339, s)
	if e != nil || t.Year() < 1 || t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
		return "", validation("非法时点")
	}
	return t.UTC().Format(time.RFC3339), nil
}
func day(date, instant any) any {
	if str(date) != "" {
		return date
	}
	if str(instant) != "" {
		t, _ := time.Parse(time.RFC3339, str(instant))
		return t.In(businessZone).Format("2006-01-02")
	}
	return nil
}
func normalizeEvent(m Object, prefix string) error {
	d, a := prefix+"_date", prefix+"_at"
	if m[d] != nil && m[a] != nil {
		return validation(d + " 与 " + a + " 不能同时填写")
	}
	if m[d] != nil {
		v, e := normalizeDate(str(m[d]))
		if e != nil {
			return e
		}
		m[d] = v
	}
	if m[a] != nil {
		v, e := normalizeInstant(str(m[a]))
		if e != nil {
			return e
		}
		m[a] = v
	}
	return nil
}
func counts(m Object) error {
	if e := allowed(m, countFields...); e != nil {
		return e
	}
	for _, k := range countFields {
		v := m[k]
		if v != nil {
			n, ok := integer(v)
			if !ok {
				return httpx.BadRequest(k + " 必须为整数或 null")
			}
			if n < 0 {
				return validation(k + " 不能为负数")
			}
			m[k] = n
		} else {
			m[k] = nil
		}
	}
	return nil
}
func emptyContent() Object {
	m := Object{"counts": Object{}, "declared_counts": Object{}, "lines": []any{}, "quantity_changes": []any{}}
	for _, k := range textFields {
		m[k] = nil
	}
	return m
}
func mergeContent(base, patch Object) (Object, error) {
	names := append(append([]string{}, textFields...), "counts", "declared_counts", "lines", "quantity_changes")
	if e := allowed(patch, names...); e != nil {
		return nil, e
	}
	m := clone(base)
	for k, v := range patch {
		switch k {
		case "counts", "declared_counts":
			if v == nil {
				m[k] = Object{}
				continue
			}
			p := obj(v)
			if p == nil {
				return nil, httpx.BadRequest(k + " 必须为对象或 null")
			}
			if e := allowed(p, countFields...); e != nil {
				return nil, e
			}
			old := obj(m[k])
			if old == nil {
				old = Object{}
			}
			for f, x := range p {
				old[f] = x
			}
			m[k] = old
		case "lines", "quantity_changes":
			if _, ok := v.([]any); !ok {
				return nil, httpx.BadRequest(k + " 必须为数组")
			}
			m[k] = v
		default:
			if v != nil {
				if _, ok := v.(string); !ok {
					return nil, httpx.BadRequest(k + " 必须为字符串或 null")
				}
			}
			m[k] = v
		}
	}
	for _, k := range []string{"counts", "declared_counts"} {
		if e := counts(obj(m[k])); e != nil {
			return nil, e
		}
	}
	for _, p := range []string{"paper_submitted", "paper_inspected"} {
		if e := normalizeEvent(m, p); e != nil {
			return nil, e
		}
	}
	seen := map[int64]bool{}
	for _, v := range m["lines"].([]any) {
		r := obj(v)
		if r == nil {
			return nil, httpx.BadRequest("明细必须为对象")
		}
		if e := allowed(r, "seq", "total_qty", "qualified_qty", "defect_qty", "defects", "remark"); e != nil {
			return nil, e
		}
		seq, ok := integer(r["seq"])
		if !ok || seq < 1 || seen[seq] {
			return nil, validation("明细序号必须为不重复正整数")
		}
		seen[seq] = true
		c := Object{}
		for _, k := range countFields {
			c[k] = r[k]
		}
		if e := counts(c); e != nil {
			return nil, e
		}
		for k, v := range c {
			r[k] = v
		}
		if r["remark"] != nil {
			if _, ok := r["remark"].(string); !ok {
				return nil, httpx.BadRequest("remark 必须为字符串")
			}
		}
		ds := obj(r["defects"])
		if ds == nil && r["defects"] != nil {
			return nil, httpx.BadRequest("defects 必须为对象")
		}
		if ds == nil {
			ds = Object{}
		}
		if e := allowed(ds, formfields.DefectCodes()...); e != nil {
			return nil, e
		}
		for _, d := range formfields.DefectCodes() {
			cells := obj(ds[d])
			if ds[d] != nil && cells == nil {
				return nil, httpx.BadRequest("缺陷必须为对象")
			}
			if cells == nil {
				cells = Object{}
			}
			if e := allowed(cells, formfields.Surfaces...); e != nil {
				return nil, e
			}
			for _, s := range formfields.Surfaces {
				if cells[s] != nil {
					n, ok := integer(cells[s])
					if !ok {
						return nil, httpx.BadRequest("缺陷必须为整数或 null")
					}
					if n < 0 {
						return nil, validation("缺陷不能为负数")
					}
					cells[s] = n
				} else {
					cells[s] = nil
				}
			}
			ds[d] = cells
		}
		r["defects"] = ds
	}
	for _, v := range m["quantity_changes"].([]any) {
		q := obj(v)
		if q == nil {
			return nil, httpx.BadRequest("数量变化必须为对象")
		}
		if e := allowed(q, "kind", "direction", "quantity", "reason", "occurred_date", "occurred_at"); e != nil {
			return nil, e
		}
		if !contains([]string{"LOSS", "ADJUSTMENT", "OTHER"}, str(q["kind"])) || !contains([]string{"DECREASE", "INCREASE", "UNKNOWN"}, str(q["direction"])) {
			return nil, validation("非法数量变化枚举")
		}
		if q["kind"] == "LOSS" && q["direction"] != "DECREASE" {
			return nil, validation("LOSS 方向必须为 DECREASE")
		}
		if strings.TrimSpace(str(q["reason"])) == "" {
			return nil, validation("数量变化原因必填")
		}
		if q["quantity"] != nil {
			n, ok := integer(q["quantity"])
			if !ok {
				return nil, httpx.BadRequest("quantity 必须为整数或 null")
			}
			if n < 0 {
				return nil, validation("quantity 不能为负数")
			}
			q["quantity"] = n
		}
		if e := normalizeEvent(q, "occurred"); e != nil {
			return nil, e
		}
	}
	return m, nil
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func derived(content Object, grouped bool) Object {
	missing := []string{}
	if !grouped {
		missing = append(missing, "operation_group_id")
	}
	for _, f := range []string{"paper_inspector", "sample_raw"} {
		if strings.TrimSpace(str(content[f])) == "" {
			missing = append(missing, f)
		}
	}
	if str(content["paper_unit_id"]) == "" && str(content["paper_unit_text"]) == "" {
		missing = append(missing, "paper_unit")
	}
	if len(content["lines"].([]any)) == 0 {
		missing = append(missing, "lines")
	}
	issues := []any{}
	c := obj(content["counts"])
	total, tok := integer(c["total_qty"])
	qualified, qok := integer(c["qualified_qty"])
	defect, dok := integer(c["defect_qty"])
	var rate any
	reason := ""
	if !tok {
		reason = "incomplete_denominator"
	} else if !qok {
		reason = "incomplete_numerator"
	} else if total == 0 {
		reason = "zero_denominator"
	} else if qualified > total {
		reason = "numerator_exceeds_denominator"
	} else {
		rate = math.Round(float64(qualified)/float64(total)*10000) / 100
	}
	if tok && qok && dok && qualified+defect != total {
		issues = append(issues, Object{"rule": "quantity_equation", "level": "warning", "message": "合格量与不合格量之和不等于检验量"})
	}
	if len(missing) > 0 {
		issues = append(issues, Object{"rule": "missing_fields", "level": "warning", "fields": missing})
	}
	lineTotals := Object{}
	diff := Object{}
	for _, f := range countFields {
		var sum int64
		complete := len(content["lines"].([]any)) > 0
		for _, v := range content["lines"].([]any) {
			n, ok := integer(obj(v)[f])
			sum += n
			complete = complete && ok
		}
		if complete {
			lineTotals[f] = sum
			if known, ok := integer(c[f]); ok && known != sum {
				issues = append(issues, Object{"rule": "line_count_difference", "level": "warning", "field": f, "message": "明细合计与正式计数不同，未自动覆盖"})
			}
		} else {
			lineTotals[f] = nil
		}
		a, aok := integer(obj(content["declared_counts"])[f])
		b, bok := integer(c[f])
		if aok && bok {
			diff[f] = a - b
			if a != b {
				issues = append(issues, Object{"rule": "declared_count_difference", "level": "warning", "field": f, "message": "纸面声明与正式计数不同，未自动覆盖"})
			}
		} else {
			diff[f] = nil
		}
	}
	if !tok || !qok || !dok {
		issues = append(issues, Object{"rule": "unknown_counts", "level": "warning", "message": "正式计数含未知项，统计将明确标为不完整"})
	}
	var defectSum int64
	for _, line := range content["lines"].([]any) {
		for _, cells := range obj(obj(line)["defects"]) {
			for _, value := range obj(cells) {
				n, _ := integer(value)
				defectSum += n
			}
		}
	}
	if dok && defectSum > defect {
		issues = append(issues, Object{"rule": "defect_overlap_possible", "level": "warning", "message": "缺陷格合计超过不合格量，可能包含重复，不能视为唯一不良件数"})
	}
	return Object{"pass_rate": rate, "pass_rate_reason": reason, "line_totals": lineTotals, "declared_differences": diff, "is_complete": len(missing) == 0, "missing_fields": missing, "issues": issues, "paper_inspected_day": day(content["paper_inspected_date"], content["paper_inspected_at"]), "paper_submitted_day": day(content["paper_submitted_date"], content["paper_submitted_at"])}
}
func requiredString(m Object, k string) error {
	if _, ok := m[k].(string); !ok {
		return httpx.BadRequest(k + " 必须为字符串")
	}
	if strings.TrimSpace(str(m[k])) == "" {
		return validation(k + " 不能为空")
	}
	return nil
}
func normalizeBatch(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, httpx.BadRequest("batch_no 必须为字符串或 null")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	return s, nil
}
func stringify(v any) string       { b, _ := json.Marshal(v); return string(b) }
func formatErr(field string) error { return httpx.BadRequest(fmt.Sprintf("%s 格式不正确", field)) }

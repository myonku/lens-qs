package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/myonku/lens-qs-api/internal/formfields"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

var legacyDateRE = regexp.MustCompile(`^(\d{4})[./](\d{1,2})[./](\d{1,2})$`)

// ClassifyLegacyDate preserves evidence; it never invents a timezone or midnight.
func ClassifyLegacyDate(raw string) Object {
	r := Object{"raw": raw, "date": nil, "at": nil, "parser_rule": nil, "classification": "ambiguous"}
	s := strings.TrimSpace(raw)
	if s == "" {
		r["classification"] = "missing"
		return r
	}
	if v, e := normalizeDate(s); e == nil {
		r["date"] = v
		r["parser_rule"] = "iso_date"
		r["classification"] = "date"
		return r
	}
	if v, e := normalizeInstant(s); e == nil {
		r["at"] = v
		r["parser_rule"] = "rfc3339_seconds"
		r["classification"] = "instant"
		return r
	}
	if m := legacyDateRE.FindStringSubmatch(s); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if v, e := normalizeDate(fmt.Sprintf("%04d-%02d-%02d", year, month, d)); e == nil {
			r["date"] = v
			r["parser_rule"] = "year_first_dot_or_slash"
			r["classification"] = "date"
		} else {
			r["classification"] = "invalid"
		}
		return r
	}
	if dateRE.MatchString(s) {
		r["classification"] = "invalid"
	}
	return r
}
func (u *unit) sourceCandidates() []Object {
	out := []Object{}
	for _, f := range u.rows("SELECT * FROM forms ORDER BY id") {
		lines := u.rows("SELECT * FROM form_lines WHERE form_id = ? ORDER BY seq,id", f["id"])
		history := u.rows("SELECT * FROM form_history WHERE form_id = ? ORDER BY id", f["id"])
		evidence := Object{"form": f, "lines": lines, "history": history}
		hash := sha256.Sum256([]byte(stringify(evidence)))
		out = append(out, Object{"source_id": f["id"], "source_fingerprint": hex.EncodeToString(hash[:]), "evidence": evidence, "dates": Object{"paper_submitted": ClassifyLegacyDate(str(f["paper_submitted_at"])), "paper_inspected": ClassifyLegacyDate(str(f["paper_inspected_at"]))}, "review_requirements": []string{"complete_identity", "operation_mapping", "inspection_kind_evidence", "explicit_whole_or_batch", "counts_and_default_zero_semantics", "source_state_not_publication"}})
	}
	return out
}

// Inventory reads a consistent snapshot and never selects account secrets or tokens.
func (s *Service) Inventory(ctx context.Context) (any, error) {
	return s.read(ctx, func(u *unit) (any, error) {
		counts := Object{}
		for _, t := range []string{"users", "sessions", "dict_items", "units", "forms", "form_lines", "form_history", "audit_logs"} {
			r := u.get("SELECT COUNT(*) AS n FROM " + t)
			if r != nil {
				counts[t] = r["n"]
			}
		}
		dates := Object{}
		candidates := u.sourceCandidates()
		for _, c := range candidates {
			for _, v := range obj(c["dates"]) {
				k := str(obj(v)["classification"])
				dates[k] = num(dates[k]) + 1
			}
		}
		quality := Object{"inspection_kind_unknown": len(candidates), "batch_semantics_unknown": len(candidates)}
		for _, field := range []string{"form_no", "sample_raw", "operation_code", "paper_inspector", "paper_machine_operator"} {
			var missing int64
			for _, candidate := range candidates {
				form := obj(obj(candidate["evidence"])["form"])
				if strings.TrimSpace(str(form[field])) == "" {
					missing++
				}
			}
			quality[field+"_missing"] = missing
		}
		var zeroRisk int64
		operations := Object{}
		states := Object{}
		for _, candidate := range candidates {
			form := obj(obj(candidate["evidence"])["form"])
			zero := false
			for _, field := range countFields {
				if num(form[field]) == 0 {
					zero = true
				}
			}
			if zero {
				zeroRisk++
			}
			code := str(form["operation_code"])
			operations[code] = num(operations[code]) + 1
			state := str(form["state"])
			states[state] = num(states[state]) + 1
		}
		quality["default_zero_review_count"] = zeroRisk
		systemDates := Object{}
		for table, fields := range map[string][]string{"users": {"locked_until", "last_login_at", "created_at", "updated_at"}, "sessions": {"created_at", "last_seen_at", "expires_at", "revoked_at"}} {
			for _, f := range fields {
				var invalid int64
				for _, r := range u.rows("SELECT " + f + " AS value FROM " + table) {
					if r["value"] != nil && str(r["value"]) != "" {
						if _, e := normalizeInstant(str(r["value"])); e != nil {
							invalid++
						}
					}
				}
				systemDates[table+"."+f] = Object{"noncanonical_count": invalid}
			}
		}
		return Object{"as_of": now(), "counts": counts, "migrations": u.rows("SELECT version,name,applied_at FROM schema_migrations ORDER BY version"), "paper_date_classification": dates, "system_date_review": systemDates, "quality_review": quality, "operation_code_counts": operations, "legacy_state_counts": states, "source_count": len(candidates), "source_fingerprints": func() []Object {
			out := []Object{}
			for _, c := range candidates {
				out = append(out, Object{"source_id": c["source_id"], "source_fingerprint": c["source_fingerprint"]})
			}
			return out
		}(), "notes": []string{"未判定来源类型、完整身份及批次；旧默认零需要人工确认；不含账户凭据"}}, nil
	})
}

// Stage retains raw source evidence without reserving a slot or publishing anything.
func (s *Service) Stage(ctx context.Context, actor, key string) ([]byte, int, bool, error) {
	return s.Write(ctx, actor, key, fingerprint("STAGE", "legacy", nil), func(u *unit) (any, int, error) {
		candidates := u.sourceCandidates()
		for _, c := range candidates {
			old := u.get("SELECT * FROM legacy_migration_candidates WHERE source_id = ?", c["source_id"])
			if old != nil {
				if old["submission_id"] != nil && old["source_fingerprint"] != c["source_fingerprint"] {
					return nil, 0, conflict("legacy_source_changed", Object{"source_id": c["source_id"]})
				}
				if old["submission_id"] == nil {
					u.exec("UPDATE legacy_migration_candidates SET source_fingerprint = ?, candidate = ? WHERE source_id = ?", c["source_fingerprint"], stringify(c), c["source_id"])
				}
				continue
			}
			u.insert("legacy_migration_candidates", Object{"source_id": c["source_id"], "source_fingerprint": c["source_fingerprint"], "candidate": stringify(c), "review_status": "PENDING"})
		}
		return Object{"staged_count": len(candidates)}, 200, nil
	})
}
func legacyContent(candidate Object) Object {
	ev := obj(candidate["evidence"])
	f := obj(ev["form"])
	c := emptyContent()
	for _, k := range textFields {
		if v, ok := f[k]; ok && !strings.HasSuffix(k, "_at") {
			c[k] = v
		}
	}
	for _, p := range []string{"paper_submitted", "paper_inspected"} {
		d := obj(obj(candidate["dates"])[p])
		c[p+"_date"] = d["date"]
		c[p+"_at"] = d["at"]
		c[p+"_raw"] = d["raw"]
	}
	for _, k := range []string{"counts", "declared_counts"} {
		cc := Object{}
		for _, field := range countFields {
			cc[field] = f[field]
		}
		c[k] = cc
	}
	lines := []any{}
	raw, _ := jsonObjects(ev["lines"])
	for i, l := range raw {
		line := Object{"seq": i + 1, "remark": l["remark"]}
		for _, field := range countFields {
			line[field] = l[field]
		}
		ds := Object{}
		for _, d := range formfields.DefectCodes() {
			cells := Object{}
			for _, surface := range formfields.Surfaces {
				cells[surface] = l[d+"_"+surface]
			}
			ds[d] = cells
		}
		line["defects"] = ds
		lines = append(lines, line)
	}
	c["lines"] = lines
	return c
}
func jsonObjects(v any) ([]Object, error) {
	b := stringify(Object{"items": v})
	m, e := decode([]byte(b))
	if e != nil {
		return nil, e
	}
	out := []Object{}
	for _, r := range m["items"].([]any) {
		out = append(out, obj(r))
	}
	return out, nil
}

// ImportReviewed accepts an explicit review manifest and creates only drafts. The
// source fingerprint prevents stale reviews; the mapping makes reruns idempotent.
func (s *Service) ImportReviewed(ctx context.Context, actor, key string, body []byte) ([]byte, int, bool, error) {
	manifest, e := decode(body)
	if e != nil {
		return nil, 0, false, e
	}
	if e = allowed(manifest, "items"); e != nil {
		return nil, 0, false, e
	}
	items, ok := manifest["items"].([]any)
	if !ok || len(items) < 1 || len(items) > 100 {
		return nil, 0, false, httpx.BadRequest("items 需要 1–100 条人工核对记录")
	}
	return s.Write(ctx, actor, key, fingerprint("IMPORT", "legacy", body), func(u *unit) (any, int, error) {
		source := map[string]Object{}
		for _, c := range u.sourceCandidates() {
			source[str(c["source_id"])] = c
		}
		out := []any{}
		for index, v := range items {
			req := obj(v)
			if req == nil {
				return nil, 0, formatErr("items")
			}
			if e := allowed(req, "source_id", "source_fingerprint", "shared_form_no", "sample_identity", "operation_id", "inspection_kind", "batch_no", "counts_confirmed", "reason"); e != nil {
				return nil, 0, e
			}
			for _, k := range []string{"source_id", "source_fingerprint", "shared_form_no", "sample_identity", "operation_id", "inspection_kind", "reason"} {
				if e := requiredString(req, k); e != nil {
					return nil, 0, e
				}
			}
			if req["counts_confirmed"] != true {
				return nil, 0, validation("必须确认表头计数与旧默认零语义")
			}
			if _, ok := req["batch_no"]; !ok {
				return nil, 0, httpx.BadRequest("人工核对必须明确给出 batch_no；整批用 null")
			}
			if !contains([]string{"NORMAL", "SAMPLING"}, str(req["inspection_kind"])) {
				return nil, 0, formatErr("inspection_kind")
			}
			c := source[str(req["source_id"])]
			if c == nil {
				return nil, 0, httpx.NotFound("旧记录不存在")
			}
			if c["source_fingerprint"] != req["source_fingerprint"] {
				return nil, 0, conflict("legacy_source_changed", Object{"index": index})
			}
			row := u.get("SELECT * FROM legacy_migration_candidates WHERE source_id = ?", req["source_id"])
			if row == nil {
				return nil, 0, validation("请先暂存旧记录")
			}
			if row["submission_id"] != nil {
				if row["source_fingerprint"] != req["source_fingerprint"] {
					return nil, 0, conflict("legacy_source_changed", nil)
				}
				stored, _ := decode([]byte(str(row["candidate"])))
				if stringify(stored["review"]) != stringify(req) {
					return nil, 0, conflict("legacy_mapping_already_reviewed", nil)
				}
				sub, e := u.must("inspection_submissions", str(row["submission_id"]))
				if e != nil {
					return nil, 0, e
				}
				out = append(out, u.detail(sub))
				continue
			}
			no := strings.TrimSpace(str(req["shared_form_no"]))
			identity := strings.TrimSpace(str(req["sample_identity"]))
			lot := u.get("SELECT * FROM product_lots WHERE shared_form_no = ? AND sample_identity_key = ?", no, identity)
			if lot == nil {
				v, e := u.createLot(Object{"shared_form_no": no, "sample_identity": identity, "sample_raw": obj(obj(c["evidence"])["form"])["sample_raw"]})
				if e != nil {
					return nil, 0, e
				}
				lot = obj(v)
			}
			g := u.get("SELECT * FROM operation_groups WHERE product_lot_id = ? AND operation_id = ? AND inspection_kind = ?", lot["id"], req["operation_id"], req["inspection_kind"])
			if g == nil {
				v, e := u.createGroup(str(lot["id"]), Object{"operation_id": req["operation_id"], "inspection_kind": req["inspection_kind"]})
				if e != nil {
					return nil, 0, e
				}
				g = obj(v)
			}
			data, e := u.createSubmission(Object{"operation_group_id": g["id"], "batch_no": req["batch_no"], "inspection_kind": req["inspection_kind"], "content": legacyContent(c)})
			if e != nil {
				return nil, 0, e
			}
			d := obj(data)
			sub := obj(d["submission"])
			c["review"] = req
			u.exec("UPDATE legacy_migration_candidates SET source_fingerprint = ?, candidate = ?, review_status = 'IMPORTED_DRAFT', submission_id = ?, reviewed_by = ?, reviewed_at = ?, reason = ? WHERE source_id = ?", req["source_fingerprint"], stringify(c), sub["id"], actor, now(), req["reason"], req["source_id"])
			u.audit("inspection_submission", "legacy_import", nil, sub, str(req["reason"]))
			out = append(out, d)
		}
		return Object{"items": out}, 201, nil
	})
}

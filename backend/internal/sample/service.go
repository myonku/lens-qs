package sample

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

const parserVersion = "v2-1"

type ParsedResult struct {
	Raw        *string             `json:"raw"`
	Project    *string             `json:"project"`
	Model      *string             `json:"model"`
	Spec       *string             `json:"spec"`
	Size       *string             `json:"size"`
	Side       *string             `json:"side"`
	Class      *string             `json:"class"`
	IsRx       *bool               `json:"is_rx"`
	Tokens     []string            `json:"tokens"`
	Unknown    []string            `json:"unknown_tokens"`
	Confidence float64             `json:"confidence"`
	Conflicts  map[string][]string `json:"conflicts,omitempty"`
}
type Result struct {
	Parsed            ParsedResult         `json:"parsed"`
	Candidates        []dictionary.Drawing `json:"candidates"`
	Drawing           *dictionary.Drawing  `json:"drawing"`
	Matched           bool                 `json:"matched"`
	FromCache         bool                 `json:"from_cache"`
	Verified          bool                 `json:"verified"`
	VerifiedBy        *string              `json:"verified_by"`
	VerifiedAt        *string              `json:"verified_at"`
	Reason            *string              `json:"unmatched_reason"`
	Note              *string              `json:"note"`
	DictionaryVersion string               `json:"dictionary_version"`
	ParserVersion     string               `json:"parser_version"`
	NeedsReview       bool                 `json:"needs_review"`
}
type cacheRow struct {
	ID                string  `db:"id"`
	Parsed            *string `db:"parsed"`
	VerifiedBy        *string `db:"verified_by"`
	VerifiedAt        *string `db:"verified_at"`
	DictionaryVersion string  `db:"dictionary_version"`
	ParserVersion     string  `db:"parser_version"`
	NeedsReview       bool    `db:"needs_review"`
}

const cacheColumns = `id,parsed,verified_by,verified_at,dictionary_version,parser_version,needs_review`

func pstr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func catalogVersion(ctx context.Context, q store.Reader) (string, error) {
	var n int64
	e := q.GetContext(ctx, &n, `SELECT version FROM shared_catalog_state WHERE id=1`)
	return fmt.Sprint(n), e
}
func parseWithDictionary(raw *string, items []dictionary.Item) ParsedResult {
	text := ""
	if raw != nil {
		text = *raw
	}
	tokens := Tokenize(text)
	out := ParsedResult{Raw: raw, Tokens: tokens, Unknown: []string{}, Conflicts: map[string][]string{}}
	type alias struct {
		field, code string
		tokens      []string
	}
	aliases := []alias{}
	for _, it := range items {
		if !strings.HasPrefix(it.Category, "sample_") {
			continue
		}
		field := strings.TrimPrefix(it.Category, "sample_")
		names := []string{it.Code}
		if it.Meta != nil {
			var m struct {
				Aliases []string `json:"aliases"`
			}
			if json.Unmarshal([]byte(*it.Meta), &m) == nil {
				names = append(names, m.Aliases...)
			}
		}
		for _, name := range names {
			ts := Tokenize(name)
			if len(ts) > 0 {
				aliases = append(aliases, alias{field, it.Code, ts})
			}
		}
	}
	sort.Slice(aliases, func(i, j int) bool { return len(aliases[i].tokens) > len(aliases[j].tokens) })
	values := map[string]map[string]bool{}
	add := func(field, value string) {
		if value == "" {
			return
		}
		if values[field] == nil {
			values[field] = map[string]bool{}
		}
		values[field][value] = true
	}
	rx := false
	for i := 0; i < len(tokens); {
		longest := 0
		hits := []alias{}
		for _, a := range aliases {
			if i+len(a.tokens) > len(tokens) {
				continue
			}
			match := true
			for j, t := range a.tokens {
				if !strings.EqualFold(t, tokens[i+j]) {
					match = false
					break
				}
			}
			if match {
				if len(a.tokens) > longest {
					longest = len(a.tokens)
					hits = nil
				}
				if len(a.tokens) == longest {
					hits = append(hits, a)
				}
			}
		}
		if longest > 0 {
			for _, a := range hits {
				add(a.field, a.code)
				if strings.Contains(strings.ToUpper(a.code), "RX") {
					rx = true
				}
			}
			i += longest
			continue
		}
		tok := tokens[i]
		p := Parse(tok, nil)
		recognized := false
		for field, value := range map[string]string{"project": p.Project, "model": p.Model, "spec": p.Spec, "size": p.Size, "side": p.Side, "class": p.Class} {
			if value != "" {
				add(field, value)
				recognized = true
			}
		}
		if p.IsRx {
			rx = true
			recognized = true
		}
		if !recognized && !ampRe.MatchString(tok) {
			out.Unknown = append(out.Unknown, tok)
		}
		i++
	}
	if rx {
		v := true
		out.IsRx = &v
		if len(values["model"]) == 1 {
			for value := range values["model"] {
				if !strings.Contains(strings.ToUpper(value), "RX") {
					delete(values["model"], value)
					values["model"][value+" Rx"] = true
				}
			}
		}
	}
	if values["class"]["MONOC"] && values["class"]["BINOC"] {
		values["class"] = map[string]bool{"MONOC_AND_BINOC": true}
	}
	for field, vs := range values {
		list := []string{}
		for v := range vs {
			list = append(list, v)
		}
		sort.Strings(list)
		if len(list) > 1 {
			out.Conflicts[field] = list
			continue
		}
		if len(list) == 0 {
			continue
		}
		v := pstr(list[0])
		switch field {
		case "project":
			out.Project = v
		case "model":
			out.Model = v
		case "spec":
			out.Spec = v
		case "size":
			out.Size = v
		case "side":
			out.Side = v
		case "class":
			out.Class = v
		}
	}
	n := 0
	for _, v := range []*string{out.Project, out.Model, out.Spec, out.Size, out.Side} {
		if v != nil && strings.TrimSpace(*v) != "" {
			n++
		}
	}
	out.Confidence = float64(n) * 0.18
	return out
}
func finish(ctx context.Context, q store.Reader, p ParsedResult) (Result, error) {
	r := Result{Parsed: p, Candidates: []dictionary.Drawing{}, ParserVersion: parserVersion}
	where := ` WHERE active=1`
	args := []any{}
	dimensions := 0
	for _, item := range []struct {
		column string
		value  *string
	}{{"project_code", p.Project}, {"model_code", p.Model}, {"spec_code", p.Spec}, {"size_code", p.Size}, {"side_code", p.Side}} {
		if item.value != nil && strings.TrimSpace(*item.value) != "" {
			dimensions++
			if item.column == "project_code" {
				where += ` AND (project_code=? OR project_code IS NULL)`
			} else {
				where += ` AND ` + item.column + `=?`
			}
			args = append(args, *item.value)
		}
	}
	if dimensions > 0 && len(p.Conflicts) == 0 {
		if e := q.SelectContext(ctx, &r.Candidates, q.Rebind(`SELECT `+dictionary.DrawingColumns+` FROM sample_drawings`+where+` ORDER BY CASE WHEN seq_no IS NULL THEN 1 ELSE 0 END,seq_no,drawing_no,id`), args...); e != nil {
			return r, e
		}
	}
	reason := ""
	note := ""
	switch {
	case dimensions == 0:
		reason = "raw_only"
		note = "未识别核心字段，请按原文填写"
	case dimensions < 5:
		reason = "partial"
		note = "部分字段未识别，请按原文核对"
	case len(r.Candidates) == 0:
		reason = "no_drawing"
		note = "参考目录无匹配，仍可保存"
	case len(r.Candidates) > 1:
		reason = "ambiguous"
		note = "存在多个候选，请人工确认"
	}
	if dimensions == 5 && len(r.Candidates) == 1 && len(p.Conflicts) == 0 {
		r.Matched = true
		d := r.Candidates[0]
		r.Drawing = &d
	}
	r.Reason = pstr(reason)
	r.Note = pstr(note)
	return r, nil
}
func decodeCache(c cacheRow, raw *string) (ParsedResult, error) {
	var p ParsedResult
	if c.Parsed == nil {
		return p, fmt.Errorf("empty cached mapping")
	}
	if e := json.Unmarshal([]byte(*c.Parsed), &p); e != nil {
		return p, e
	}
	if c.NeedsReview {
		var m Mapping
		_ = json.Unmarshal([]byte(*c.Parsed), &m)
		if p.Project == nil {
			p.Project = m.Project
		}
		if p.Model == nil {
			p.Model = m.Model
		}
		if p.Spec == nil {
			p.Spec = m.Spec
		}
		if p.Size == nil {
			p.Size = m.Size
		}
		if p.Side == nil {
			p.Side = m.Side
		}
		if p.Class == nil {
			p.Class = m.Class
		}
		if p.IsRx == nil {
			p.IsRx = m.IsRx
		}
	}
	p.Raw = raw
	text := ""
	if raw != nil {
		text = *raw
	}
	p.Tokens = Tokenize(text)
	if p.Unknown == nil {
		p.Unknown = []string{}
	}
	return p, nil
}
func ParseAndCache(ctx context.Context, db *sqlx.DB, raw *string, confirmation *ParsedResult) (Result, error) {
	w, e := store.BeginWrite(ctx, db, "", false, nil)
	if e != nil {
		return Result{}, e
	}
	defer w.Close()
	text := ""
	if raw != nil {
		text = *raw
	}
	key := NormalizeKey(text)
	version, e := catalogVersion(w.Context, w)
	if e != nil {
		return Result{}, e
	}
	var cached cacheRow
	e = w.GetContext(w.Context, &cached, w.Rebind(`SELECT `+cacheColumns+` FROM sample_parse_cache WHERE raw_key=?`), key)
	exists := e == nil
	if e != nil && e != sql.ErrNoRows {
		return Result{}, e
	}
	valid := exists && (cached.VerifiedBy != nil || cached.DictionaryVersion == version && cached.ParserVersion == parserVersion)
	var p ParsedResult
	if confirmation != nil {
		p = *confirmation
		p.Raw = raw
		p.Tokens = Tokenize(text)
		p.Unknown = []string{}
	} else if valid {
		p, e = decodeCache(cached, raw)
		if e != nil {
			return Result{}, e
		}
	} else {
		items := []dictionary.Item{}
		if e = w.SelectContext(w.Context, &items, `SELECT id,category,code,label,sort_no,active,meta,created_at,updated_at FROM dict_items WHERE active=1 ORDER BY category,sort_no,code`); e != nil {
			return Result{}, e
		}
		p = parseWithDictionary(raw, items)
	}
	result, e := finish(w.Context, w, p)
	if e != nil {
		return Result{}, e
	}
	result.DictionaryVersion = version
	if confirmation == nil && valid {
		result.FromCache = true
		result.Verified = cached.VerifiedBy != nil
		result.VerifiedBy = cached.VerifiedBy
		result.VerifiedAt = cached.VerifiedAt
		result.DictionaryVersion = cached.DictionaryVersion
		result.ParserVersion = cached.ParserVersion
		result.NeedsReview = cached.NeedsReview || cached.DictionaryVersion != version || cached.ParserVersion != parserVersion
	}
	if confirmation != nil {
		if strings.TrimSpace(text) == "" {
			return Result{}, httpx.BadRequest("sample_raw 必填")
		}
		result.Verified = true
		result.VerifiedBy = &w.Actor.ID
		result.VerifiedAt = &w.Time
	}
	if key != "" {
		if !exists {
			cached.ID = id.New()
		}
		if confirmation != nil || !valid {
			b, e := json.Marshal(p)
			if e != nil {
				return Result{}, e
			}
			if exists {
				_, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sample_parse_cache SET raw_sample=?,parsed=?,verified_by=?,verified_at=?,dictionary_version=?,parser_version=?,needs_review=0,updated_at=?,hit_count=hit_count+1 WHERE id=?`), text, string(b), result.VerifiedBy, result.VerifiedAt, version, parserVersion, w.Time, cached.ID)
			} else {
				_, e = w.ExecContext(w.Context, w.Rebind(`INSERT INTO sample_parse_cache(id,raw_key,raw_sample,parsed,verified_by,verified_at,dictionary_version,parser_version,needs_review,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,0,?,?)`), cached.ID, key, text, string(b), result.VerifiedBy, result.VerifiedAt, version, parserVersion, w.Time, w.Time)
			}
			if e != nil {
				return Result{}, e
			}
		} else {
			if _, e = w.ExecContext(w.Context, w.Rebind(`UPDATE sample_parse_cache SET hit_count=hit_count+1,updated_at=? WHERE id=?`), w.Time, cached.ID); e != nil {
				return Result{}, e
			}
		}
		if confirmation != nil {
			if e = w.Event("confirm", "sample_cache", cached.ID, nil, p, nil); e != nil {
				return Result{}, e
			}
		}
	}
	return result, w.Commit()
}
func Register(g *gin.RouterGroup, db *sqlx.DB) {
	g.POST("/sample/parse", func(c *gin.Context) {
		if e := httpx.Query(c); e != nil {
			httpx.Fail(c, e)
			return
		}
		var in struct {
			Raw *string `json:"sample_raw"`
		}
		if e := httpx.BindJSON(c, &in); e != nil {
			httpx.Fail(c, e)
			return
		}
		result, e := ParseAndCache(c.Request.Context(), db, in.Raw, nil)
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		httpx.OK(c, result)
	})
	g.POST("/sample/confirm", func(c *gin.Context) {
		if e := httpx.Query(c); e != nil {
			httpx.Fail(c, e)
			return
		}
		var in struct {
			Raw     string  `json:"sample_raw"`
			Project *string `json:"project"`
			Model   *string `json:"model"`
			Spec    *string `json:"spec"`
			Size    *string `json:"size"`
			Side    *string `json:"side"`
			Class   *string `json:"class"`
			IsRx    *bool   `json:"is_rx"`
		}
		if e := httpx.BindJSON(c, &in); e != nil {
			httpx.Fail(c, e)
			return
		}
		if strings.TrimSpace(in.Raw) == "" {
			httpx.Fail(c, httpx.BadRequest("sample_raw 必填"))
			return
		}
		p := ParsedResult{Project: in.Project, Model: in.Model, Spec: in.Spec, Size: in.Size, Side: in.Side, Class: in.Class, IsRx: in.IsRx, Confidence: 1}
		result, e := ParseAndCache(c.Request.Context(), db, &in.Raw, &p)
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		httpx.OK(c, result)
	})
	g.GET("/sample/cache", func(c *gin.Context) {
		if e := httpx.Query(c, "sample_raw"); e != nil {
			httpx.Fail(c, e)
			return
		}
		if !c.Request.URL.Query().Has("sample_raw") {
			httpx.Fail(c, httpx.BadRequest("sample_raw 参数必填"))
			return
		}
		raw := c.Query("sample_raw")
		key := NormalizeKey(raw)
		var result any
		_, e := store.ReadView(c.Request.Context(), db, func(q store.Reader, _ store.Snapshot) error {
			out := gin.H{"normalized_key": key, "from_cache": false, "parsed": nil, "verified": nil, "verified_by": nil, "verified_at": nil}
			var row cacheRow
			e := q.GetContext(c.Request.Context(), &row, q.Rebind(`SELECT `+cacheColumns+` FROM sample_parse_cache WHERE raw_key=?`), key)
			if e == sql.ErrNoRows {
				result = out
				return nil
			}
			if e != nil {
				return e
			}
			version, e := catalogVersion(c.Request.Context(), q)
			if e != nil {
				return e
			}
			if row.VerifiedBy == nil && (row.ParserVersion != parserVersion || row.DictionaryVersion != version) {
				result = out
				return nil
			}
			p, e := decodeCache(row, &raw)
			if e != nil {
				return e
			}
			out["from_cache"] = true
			out["parsed"] = p
			out["verified"] = row.VerifiedBy != nil
			out["verified_by"] = row.VerifiedBy
			out["verified_at"] = row.VerifiedAt
			out["needs_review"] = row.NeedsReview || row.DictionaryVersion != version || row.ParserVersion != parserVersion
			out["dictionary_version"] = row.DictionaryVersion
			out["parser_version"] = row.ParserVersion
			result = out
			return nil
		})
		if e != nil {
			httpx.Fail(c, e)
			return
		}
		httpx.OK(c, result)
	})
}

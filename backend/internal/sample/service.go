package sample

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/pkg/id"
	"github.com/myonku/lens-qs-api/internal/store"
)

// CacheEntry 对应数据库表 sample_parse_cache。
type CacheEntry struct {
	ID         string  `db:"id" json:"id"`
	RawKey     string  `db:"raw_key" json:"raw_key"`
	RawSample  string  `db:"raw_sample" json:"raw_sample"`
	Parsed     *string `db:"parsed" json:"parsed,omitempty"`
	DrawingID  *string `db:"drawing_id" json:"drawing_id,omitempty"`
	Confidence float64 `db:"confidence" json:"confidence"`
	HitCount   int     `db:"hit_count" json:"hit_count"`
	VerifiedBy *string `db:"verified_by" json:"verified_by,omitempty"`
	VerifiedAt *string `db:"verified_at" json:"verified_at,omitempty"`
	CreatedAt  string  `db:"created_at" json:"created_at"`
	UpdatedAt  string  `db:"updated_at" json:"updated_at"`
}

// Service 提供样品信息解析与参考匹配。
type Service struct {
	db     *sqlx.DB
	dict   *dictionary.Store
	driver string
}

// NewService 构造服务。
func NewService(db *sqlx.DB, dict *dictionary.Store, driver string) *Service {
	return &Service{db: db, dict: dict, driver: driver}
}

// ParseResult 是解析接口的返回。
type ParseResult struct {
	Parsed    Parsed              `json:"parsed"`
	Drawing   *dictionary.Drawing `json:"drawing,omitempty"`
	Matched   bool                `json:"matched"`
	Reason    string              `json:"unmatched_reason,omitempty"`
	FromCache bool                `json:"from_cache"`
	Note      string              `json:"note"`
}

// Parse 解析原始串：优先命中缓存，其次按字典+内置规则拆解，最后与参考清单比对。
func (s *Service) Parse(ctx context.Context, raw string) (*ParseResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &ParseResult{
			Parsed: Parsed{Raw: raw, UnmatchedReason: "raw_only"},
			Note:   "样品信息为空；可直接保存，稍后补填（系统不做强制）",
		}, nil
	}
	key := NormalizeKey(raw)

	if entry := s.getCache(ctx, key); entry != nil && entry.Parsed != nil && *entry.Parsed != "" {
		var p Parsed
		if err := json.Unmarshal([]byte(*entry.Parsed), &p); err == nil {
			s.bumpHit(ctx, entry.ID)
			res := &ParseResult{Parsed: p, Matched: p.UnmatchedReason == "", Reason: p.UnmatchedReason, FromCache: true}
			if entry.DrawingID != nil {
				res.Drawing = s.loadDrawing(ctx, *entry.DrawingID)
			}
			res.Note = noteFor(p)
			return res, nil
		}
	}

	extra, err := s.dictValues(ctx)
	if err != nil {
		return nil, err
	}
	parsed := Parse(raw, extra)

	var drawing *dictionary.Drawing
	if parsed.Project != "" || parsed.Model != "" || parsed.Spec != "" || parsed.Size != "" || parsed.Side != "" {
		drawing, err = s.dict.FindDrawingByDims(ctx, parsed.Project, parsed.Model, parsed.Spec, parsed.Size, parsed.Side)
		if err != nil {
			return nil, err
		}
	}
	matched, reason := MatchDrawing(parsed, drawing != nil)
	if matched {
		parsed.Confidence = 1.0
		parsed.UnmatchedReason = ""
	} else {
		parsed.UnmatchedReason = reason
	}

	s.saveCache(ctx, key, raw, parsed, drawing)

	res := &ParseResult{Parsed: parsed, Drawing: drawing, Matched: matched, Reason: parsed.UnmatchedReason}
	res.Note = noteFor(parsed)
	return res, nil
}

// Confirm 人工确认/修正结构化字段。注意：不会改写原始串（sample_raw 是事实来源）。
func (s *Service) Confirm(ctx context.Context, raw string, p Parsed, verifiedBy string) (Parsed, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Parsed{}, httpx.BadRequest("原始串不能为空")
	}
	key := NormalizeKey(raw)
	now := store.FormatTime(time.Now())
	payload, err := json.Marshal(p)
	if err != nil {
		return Parsed{}, fmt.Errorf("序列化解析结果失败: %w", err)
	}
	payloadStr := string(payload)

	var existing CacheEntry
	e := s.db.GetContext(ctx, &existing,
		s.db.Rebind(`SELECT id, raw_key, raw_sample, parsed, drawing_id, confidence, hit_count,
			verified_by, verified_at, created_at, updated_at FROM sample_parse_cache WHERE raw_key = ?`), key)
	switch {
	case e == nil:
		_, err = s.db.NamedExecContext(ctx, `UPDATE sample_parse_cache SET
			parsed = :parsed, confidence = :confidence, verified_by = :verified_by,
			verified_at = :verified_at, updated_at = :updated_at WHERE id = :id`,
			map[string]any{
				"parsed": payloadStr, "confidence": p.Confidence, "verified_by": verifiedBy,
				"verified_at": now, "updated_at": now, "id": existing.ID,
			})
	case errors.Is(e, sql.ErrNoRows):
		entry := &CacheEntry{
			ID: id.New(), RawKey: key, RawSample: raw, Parsed: &payloadStr,
			Confidence: p.Confidence, HitCount: 1, VerifiedBy: &verifiedBy, VerifiedAt: &now,
			CreatedAt: now, UpdatedAt: now,
		}
		_, err = s.db.NamedExecContext(ctx, `INSERT INTO sample_parse_cache
			(id, raw_key, raw_sample, parsed, drawing_id, confidence, hit_count, verified_by, verified_at, created_at, updated_at)
			VALUES (:id, :raw_key, :raw_sample, :parsed, :drawing_id, :confidence, :hit_count, :verified_by, :verified_at, :created_at, :updated_at)`, entry)
	default:
		err = e
	}
	if err != nil {
		return Parsed{}, fmt.Errorf("保存解析确认失败: %w", err)
	}
	return p, nil
}

// PendingQuery 是"待核对清单"的查询条件。
type PendingQuery struct {
	Limit int
}

// ------------------------------- 内部 -------------------------------

func (s *Service) dictValues(ctx context.Context) (map[string][]string, error) {
	items, err := s.dict.ListItems(ctx, "", false)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, 6)
	mapping := map[string]string{
		dictionary.CategorySampleProject: "project",
		dictionary.CategorySampleModel:   "model",
		dictionary.CategorySampleSpec:    "spec",
		dictionary.CategorySampleSize:    "size",
		dictionary.CategorySampleSide:    "side",
		dictionary.CategorySampleClass:   "class",
	}
	for _, it := range items {
		if field, ok := mapping[it.Category]; ok {
			out[field] = append(out[field], it.Code)
			// 别名也参与匹配
			if it.Meta != nil && *it.Meta != "" {
				var meta struct {
					Aliases []string `json:"aliases"`
				}
				if json.Unmarshal([]byte(*it.Meta), &meta) == nil {
					for _, a := range meta.Aliases {
						out[field] = append(out[field], a)
					}
				}
			}
		}
	}
	return out, nil
}

func (s *Service) getCache(ctx context.Context, key string) *CacheEntry {
	if key == "" {
		return nil
	}
	var e CacheEntry
	err := s.db.GetContext(ctx, &e, s.db.Rebind(`SELECT id, raw_key, raw_sample, parsed, drawing_id,
		confidence, hit_count, verified_by, verified_at, created_at, updated_at
		FROM sample_parse_cache WHERE raw_key = ?`), key)
	if err != nil {
		return nil
	}
	return &e
}

func (s *Service) bumpHit(ctx context.Context, entryID string) {
	_, _ = s.db.ExecContext(ctx, s.db.Rebind(`UPDATE sample_parse_cache SET hit_count = hit_count + 1 WHERE id = ?`), entryID)
}

func (s *Service) saveCache(ctx context.Context, key, raw string, p Parsed, d *dictionary.Drawing) {
	if key == "" {
		return
	}
	now := store.FormatTime(time.Now())
	var drawingID *string
	if d != nil {
		drawingID = &d.ID
	}
	if existing := s.getCache(ctx, key); existing != nil {
		payload, err := json.Marshal(p)
		if err != nil {
			return
		}
		payloadStr := string(payload)
		_, _ = s.db.NamedExecContext(ctx, `UPDATE sample_parse_cache SET
			parsed = :parsed, drawing_id = :drawing_id, confidence = :confidence,
			hit_count = hit_count + 1, updated_at = :updated_at WHERE id = :id`,
			map[string]any{"parsed": payloadStr, "drawing_id": drawingID, "confidence": p.Confidence,
				"updated_at": now, "id": existing.ID})
		return
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return
	}
	payloadStr := string(payload)
	entry := &CacheEntry{
		ID: id.New(), RawKey: key, RawSample: raw, Parsed: &payloadStr,
		DrawingID: drawingID, Confidence: p.Confidence, HitCount: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	_, _ = s.db.NamedExecContext(ctx, `INSERT INTO sample_parse_cache
		(id, raw_key, raw_sample, parsed, drawing_id, confidence, hit_count, verified_by, verified_at, created_at, updated_at)
		VALUES (:id, :raw_key, :raw_sample, :parsed, :drawing_id, :confidence, :hit_count, :verified_by, :verified_at, :created_at, :updated_at)`, entry)
}

func (s *Service) loadDrawing(ctx context.Context, drawingID string) *dictionary.Drawing {
	list, err := s.dict.ListDrawings(ctx, dictionary.DrawingFilter{Limit: 1000})
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].ID == drawingID {
			return &list[i]
		}
	}
	return nil
}

func noteFor(p Parsed) string {
	switch {
	case p.UnmatchedReason == "raw_only":
		return "未能从原始串拆解出结构化字段；已保留原始串（不影响保存）"
	case p.UnmatchedReason == "partial":
		return "结构化字段不完整；已保留原始串与已识别字段（不影响保存）"
	case p.UnmatchedReason == "no_drawing":
		return "未在参考部件号清单中找到匹配项；这属于正常情况，原始串仍然有效（不影响保存）"
	default:
		return "解析成功并匹配到参考部件号"
	}
}

// CheckDriver 断言驱动名（避免未使用 driver 字段）。
func (s *Service) CheckDriver() string { return s.driver }

// Package sample 实现"样品信息"的双轨存储支持：原始串解析、解析缓存与参考部件号比对。
//
// 设计原则（需求 §4.2、B-6 / B-8）：
//   - 原始串（sample_raw）是事实来源，解析与人工修正都**不改写**它
//   - 解析是"尽力而为"的辅助能力，**不阻断保存**
//   - 与参考部件号清单不匹配属于正常情况，只打标记
//
// 解析采用**位置无关的 token 集合匹配**：参考清单里字符串顺序是
// 「规格-大小-左右-型号-结构类别」，而业务层级是「项目→型号→规格→大小→左右」，
// 两者顺序并不一致，因此禁止按位置取字段。
package sample

import (
	"regexp"
	"strings"
)

// Parsed 是解析结果。
type Parsed struct {
	Raw        string   `json:"raw"`
	Project    string   `json:"project,omitempty"`
	Model      string   `json:"model,omitempty"`
	Spec       string   `json:"spec,omitempty"`
	Size       string   `json:"size,omitempty"`
	Side       string   `json:"side,omitempty"`
	Class      string   `json:"class,omitempty"` // MONOC | BINOC | MONOC_AND_BINOC
	IsRx       bool     `json:"is_rx"`
	Tokens     []string `json:"tokens"`
	Unknown    []string `json:"unknown_tokens,omitempty"`
	Confidence float64  `json:"confidence"`
	// UnmatchedReason 为 raw_only / no_drawing / partial 之一，空表示五核心字段齐全
	UnmatchedReason string `json:"unmatched_reason,omitempty"`
}

// 分隔符：连字符、下划线、斜杠、各类空白都视为分隔符；& 保留语义单独处理。
var (
	sepRe     = regexp.MustCompile(`[\-_/\\,，、|｜:：;；()（）\[\]【】]+`)
	spaceRe   = regexp.MustCompile(`[\s\x{00a0}\x{3000}]+`)
	ampRe     = regexp.MustCompile(`(?i)^(?:and|&|和|与)$`)
	rxRe      = regexp.MustCompile(`(?i)^r[xy]$`)
	modelRe   = regexp.MustCompile(`(?i)^vid\s*([12])\s*(r[xy])?$`)
	monocRe   = regexp.MustCompile(`(?i)^(monoc|mono|monocular|单目)$`)
	binocRe   = regexp.MustCompile(`(?i)^(binoc|bino|binocular|双目)$`)
	projectRe = regexp.MustCompile(`(?i)^m\s*([12])$`)
	specRe    = regexp.MustCompile(`(?i)^s\s*([12])$`)
	sizeRe    = regexp.MustCompile(`(?i)^(md|sm|lg)$`)
	sideRe    = regexp.MustCompile(`(?i)^([lr])$`)
)

// 已知取值的规范写法（参考 docs/requirements.md §4.2.3 的清单）。
// 字典中存在同名 code 时以字典为准，这里只是内置兜底，保证早期可用。
var (
	specCanon    = map[string]string{"1": "S1", "2": "S2"}
	sizeCanon    = map[string]string{"md": "MD", "sm": "SM", "lg": "LG"}
	sideCanon    = map[string]string{"l": "L", "r": "R"}
	modelCanon   = map[string]string{"1": "VID1", "2": "VID2"}
	projectCanon = map[string]string{"1": "M1", "2": "M2"}
)

// NormalizeKey 生成用于缓存查找的规范化键（大小写、全角、空白归一）。
func NormalizeKey(raw string) string {
	s := toHalfWidth(raw)
	s = strings.ToLower(strings.TrimSpace(s))
	s = sepRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 标记 Rx 与 单双目，避免 "VID1 Rx" 因空格差异产生不同键
	s = strings.ReplaceAll(s, " rx", " rx")
	return s
}

// Tokenize 把原始串切分为 token（保留 & 的语义组合）。
func Tokenize(raw string) []string {
	s := toHalfWidth(raw)
	s = sepRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(s, " ")
	tokens := make([]string, 0, 8)
	for _, t := range strings.Split(s, " ") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		tokens = append(tokens, t)
	}
	return tokens
}

// Parse 解析原始串。secondPass 为字典补充的"code → 字段"映射（可为 nil）。
//
// extra 形如 map[字段名][]取值，字段名取 "project"/"model"/"spec"/"size"/"side"/"class"。
func Parse(raw string, extra map[string][]string) Parsed {
	out := Parsed{Raw: raw, Tokens: Tokenize(raw)}
	if len(out.Tokens) == 0 {
		out.UnmatchedReason = "raw_only"
		return out
	}

	lookup := buildLookup(extra)
	unknown := make([]string, 0, 4)
	classMonoc, classBinoc := false, false

	for _, tok := range out.Tokens {
		key := strings.ToLower(strings.TrimSpace(tok))
		if key == "" {
			continue
		}
		// 连接符（&/and/和/与）在结构类别判定中有意义，先处理
		if ampRe.MatchString(tok) {
			continue
		}

		// 字典优先
		if hit, ok := lookup[key]; ok {
			if hit.field == "class" {
				// 结构类别可能是字典里的组合值（如 MONOC_AND_BINOC），需要按分量累计
				applyClassToken(&classMonoc, &classBinoc, hit.canon)
			} else {
				assign(&out, hit.field, hit.canon)
			}
			continue
		}

		switch {
		case monocRe.MatchString(tok):
			classMonoc = true
		case binocRe.MatchString(tok):
			classBinoc = true
		case rxRe.MatchString(tok):
			// Rx 可能是独立 token（"S1 - MD - L - VID1 Rx"）：
			// 此时把已识别的型号升级为处方镜型号。
			out.IsRx = true
			if out.Model != "" && !strings.HasSuffix(strings.ToUpper(out.Model), "RX") {
				out.Model = strings.TrimSpace(out.Model) + " Rx"
			}
		case modelRe.MatchString(tok):
			m := modelRe.FindStringSubmatch(tok)
			model := modelCanon[m[1]]
			if m[2] != "" {
				model += " Rx"
				out.IsRx = true
			}
			if out.Model == "" {
				out.Model = model
			}
		case projectRe.MatchString(tok):
			m := projectRe.FindStringSubmatch(tok)
			if out.Project == "" {
				out.Project = projectCanon[m[1]]
			}
		case specRe.MatchString(tok):
			m := specRe.FindStringSubmatch(tok)
			if out.Spec == "" {
				out.Spec = specCanon[m[1]]
			}
		case sizeRe.MatchString(tok):
			if out.Size == "" {
				out.Size = sizeCanon[strings.ToLower(tok)]
			}
		case sideRe.MatchString(tok):
			// 仅当已有其他维度信息时才把单个字母当作"左右"，
			// 避免把型号里的字母误判（型号已在前面匹配）。
			if out.Side == "" {
				out.Side = sideCanon[strings.ToLower(tok)]
			}
		default:
			unknown = append(unknown, tok)
		}
	}

	switch {
	case classMonoc && classBinoc:
		out.Class = "MONOC_AND_BINOC"
	case classMonoc:
		out.Class = "MONOC"
	case classBinoc:
		out.Class = "BINOC"
	}
	out.Unknown = unknown
	if out.Class != "" {
		out.Unknown = removeToken(out.Unknown, "&")
	}

	coreCount := 0
	for _, v := range []string{out.Project, out.Model, out.Spec, out.Size, out.Side} {
		if v != "" {
			coreCount++
		}
	}
	switch {
	case coreCount == 5:
		out.Confidence = 0.9
	case coreCount > 0:
		out.Confidence = 0.5
		out.UnmatchedReason = "partial"
	default:
		out.Confidence = 0
		out.UnmatchedReason = "raw_only"
	}
	return out
}

// MatchDrawing 判定解析结果与参考部件号清单是否匹配。
//
// 返回的 matched=false 时 reason 说明原因：no_drawing（清单无匹配）/ partial（核心字段不全）。
func MatchDrawing(p Parsed, found bool) (matched bool, reason string) {
	if p.Project == "" || p.Model == "" || p.Spec == "" || p.Size == "" || p.Side == "" {
		return false, "partial"
	}
	if !found {
		return false, "no_drawing"
	}
	return true, ""
}

func assign(out *Parsed, field, val string) {
	switch field {
	case "project":
		if out.Project == "" {
			out.Project = val
		}
	case "model":
		if out.Model == "" {
			out.Model = val
		}
	case "spec":
		if out.Spec == "" {
			out.Spec = val
		}
	case "size":
		if out.Size == "" {
			out.Size = val
		}
	case "side":
		if out.Side == "" {
			out.Side = val
		}
	case "class":
		out.Class = val
	case "rx":
		out.IsRx = true
	}
}

func buildLookup(extra map[string][]string) map[string]struct{ field, canon string } {
	out := make(map[string]struct{ field, canon string }, 32)
	for field, values := range extra {
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			key := strings.ToLower(toHalfWidth(v))
			out[key] = struct{ field, canon string }{field: field, canon: v}
		}
	}
	return out
}

// applyClassToken 把结构类别取值累加到"单目 / 双目"标记上。
// 因为该维度可能是组合值（MONOC_AND_BINOC）或分开写（MONOC & BINOC），
// 所以按分量累计，而不是整体覆盖。
func applyClassToken(monoc, binoc *bool, val string) {
	u := strings.ToUpper(val)
	switch {
	case strings.Contains(u, "AND") || strings.Contains(u, "&") || strings.Contains(u, "_AND_"):
		*monoc = true
		*binoc = true
	case strings.Contains(u, "MONOC") || strings.Contains(u, "MONO"):
		*monoc = true
	case strings.Contains(u, "BINOC") || strings.Contains(u, "BINO"):
		*binoc = true
	}
}

func removeToken(list []string, target string) []string {
	out := list[:0]
	for _, v := range list {
		if !strings.EqualFold(strings.TrimSpace(v), target) {
			out = append(out, v)
		}
	}
	return out
}

// toHalfWidth 把全角字符转换为半角（含全角字母数字与全角空格）。
func toHalfWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0x3000:
			b.WriteRune(' ')
		case r >= 0xFF01 && r <= 0xFF5E:
			b.WriteRune(r - 0xFEE0)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

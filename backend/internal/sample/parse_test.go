package sample

import (
	"reflect"
	"testing"
)

// 用例来源：docs/requirements.md §4.2.2 / §4.2.3 与表单照片中的手写串。
func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    Parsed
		unknown []string
	}{
		{
			name: "参考清单写法（规格-大小-左右-型号-结构类别）",
			raw:  "S1 - MD - R - VID2 - MONOC & BINOC",
			want: Parsed{Project: "", Model: "VID2", Spec: "S1", Size: "MD", Side: "R", Class: "MONOC_AND_BINOC"},
		},
		{
			name: "表单照片中的手写串",
			raw:  "M2 S2 ND2 SMR",
			want: Parsed{Project: "M2", Model: "", Spec: "S2", Size: "", Side: ""},
			// ND2 与 SMR 无法识别：收集进 unknown，不影响保存
			unknown: []string{"ND2", "SMR"},
		},
		{
			name: "处方镜型号（Rx 与 VID1 合并为一个型号）",
			raw:  "S1 - MD - L - VID1 Rx",
			want: Parsed{Model: "VID1 Rx", Spec: "S1", Size: "MD", Side: "L", IsRx: true},
		},
		{
			name: "小写与无分隔符",
			raw:  "s2 sm l vid1 rx",
			want: Parsed{Model: "VID1 Rx", Spec: "S2", Size: "SM", Side: "L", IsRx: true},
		},
		{
			name: "全角字符与中文空格",
			raw:  "Ｓ１　ＭＤ　Ｒ　ＶＩＤ２",
			want: Parsed{Model: "VID2", Spec: "S1", Size: "MD", Side: "R"},
		},
		{
			name: "仅单目",
			raw:  "S2 - SM - R - VID2 - MONOC",
			want: Parsed{Model: "VID2", Spec: "S2", Size: "SM", Side: "R", Class: "MONOC"},
		},
		{
			name: "只写了项目",
			raw:  "M1",
			want: Parsed{Project: "M1"},
		},
		{
			name: "空串",
			raw:  "",
			want: Parsed{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.raw, nil)
			if got.Project != tc.want.Project {
				t.Errorf("Project = %q, want %q", got.Project, tc.want.Project)
			}
			if got.Model != tc.want.Model {
				t.Errorf("Model = %q, want %q", got.Model, tc.want.Model)
			}
			if got.Spec != tc.want.Spec {
				t.Errorf("Spec = %q, want %q", got.Spec, tc.want.Spec)
			}
			if got.Size != tc.want.Size {
				t.Errorf("Size = %q, want %q", got.Size, tc.want.Size)
			}
			if got.Side != tc.want.Side {
				t.Errorf("Side = %q, want %q", got.Side, tc.want.Side)
			}
			if got.Class != tc.want.Class {
				t.Errorf("Class = %q, want %q", got.Class, tc.want.Class)
			}
			if tc.want.Class != "" && len(got.Unknown) != 0 {
				t.Errorf("结构类别识别后不应残留未知 token: %v", got.Unknown)
			}
			if got.IsRx != tc.want.IsRx {
				t.Errorf("IsRx = %v, want %v", got.IsRx, tc.want.IsRx)
			}
			if tc.unknown != nil && !reflect.DeepEqual(got.Unknown, tc.unknown) {
				t.Errorf("Unknown = %v, want %v", got.Unknown, tc.unknown)
			}
		})
	}
}

// 重要：解析绝不能改写原始串。
func TestParseKeepsRaw(t *testing.T) {
	raw := "M2  S2   ND2 SMR"
	got := Parse(raw, nil)
	if got.Raw != raw {
		t.Fatalf("Raw 应逐字保留，得到 %q", got.Raw)
	}
}

func TestNormalizeKeyStable(t *testing.T) {
	a := NormalizeKey("S1 - MD - R - VID2 - MONOC & BINOC")
	b := NormalizeKey("s1_md_r_vid2_monoc & binoc")
	if a != b {
		t.Fatalf("规范化键应一致：%q vs %q", a, b)
	}
}

func TestMatchDrawing(t *testing.T) {
	full := Parsed{Project: "M2", Model: "VID2", Spec: "S2", Size: "SM", Side: "R"}
	if matched, _ := MatchDrawing(full, true); !matched {
		t.Error("五核心字段齐全且命中清单时应判定为匹配")
	}
	if matched, reason := MatchDrawing(full, false); matched || reason != "no_drawing" {
		t.Errorf("清单无匹配时应返回 no_drawing，得到 matched=%v reason=%q", matched, reason)
	}
	partial := Parsed{Project: "M2", Spec: "S2"}
	if matched, reason := MatchDrawing(partial, true); matched || reason != "partial" {
		t.Errorf("核心字段不全时应返回 partial，得到 matched=%v reason=%q", matched, reason)
	}
}

// 字典补充的取值也应能被识别。
func TestParseWithDictionaryValues(t *testing.T) {
	extra := map[string][]string{
		"project": {"M1", "M2"},
		"model":   {"VID2"},
		"size":    {"MD", "SM", "LG"},
	}
	got := Parse("M1 LG VID2", extra)
	if got.Project != "M1" || got.Size != "LG" || got.Model != "VID2" {
		t.Fatalf("字典取值未被识别: %+v", got)
	}
}

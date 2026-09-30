package inspection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Patch 表示一次"字段级合并"的输入：只有出现在 JSON 中的键才会被写入目标结构体。
//
// 语义来自 docs/requirements.md v0.4 §5.3：
//   - 请求里出现的字段 → 更新（含显式 null → 清空）
//   - 请求里没有的字段 → 保持原值
type Patch map[string]json.RawMessage

// ParsePatch 解析请求中的 form / record / line 对象为字段补丁。
// 传 nil 或空对象时返回空补丁（表示"本次不改这一层"）。
func ParsePatch(raw json.RawMessage) (Patch, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Patch{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("字段补丁格式不正确: %w", err)
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return Patch(m), nil
}

// Empty 报告补丁是否没有任何字段。
func (p Patch) Empty() bool { return len(p) == 0 }

// Merge 把补丁按字段名合并进 dst（必须是结构体指针）。
//
//	readonly 中的字段永不被合并（如 id / form_id / sequence_no / created_at）。
//
// 未知字段（数据库中不存在的列）会被忽略并由 Unknown 返回，用于提示调用方拼写错误。
func (p Patch) Merge(dst any, readonly ...string) (unknown []string, err error) {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("Merge 需要结构体指针，实际为 %T", dst)
	}
	sv := v.Elem()
	st := sv.Type()

	skip := make(map[string]bool, len(readonly)+1)
	skip["id"] = true
	for _, f := range readonly {
		skip[f] = true
	}

	// 建立列名 → 字段索引 的映射
	byColumn := make(map[string]int, st.NumField())
	for i := 0; i < st.NumField(); i++ {
		col := columnOf(st.Field(i))
		if col == "" || col == "-" {
			continue
		}
		byColumn[col] = i
	}

	for key, raw := range p {
		idx, ok := byColumn[key]
		if !ok {
			unknown = append(unknown, key)
			continue
		}
		if skip[key] {
			continue
		}
		field := sv.Field(idx)
		if !field.CanSet() {
			continue
		}
		if err := setField(field, raw); err != nil {
			return unknown, fmt.Errorf("字段 %s 赋值失败: %w", key, err)
		}
	}
	return unknown, nil
}

func columnOf(sf reflect.StructField) string {
	tag := sf.Tag.Get("db")
	if tag == "" {
		return ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

func setField(field reflect.Value, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	isNull := bytes.Equal(trimmed, []byte("null"))

	// 可空字段：null 表示清空，其余按基础类型解析
	if field.Kind() == reflect.Ptr {
		if isNull {
			field.Set(reflect.Zero(field.Type()))
			return nil
		}
		nv := reflect.New(field.Type().Elem())
		if err := json.Unmarshal(trimmed, nv.Interface()); err != nil {
			return err
		}
		field.Set(nv)
		return nil
	}

	if isNull {
		// 非指针字段收到 null：保持原值（不把 0/"" 写进去，避免误清空）
		return nil
	}
	return json.Unmarshal(trimmed, field.Addr().Interface())
}

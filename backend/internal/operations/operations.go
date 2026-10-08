// Package operations 定义「工序表单」注册表：每个工序对应哪张表单、能填哪些列。
//
// 现实依据：**每道工序都要填一份表单，但各工序所填内容不同**，且具体差异业务方尚不确定。
// 因此注册表的作用是"把当前已知的默认值固化下来"，同时：
//   - 所有工序默认开放**全部列**（最大灵活性 F-1/F-8，前端可按工序只显示所需项）；
//   - `allowed_fields` 支持将来按工序收窄（不确定时留空表示不限制）；
//   - 新增工序只需加一条记录（或走字典维护接口），不需要改代码逻辑。
package operations

// Template 是一个工序表单模板。
type Template struct {
	Code  string `json:"code"`  // 工序编码，如 "一次加硬"
	Label string `json:"label"` // 显示名
	// AllowedFields 为空表示"不限制"（开放全部列）；
	// 非空时只允许提交其中的字段（仍不校验必填）。
	AllowedFields []string `json:"allowed_fields,omitempty"`
	SortNo        int      `json:"sort_no"`
	Note          string   `json:"note,omitempty"`
}

// Default 是未指定工序时使用的模板编码（"找到自己工序所在表单"这一诉求的兜底）。
const Default = "GENERAL"

// Defaults 是内置的工序表单模板。
//
// 说明：目前只固化了纸质表单上出现过的工序 + 一个通用模板；
// 各工序"具体填哪些列"业务方未定，因此一律开放全部列，不设必填。
var Defaults = []Template{
	{Code: Default, Label: "一般检验", SortNo: 0, Note: "通用模板：开放全部列，用于工序未确定或临时录入"},
	{Code: "一次加硬", Label: "一次加硬", SortNo: 10},
	{Code: "二次加硬", Label: "二次加硬", SortNo: 20},
}

// Find 按编码查找模板；找不到时返回通用模板（不报错——工序可自由新增，F-4）。
func Find(code string) Template {
	for _, t := range Defaults {
		if t.Code == code {
			return t
		}
	}
	return Template{
		Code:   code,
		Label:  code,
		SortNo: 100,
		Note:   "未登记的工序：按通用模板处理（开放全部列）",
	}
}

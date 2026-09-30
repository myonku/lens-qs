package store

import (
	"time"
)

// nowText 返回统一的时间文本（UTC，RFC3339，秒精度）。
// 全库时间都以此格式写入，保证 SQLite 与 PostgreSQL 行为一致、且可直接按字符串比较排序。
func nowText() string { return FormatTime(time.Now()) }

// FormatTime 把时间格式化为库内统一文本。
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ParseTime 解析库内时间文本；空串或非法值返回零值。
func ParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

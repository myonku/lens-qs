package password

import (
	"unicode/utf8"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

func Validate(s string) error {
	n := utf8.RuneCountInString(s)
	if n < 8 || n > 128 {
		return httpx.Validation("密码长度必须为8–128字符", nil)
	}
	return nil
}

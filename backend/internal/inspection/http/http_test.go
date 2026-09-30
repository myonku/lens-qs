package inspectionhttp

import (
	"errors"
	"testing"

	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
)

// 唯一约束冲突必须映射成 409（V-7），而不是 500——前端要能区分"重号"与"服务异常"。
func TestMapConstraintError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "SQLite form_no 唯一冲突",
			err:    errors.New("写入表单失败: constraint failed: UNIQUE constraint failed: forms.form_no (2067)"),
			status: 409,
			code:   httpx.CodeConflict,
		},
		{
			name:   "PostgreSQL 主键重号",
			err:    errors.New(`写入工序记录失败: ERROR: duplicate key value violates unique constraint "ux_form_records_form_seq" (SQLSTATE 23505)`),
			status: 409,
			code:   httpx.CodeConflict,
		},
		{
			name:   "明细行序号重复",
			err:    errors.New("写入明细行失败: UNIQUE constraint failed: record_lines.record_id, record_lines.seq"),
			status: 409,
			code:   httpx.CodeConflict,
		},
		{
			name:   "非约束类错误保持原样（走 500）",
			err:    errors.New("数据库连接不可用"),
			status: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapConstraintError(tc.err, "M-260908-01")
			if tc.status == 0 {
				if got != tc.err {
					t.Fatalf("非约束错误应原样返回，得到 %v", got)
				}
				return
			}
			var he *httpx.Error
			if !errors.As(got, &he) {
				t.Fatalf("应返回业务错误，得到 %v", got)
			}
			if he.Status != tc.status || he.Code != tc.code {
				t.Fatalf("期望 %d/%s，得到 %d/%s（%s）", tc.status, tc.code, he.Status, he.Code, he.Message)
			}
		})
	}
}

// 已经是业务错误（如 404/422/409）时必须原样透传，不能被二次包装。
func TestMapConstraintErrorPassesThroughBusinessError(t *testing.T) {
	original := httpx.NotFound("工序记录不存在")
	got := mapConstraintError(original, "M-1")
	var he *httpx.Error
	if !errors.As(got, &he) || he.Status != 404 {
		t.Fatalf("业务错误应原样透传，得到 %v", got)
	}
	if he.Message != original.Message {
		t.Fatalf("错误消息被改写: %q", he.Message)
	}
}

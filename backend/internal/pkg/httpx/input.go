package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// DecodeStrict rejects duplicate keys at every depth before decoding typed DTOs.
func DecodeStrict(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func() error
	visit = func() error {
		t, e := d.Token()
		if e != nil {
			return e
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				s := key.(string)
				if seen[s] {
					return fmt.Errorf("duplicate field: %s", s)
				}
				seen[s] = true
				if e = visit(); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = visit(); e != nil {
					return e
				}
			}
		default:
			return fmt.Errorf("invalid JSON")
		}
		_, e = d.Token()
		return e
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return BadRequest("请求必须是 JSON 对象")
	}
	if e := visit(); e != nil {
		return BadRequest(e.Error())
	}
	if _, e := d.Token(); e != io.EOF {
		return BadRequest("请求只能包含一个 JSON 对象")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(dst); e != nil {
		return BadRequest("请求类型不正确: " + e.Error())
	}
	return nil
}
func Query(c *gin.Context, names ...string) error {
	allowed := map[string]bool{}
	for _, s := range names {
		allowed[s] = true
	}
	if c.Request.Method == http.MethodGet && c.Request.ContentLength != 0 {
		return BadRequest("GET 不接受正文")
	}
	for k, v := range c.Request.URL.Query() {
		if !allowed[k] || len(v) != 1 {
			return BadRequest("未知或重复参数", map[string]string{"parameter": k})
		}
	}
	return nil
}
func Pagination(c *gin.Context) (int, int, error) {
	p, s := 1, 20
	var e error
	if v, ok := c.GetQuery("page"); ok {
		p, e = strconv.Atoi(v)
		if e != nil || p < 1 || p > 100000000 {
			return 0, 0, BadRequest("非法 page")
		}
	}
	if v, ok := c.GetQuery("page_size"); ok {
		s, e = strconv.Atoi(v)
		if e != nil || s < 1 || s > 100 {
			return 0, 0, BadRequest("非法 page_size")
		}
	}
	return p, s, nil
}

// Optional distinguishes omitted, null, and concrete values in maintenance PATCH.
type Optional[T any] struct {
	Set   bool
	Value *T
}

func (o *Optional[T]) UnmarshalJSON(raw []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v T
	if e := json.Unmarshal(raw, &v); e != nil {
		return e
	}
	o.Value = &v
	return nil
}

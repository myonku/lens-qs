package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Recovery 捕获 panic，避免单个请求打挂进程。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		_ = c.Error(gin.Error{Err: asError(recovered), Type: gin.ErrorTypePrivate})
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"code":    CodeInternal,
			"message": "服务器内部错误",
		})
	})
}

type errString string

func (e errString) Error() string { return string(e) }

func asError(v any) error {
	switch t := v.(type) {
	case error:
		return t
	case string:
		return errString(t)
	default:
		return errString("panic")
	}
}

// ErrorHandler 把所有由 handler 通过 c.Error(err) 收集的错误统一写成响应。
// 只有显式返回错误（httpx.Fail）时才会写出响应，本中间件用于兜底。
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		Fail(c, c.Errors.Last().Err)
	}
}

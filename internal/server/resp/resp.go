package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ResponseStruct struct {
	Code    int         `json:"code" example:"200"`
	Message string      `json:"message" example:"success"`
	Data    interface{} `json:"data,omitempty"`
	// ErrorCode 机器可读的错误分类, 省略表示按 HTTP 状态码理解即可。
	ErrorCode string `json:"error_code,omitempty"`
}

func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, ResponseStruct{
		Code:    http.StatusOK,
		Message: "success",
		Data:    data,
	})
}

func Error(c *gin.Context, code int, err string) {
	c.AbortWithStatusJSON(code, ResponseStruct{
		Code:    code,
		Message: err,
	})
}

// ErrorWithCode 带机器可读错误码的失败响应; 供前端按错误码分流(目前只有 step-up 一种)。
func ErrorWithCode(c *gin.Context, code int, err, errorCode string) {
	c.AbortWithStatusJSON(code, ResponseStruct{
		Code:      code,
		Message:   err,
		ErrorCode: errorCode,
	})
}

// StepUpRequired 高危操作缺二次验证: 428 Precondition Required + step_up_required。
// 请求在进入业务处理前就被拦下, 因此前端"确认密码后原样重发一次"不会重复执行已发生的操作。
func StepUpRequired(c *gin.Context) {
	ErrorWithCode(c, http.StatusPreconditionRequired, ErrStepUpRequired, CodeStepUpRequired)
}

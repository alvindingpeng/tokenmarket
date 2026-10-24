package resp

const (
	ErrBadRequest        = "Invalid request parameters"
	ErrInvalidJSON       = "Invalid JSON format"
	ErrInvalidParam      = "Invalid parameter"
	ErrValidation        = "Input validation failed"
	ErrDuplicateResource = "Resource already exists"
	ErrResourceNotFound  = "Resource not found"
	ErrInternalServer    = "An unexpected error occurred"
	ErrDatabase          = "Database operation failed"
	ErrUnauthorized      = "Authentication failed"

	// ErrStepUpRequired 高危操作缺二次验证(step-up)时的说明文案;
	// 机器可读的判定依据是 ResponseStruct.ErrorCode == CodeStepUpRequired, 不要靠文案匹配。
	ErrStepUpRequired = "Step-up verification required"
	// CodeStepUpRequired 明确的 step-up 错误码: 前端只有见到它才弹密码确认并重发原请求,
	// 其它错误(超时/5xx/校验失败)一律照常报错, 不触发二次验证也不自动重试。
	CodeStepUpRequired = "step_up_required"
)

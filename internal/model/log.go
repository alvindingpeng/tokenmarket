package model

type RelayLog struct {
	ID               int     `json:"id" gorm:"primaryKey"`
	Time             int64   `json:"time"`               // 时间戳
	RequestModelName string  `json:"request_model_name"` // 请求模型名称
	ChannelId        int     `json:"channel"`            // 实际使用的渠道ID
	ActualModelName  string  `json:"actual_model_name"`  // 实际使用模型名称
	InputTokens      int     `json:"input_tokens"`       // 输入Token
	OutputTokens     int     `json:"output_tokens"`      // 输出 Token
	Ftut             int     `json:"ftut"`               // 首字时间(毫秒)
	UseTime          int     `json:"use_time"`           // 总用时(毫秒)
	Cost             float64 `json:"cost"`               // 消耗费用
	RequestContent   string  `json:"request_content"`    // 请求内容
	ResponseContent  string  `json:"response_content"`   // 响应内容
}

package xfyun

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 响应帧 header.status / payload.choices.status 取值(讯飞官方文档):
// 0 = 数据尚未传输完, 后面还有帧; 2 = 最后一帧, 数据传输完成。
// 其余取值一律按未完处理, 前向兼容新版本协议。
const (
	StatusStreaming = 0
	StatusComplete  = 2
)

// 会话角色; 星火帧只接受 user/assistant/system 三种。
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// Frame 是星火 WebSocket 协议的请求/响应共用帧:
// 请求帧带 header(app_id/uid) + parameter.chat + payload.message.text;
// 响应帧带 header(code/message/sid/status) + payload.choices.text + payload.usage。
type Frame struct {
	Header    FrameHeader    `json:"header"`
	Parameter *ChatParameter `json:"parameter,omitempty"`
	Payload   *FramePayload  `json:"payload,omitempty"`
}

// FrameHeader 是帧头: 请求用 app_id/uid, 响应用 code/message/sid/status。
type FrameHeader struct {
	AppID   string `json:"app_id,omitempty"`
	UID     string `json:"uid,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	SID     string `json:"sid,omitempty"`
	Status  int    `json:"status,omitempty"`
}

// ChatParameter 是请求参数块, 线格式为 {"chat": {...}}。
type ChatParameter struct {
	Chat ChatConfig `json:"chat"`
}

// ChatConfig 是单轮对话的采样参数。
type ChatConfig struct {
	Domain      string  `json:"domain"`
	Temperature float64 `json:"temperature,omitempty"`
	MaxTokens   int64   `json:"max_tokens,omitempty"`
	TopK        int64   `json:"top_k,omitempty"`
	ChatID      string  `json:"chat_id,omitempty"`
}

// FramePayload 是帧载荷: 请求帧只有 Message, 响应帧只有 Choices/Usage。
type FramePayload struct {
	Message *ChatMessages `json:"message,omitempty"`
	Choices *ChatChoices  `json:"choices,omitempty"`
	Usage   *ChatUsage    `json:"usage,omitempty"`
}

// ChatMessages 是请求帧的多轮上下文。
type ChatMessages struct {
	Text []ChatMessage `json:"text"`
}

// ChatMessage 是上下文中的一条消息。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatChoices 是响应帧的增量输出。
type ChatChoices struct {
	Status int          `json:"status"`
	Seq    int          `json:"seq"`
	Text   []ChatChoice `json:"text,omitempty"`
}

// ChatChoice 是响应帧中的一段增量文本。
type ChatChoice struct {
	Content string `json:"content"`
	Role    string `json:"role,omitempty"`
	Index   int    `json:"index,omitempty"`
}

// ChatUsage 是响应帧报告的用量; 星火逐帧累计下发, 以最后一帧为准。
type ChatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// Error 是响应帧携带的协议级错误(header.code != 0)。
// 文本只含错误码/错误信息/sid, 不含任何凭据。
type Error struct {
	Code    int
	Message string
	SID     string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "unknown error"
	}
	if e.SID != "" {
		return fmt.Sprintf("xunfei spark error %d: %s (sid %s)", e.Code, msg, e.SID)
	}
	return fmt.Sprintf("xunfei spark error %d: %s", e.Code, msg)
}

// Err 把响应帧的协议错误转成 error; 正常帧返回 nil。
func (f *Frame) Err() error {
	if f == nil {
		return nil
	}
	if f.Header.Code == 0 {
		return nil
	}
	return &Error{Code: f.Header.Code, Message: f.Header.Message, SID: f.Header.SID}
}

// Done 判断该响应帧是否终结整个流: header.status 为 2, 或 choices.status 为 2。
func (f *Frame) Done() bool {
	if f == nil {
		return false
	}
	if f.Header.Status == StatusComplete {
		return true
	}
	return f.Payload != nil && f.Payload.Choices != nil && f.Payload.Choices.Status == StatusComplete
}

// Content 拼出该响应帧携带的全部增量文本。
func (f *Frame) Content() string {
	if f == nil || f.Payload == nil || f.Payload.Choices == nil {
		return ""
	}
	var b strings.Builder
	for _, text := range f.Payload.Choices.Text {
		b.WriteString(text.Content)
	}
	return b.String()
}

// Usage 返回响应帧报告的用量(可能为 nil)。
func (f *Frame) Usage() *ChatUsage {
	if f == nil || f.Payload == nil {
		return nil
	}
	return f.Payload.Usage
}

// SID 返回响应帧的会话 ID(可能为空)。
func (f *Frame) SID() string {
	if f == nil {
		return ""
	}
	return f.Header.SID
}

// Encode 序列化帧为 WebSocket 文本帧正文。
func (f *Frame) Encode() ([]byte, error) {
	return json.Marshal(f)
}

// DecodeFrame 解析 WebSocket 文本帧正文; 解析失败的错误文本只含正文片段, 不回显完整帧,
// 但帧本身不携带凭据(app_id 除外), 该约束主要为防将来扩字段时误带。
func DecodeFrame(data []byte) (*Frame, error) {
	var frame Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		return nil, fmt.Errorf("decode xunfei spark frame: %w", err)
	}
	return &frame, nil
}

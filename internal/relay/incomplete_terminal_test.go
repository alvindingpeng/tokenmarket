package relay

import (
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// TestIncompleteIsNormalTerminalState 钉住 response.incomplete 是正常终态。
//
// 缺陷(线上实测): 流式请求被 max_tokens 截断时, 上游按规范下发
// response.incomplete(reason=max_output_tokens)。旧实现无条件返回 ResponseError,
// 于是"输出到上限"被记成失败、计入成员失败率, 反复几次把 breaker 打开,
// 整个账号在一个窗口内被踢出路由 —— 用户侧是"第一句话能通, 之后全是 429"。
//
// 规范里 incomplete_details.reason 只有 max_output_tokens 与 content_filter 两种,
// 都是服务端按客户端要求正常收尾, 不该判为失败。
func TestIncompleteIsNormalTerminalState(t *testing.T) {
	for _, reason := range []string{"max_output_tokens", "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			// 事件名与正文 type 一致; 正文用 response 对象携带 incomplete_details。
			data := []byte(`{"type":"response.incomplete","response":{"id":"r1","status":"incomplete","incomplete_details":{"reason":"` + reason + `"}}}`)
			event := &httpclient.StreamEvent{Type: "response.incomplete", Data: data}
			last, err := inspectStreamEvent(llm.APIFormatOpenAIResponse, event)
			if !last {
				t.Fatal("response.incomplete 必须结束整个响应流")
			}
			if err != nil {
				t.Fatalf("incomplete(%s) 是正常终态, 不能判为失败: %v", reason, err)
			}
		})
	}
}

// TestResponseFailedIsStillError 是边界: 真的失败终态必须继续判错。
//
// 放宽 incomplete 不能顺手把 response.failed 也放过去 —— 那会把上游真实故障
// 当成正常结束, 反而掩盖问题。
func TestResponseFailedIsStillError(t *testing.T) {
	data := []byte(`{"type":"response.failed","response":{"id":"r1","status":"failed","error":{"code":"server_error","message":"boom"}}}`)
	event := &httpclient.StreamEvent{Type: "response.failed", Data: data}
	last, err := inspectStreamEvent(llm.APIFormatOpenAIResponse, event)
	if !last {
		t.Fatal("response.failed 必须结束整个响应流")
	}
	if err == nil {
		t.Fatal("response.failed 必须判为失败: 否则上游故障会被当成正常结束")
	}
}

// TestNonStreamingLengthIsNotFailure 钉住非流式的 finish_reason=length 同样是正常终态。
//
// 与流式 response.incomplete 是同一个缺陷的另一条路径: Responses 协议把
// finish_reason=length 映射成 response_incomplete, 旧实现同样判为失败。
func TestNonStreamingLengthIsNotFailure(t *testing.T) {
	reason := "length"
	response := &llm.Response{
		Choices: []llm.Choice{{FinishReason: &reason}},
	}
	if err := validateResponse(llm.APIFormatOpenAIResponse, response); err != nil {
		t.Fatalf("finish_reason=length 到上限是正常收尾, 不能判为失败: %v", err)
	}
}

// TestNonStreamingErrorStillFails 是边界: error 终态必须继续判错。
func TestNonStreamingErrorStillFails(t *testing.T) {
	reason := "error"
	response := &llm.Response{
		Choices: []llm.Choice{{FinishReason: &reason}},
	}
	if err := validateResponse(llm.APIFormatOpenAIResponse, response); err == nil {
		t.Fatal("finish_reason=error 必须判为失败")
	}
}

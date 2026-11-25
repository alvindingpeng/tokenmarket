package relay

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestClassifyAutoCodingToolAndUnsupportedShapes(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		protocol model.Protocol
		want     string
	}{
		{
			name:     "fenced code is coding",
			body:     "{\"messages\":[{\"role\":\"user\",\"content\":\"Please review this code:\\n```go\\nfunc main() {}\\n```\"}]}",
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskCoding,
		},
		{
			name:     "coding keyword is coding",
			body:     `{"messages":[{"role":"user","content":"Fix the bug in this GitHub pull request"}]}`,
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskCoding,
		},
		{
			name:     "tool schema is a tool task",
			body:     `{"messages":[{"role":"user","content":"Find a train"}],"tools":[{"type":"function","function":{"name":"search"}}]}`,
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskTool,
		},
		{
			name:     "multi-turn tool result remains a tool task",
			body:     `{"messages":[{"role":"user","content":"Find a train"},{"role":"assistant","content":null,"tool_calls":[{"id":"call-1"}]},{"role":"tool","content":"No service"}]}`,
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskTool,
		},
		{
			name:     "text array remains supported",
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"请总结这篇文章"}]}]}`,
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskSummary,
		},
		{
			name:     "image content is unsupported",
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`,
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskUnsupported,
		},
		{
			name:     "unknown role is unsupported",
			body:     `{"messages":[{"role":"critic","content":"hello"}]}`,
			protocol: model.ProtocolOpenAIChatCompletion,
			want:     AutoTaskUnsupported,
		},
		{
			name:     "responses coding text is supported",
			body:     `{"input":[{"role":"user","content":[{"type":"input_text","text":"Fix this compile error"}]}]}`,
			protocol: model.ProtocolOpenAIResponse,
			want:     AutoTaskCoding,
		},
		{
			name:     "anthropic native coding text",
			body:     `{"system":[{"type":"text","text":"Be concise"}],"messages":[{"role":"user","content":[{"type":"text","text":"Fix this compile error"}]}]}`,
			protocol: model.ProtocolAnthropicMessage,
			want:     AutoTaskCoding,
		},
		{
			name:     "responses full function history",
			body:     `{"input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			protocol: model.ProtocolOpenAIResponse,
			want:     AutoTaskTool,
		},
		{
			name:     "responses stored continuation requires affinity",
			body:     `{"previous_response_id":"resp_1","input":"continue"}`,
			protocol: model.ProtocolOpenAIResponse,
			want:     AutoTaskUnsupported,
		},
		{
			name:     "responses function output is unsupported",
			body:     `{"input":[{"role":"user","content":[{"type":"input_text","text":"Continue"}]},{"type":"function_call_output","output":"ok"}]}`,
			protocol: model.ProtocolOpenAIResponse,
			want:     AutoTaskUnsupported,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAuto([]byte(tc.body), tc.protocol, 10, 10).name; got != tc.want {
				t.Fatalf("class=%q want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyAutoResponseExtraction(t *testing.T) {
	body := []byte("{\"input\":[{\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Extract fields\"}]}],\"text\":{\"format\":{\"type\":\"json_schema\",\"name\":\"sample\",\"schema\":{\"type\":\"object\"}}}}")
	if got := classifyAuto(body, model.ProtocolOpenAIResponse, 10, 10).name; got != AutoTaskExtraction {
		t.Fatalf("class=%q want extraction", got)
	}
}

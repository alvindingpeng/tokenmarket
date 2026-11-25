package relay

import (
	"strings"
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
			name:     "anthropic native custom tool definition",
			body:     `{"messages":[{"role":"user","content":"Check weather"}],"tools":[{"name":"get_weather","input_schema":{"type":"object"}}]}`,
			protocol: model.ProtocolAnthropicMessage,
			want:     AutoTaskTool,
		},
		{
			name:     "anthropic native multi-turn tool use and text tool result",
			body:     `{"messages":[{"role":"user","content":"Check weather"},{"role":"assistant","content":[{"type":"text","text":"Checking"},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Tokyo"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"Sunny 22C"}]}]}]}`,
			protocol: model.ProtocolAnthropicMessage,
			want:     AutoTaskTool,
		},
		{
			name:     "anthropic server built-in tool type is unsupported",
			body:     `{"messages":[{"role":"user","content":"Search news"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`,
			protocol: model.ProtocolAnthropicMessage,
			want:     AutoTaskUnsupported,
		},
		{
			name:     "anthropic tool result with image block is unsupported",
			body:     `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}]}`,
			protocol: model.ProtocolAnthropicMessage,
			want:     AutoTaskUnsupported,
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

func TestAnthropicToolBudgetIncludesCallMetadata(t *testing.T) {
	short := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"lookup","input":{}}]}]}`
	long := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"` + strings.Repeat("a", 900) + `","name":"lookup","input":{}}]}]}`
	a := classifyAuto([]byte(short), model.ProtocolAnthropicMessage, 0, 100)
	b := classifyAuto([]byte(long), model.ProtocolAnthropicMessage, 0, 100)
	if a.name != AutoTaskTool || b.name != AutoTaskTool || b.input <= a.input {
		t.Fatalf("tool metadata omitted from budget: short=%+v long=%+v", a, b)
	}
}

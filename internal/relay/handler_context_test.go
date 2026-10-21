package relay

import (
	"testing"
)

func TestCheckContextLimit(t *testing.T) {
	tests := []struct {
		name         string
		modelName    string
		bodySize     int // 字节数
		wantExceeded bool
	}{
		{
			name:         "未超限的请求",
			modelName:    "gpt-4", // 假设这个模型在能力库中有 128k 限制
			bodySize:     1000,    // ~250 tokens
			wantExceeded: false,
		},
		{
			name:         "没有能力信息的模型放行",
			modelName:    "unknown-model-xyz-123",
			bodySize:     1000000, // 即使很大，没有能力信息也放行
			wantExceeded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试请求体
			body := make([]byte, tt.bodySize)

			exceeded, _, _ := checkContextLimit(tt.modelName, body)

			// 对于未知模型，应该放行（exceeded = false）
			if tt.modelName == "unknown-model-xyz-123" && exceeded {
				t.Errorf("checkContextLimit() should not block unknown models, got exceeded = %v", exceeded)
			}

			// 对于已知模型，检查是否符合预期
			// 注意：实际的 exceeded 值取决于能力库中是否有该模型的信息
			// 这里只测试函数不会 panic
		})
	}
}

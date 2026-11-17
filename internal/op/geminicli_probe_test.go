package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestProbeChannelModelsGeminiCLIEndToEnd 是订阅渠道方案 S0 的验收测试。
//
// 它验证的是「建订阅渠道时, 系统能自动把上游可用模型读进来」这条链路, 而不是单个解析函数。
// 链路: ProbeChannelModels -> probeGeminiCLIModels -> LoadCodeAssist -> RetrieveUserQuota
//
//	-> AvailableModels -> []ChannelFetchModel
//
// 用假上游而不是真账号: 真实账号依赖外部网络与有效凭据, 既不可靠也不该进单测。
// 假服务端按真实协议形态应答(冒号方法 URL、顶层结果结构), 因此这仍是端到端验证。
func TestProbeChannelModelsGeminiCLIEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			// 该 RPC 的响应直接解到顶层 LoadCodeAssistResult, 没有 response 包裹。
			// 给出 project 以免落到 onboardUser 分支。
			writeJSON(w, map[string]any{
				"currentTier":             map[string]any{"id": "free-tier"},
				"cloudaicompanionProject": "test-project-123",
			})
		case "/v1internal:retrieveUserQuota":
			// 顶层 RetrieveUserQuotaResult{quota:{buckets:[...]}}。
			// remainingFraction<=0 的模型必须被过滤掉(这是实现契约)。
			writeJSON(w, map[string]any{
				"quota": map[string]any{
					"buckets": []any{
						map[string]any{"modelId": "gemini-3-flash", "remainingFraction": 0.8},
						map[string]any{"modelId": "gemini-3-pro", "remainingFraction": 0.0},
						map[string]any{"modelId": "gemini-3-ultra", "remainingFraction": 0.5},
					},
				},
			})
		default:
			t.Errorf("意外的请求路径: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	target := model.ChannelConfig{Dialect: model.DialectGeminiCLI, BaseURL: srv.URL}
	models, err := ProbeChannelModels(srv.Client(), target, "fake-access-token", "", "", "", context.Background())
	if err != nil {
		t.Fatalf("ProbeChannelModels 应能返回模型, 实际报错: %v", err)
	}

	// 验收: 只剩额度可用的两个模型, pro 因 remainingFraction=0 被过滤。
	wantNames := []string{"gemini-3-flash", "gemini-3-ultra"}
	if len(models) != len(wantNames) {
		t.Fatalf("模型数量: got %d want %d (%+v)", len(models), len(wantNames), models)
	}
	for i, want := range wantNames {
		if models[i].Name != want {
			t.Errorf("index %d: got %q want %q", i, models[i].Name, want)
		}
		// 订阅渠道的模型协议必须直接是 ProtocolGeminiCLI, 不需要再推断。
		if models[i].Protocols != model.ProtocolGeminiCLI {
			t.Errorf("index %d (%s): Protocols=%v want ProtocolGeminiCLI", i, models[i].Name, models[i].Protocols)
		}
	}
}

// TestProbeChannelModelsGeminiCLIRequiresModels 上游不给模型时探测必须报错, 不能静默返回空。
//
// 静默返回空会让界面显示"该渠道无模型", 而真实原因可能是凭据 scope 不足或 project 不对,
// 两者都需要被看见(见 channel_probe.go 对应注释)。
func TestProbeChannelModelsGeminiCLIRequiresModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1internal:retrieveUserQuota" {
			writeJSON(w, map[string]any{"quota": map[string]any{}})
			return
		}
		writeJSON(w, map[string]any{
			"currentTier":             map[string]any{"id": "free-tier"},
			"cloudaicompanionProject": "test-project-123",
		})
	}))
	defer srv.Close()

	target := model.ChannelConfig{Dialect: model.DialectGeminiCLI, BaseURL: srv.URL}
	_, err := ProbeChannelModels(srv.Client(), target, "fake-access-token", "", "", "", context.Background())
	if err == nil {
		t.Fatal("上游无可用模型时必须报错, 不能静默返回空清单")
	}
}

// TestProbeChannelModelsRequiresBaseURLUnlessSubscription 锁定 S1 的边界。
//
// 订阅渠道允许留空地址(调用信息由平台描述提供), 其余方言仍必须有地址:
// 它们没有内置默认值, 空地址只会让请求打到空 URL 上, 那种失败极难排查。
//
// 两个分支都断言, 因为只测"订阅渠道能过"会让"所有人都不校验"的退化实现也通过。
func TestProbeChannelModelsRequiresBaseURLUnlessSubscription(t *testing.T) {
	tests := []struct {
		name    string
		dialect model.Dialect
		wantErr bool
	}{
		{"普通渠道必须有地址", model.DialectGeneric, true},
		{"Gemini CLI 渠道必须有地址", model.DialectGeminiCLI, true},
		{"订阅渠道允许留空", model.DialectSubscription, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := model.ChannelConfig{Dialect: tt.dialect}
			_, err := ProbeChannelModels(http.DefaultClient, target, "tok", "", "", "", context.Background())
			if tt.wantErr && err == nil {
				t.Fatalf("方言 %s 空地址必须报错", tt.dialect)
			}
			if !tt.wantErr && errors.Is(err, ErrProbeBaseURLRequired) {
				t.Fatalf("方言 %s 空地址不应报地址缺失", tt.dialect)
			}
		})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(w, "encode error: %v", err)
	}
}

// TestProbeChannelModelsSubscriptionDialectUsesGeminiChain 是 S2 的核心验收。
//
// 订阅渠道的目的是"不填调用信息就能拉模型", 它和 gemini_cli 渠道用的是同一条上游链
// (LoadCodeAssist -> RetrieveUserQuota -> 可用模型), 但两者的方言不同。
//
// 这里专门测订阅方言: 若探测分派只认 gemini_cli, 订阅渠道会掉进通用分支,
// 去拼 OpenAI/Anthropic 的 models URL 并必然失败 —— 而失败信息会指向"上游不可用",
// 指不到真正的分派错误。
func TestProbeChannelModelsSubscriptionDialectUsesGeminiChain(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			writeJSON(w, map[string]any{
				"currentTier":             map[string]any{"id": "free-tier"},
				"cloudaicompanionProject": "test-project-123",
			})
		case "/v1internal:retrieveUserQuota":
			writeJSON(w, map[string]any{
				"buckets": []any{
					map[string]any{"modelId": "gemini-3-flash", "remainingFraction": 0.8, "remainingAmount": "1000"},
					map[string]any{"modelId": "gemini-3-ultra", "remainingFraction": 0.5},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 关键: 方言是 subscription, 不是 gemini_cli。
	target := model.ChannelConfig{Dialect: model.DialectSubscription, BaseURL: srv.URL, SubPlatform: "gemini"}
	models, err := ProbeChannelModels(srv.Client(), target, "fake-access-token", "", "", "", context.Background())
	if err != nil {
		t.Fatalf("订阅渠道探测应走 Gemini 链并返回模型, 实际报错: %v (请求路径: %v)", err, paths)
	}
	if len(models) != 2 {
		t.Fatalf("模型数量: got %d want 2 (%+v)", len(models), models)
	}
	if models[0].Name != "gemini-3-flash" || models[1].Name != "gemini-3-ultra" {
		t.Errorf("模型名不符: %+v", models)
	}
	for _, m := range models {
		if m.Protocols != model.ProtocolGeminiCLI {
			t.Errorf("%s: Protocols=%v want ProtocolGeminiCLI", m.Name, m.Protocols)
		}
	}
	// 断言真的走了 Gemini RPC: 若掉进通用分支, 这里会是 /v1/models 之类。
	sawQuota := false
	for _, p := range paths {
		if p == "/v1internal:retrieveUserQuota" {
			sawQuota = true
		}
	}
	if !sawQuota {
		t.Errorf("未调用 retrieveUserQuota, 实际请求路径: %v", paths)
	}
}

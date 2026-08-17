package relay

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/doubao"
	"github.com/looplj/axonhub/llm/transformer/gemini"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// newOutbound 为目标渠道创建统一请求的出站转换器。
func newOutbound(channelProvider model.ChannelProvider, baseURL, key string) (transformer.Outbound, error) {
	keyProvider := auth.NewStaticKeyProvider(key)
	switch channelProvider {
	case model.ChannelProviderOpenAI:
		return openai.NewOutboundTransformerWithConfig(&openai.Config{PlatformType: openai.PlatformOpenAI, BaseURL: baseURL, APIKeyProvider: keyProvider})
	case model.ChannelProviderOpenAIResponses:
		return responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: baseURL, APIKeyProvider: keyProvider})
	case model.ChannelProviderAnthropic:
		return anthropic.NewOutboundTransformerWithConfig(&anthropic.Config{Type: anthropic.PlatformDirect, BaseURL: baseURL, APIKeyProvider: keyProvider})
	case model.ChannelProviderGemini:
		return gemini.NewOutboundTransformerWithConfig(gemini.Config{BaseURL: baseURL, APIKeyProvider: keyProvider})
	case model.ChannelProviderVolcengine:
		return doubao.NewOutboundTransformerWithConfig(&doubao.Config{BaseURL: baseURL, APIKeyProvider: keyProvider})
	default:
		return nil, fmt.Errorf("unsupported channel provider: %s", channelProvider)
	}
}

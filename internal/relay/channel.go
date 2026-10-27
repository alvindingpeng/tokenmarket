package relay

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/relay/xfyun"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/sjson"
)

// buildOutbound 在渠道授权支持的协议内选出本轮上游协议, 构造对应的出站转换器, 并返回选中的协议和能否同协议透传。
// 地址由渠道的协议路径字段与地址拼接, 凭据取自目标绑定的渠道凭据。
// want 是客户端请求使用的协议, 由调用方按入站格式定出; 选中的协议随请求状态推给界面, 故一并返回。
// inboundFormat 只在 ProtocolOpenAIImage 下生效: 区分 generations / edits 两种请求路径。
func buildOutbound(channel model.Channel, grant model.ChannelGrant, channelKey model.ChannelKey, want model.Protocol, inboundFormat llm.APIFormat) (transformer.Outbound, model.Protocol, bool, error) {
	protocol, passthrough := want, grant.Protocols&want != 0
	if !passthrough {
		// 媒体协议不走跨协议回退: 客户端发起的生图/生视频请求只能由带对应协议位的授权承接,
		// 回退到 chat/response/anthropic 只会把图片或视频请求转成聊天请求打向错误端点。
		// want=Image/Video 且授权无对应位时保持 protocol=0, 由 default 分支报错并换下一个成员。
		if want != model.ProtocolOpenAIImage && want != model.ProtocolOpenAIVideo {
			switch {
			case grant.Protocols&model.ProtocolAnthropicMessage != 0:
				protocol = model.ProtocolAnthropicMessage
			case grant.Protocols&model.ProtocolOpenAIResponse != 0:
				protocol = model.ProtocolOpenAIResponse
			case grant.Protocols&model.ProtocolOpenAIChatCompletion != 0:
				protocol = model.ProtocolOpenAIChatCompletion
			case grant.Protocols&model.ProtocolXunfeiSparkWS != 0:
				// 星火 WebSocket 只承接纯文本对话(无工具/视觉), 排在 HTTP 协议之后兜底:
				// 同一授权能走 OpenAI 兼容 HTTP 时优先走它, 不丢能力。
				protocol = model.ProtocolXunfeiSparkWS
			}
		}
	}

	key := auth.NewStaticKeyProvider(channelKey.Key)
	switch protocol {
	case model.ProtocolOpenAIChatCompletion:
		outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{PlatformType: openai.PlatformOpenAI, BaseURL: channel.BaseURL, EndpointPath: channel.OpenAIChatCompletionPath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolOpenAIResponse:
		outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: channel.BaseURL, EndpointPath: channel.OpenAIResponsePath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolAnthropicMessage:
		outbound, err := anthropic.NewOutboundTransformerWithConfig(&anthropic.Config{Type: anthropic.PlatformDirect, BaseURL: channel.BaseURL, EndpointPath: channel.AnthropicMessagePath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolOpenAIImage:
		// 生成与编辑是两个不同的请求路径; openai 出站会根据入站 APIFormat 自动生成 /v1/images/generations
		// 或 /v1/images/edits, 因此这里按入站形态选择对应的渠道路径, 留空时由转换器用默认 fallback。
		endpointPath := ""
		switch inboundFormat {
		case llm.APIFormatOpenAIImageEdit:
			endpointPath = channel.OpenAIImageEditPath
		case llm.APIFormatOpenAIImageGeneration:
			endpointPath = channel.OpenAIImageGenerationPath
		}
		outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{PlatformType: openai.PlatformOpenAI, BaseURL: channel.BaseURL, EndpointPath: endpointPath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolOpenAIVideo:
		// 视频出站由转换器在 BaseURL 之后自行拼接 /videos(创建) 与 /videos/{id}(查询/删除), 不接受 EndpointPath,
		// 因此把渠道的基路径并入 BaseURL 一并交给转换器, 而不是走上面几个协议的路径字段。
		baseURL := strings.TrimSuffix(channel.BaseURL, "/") + channel.OpenAIVideoBasePath
		outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{PlatformType: openai.PlatformOpenAI, BaseURL: baseURL, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolXunfeiSparkWS:
		// 星火 WebSocket 出站: AppID 进请求帧, APIKey/APISecret 只参与 URL 签名(不进 Header);
		// Domain 决定服务版本路径, 留空时按上游模型名解析。拨号收发由 xfyun 执行器完成。
		outbound, err := xfyun.NewOutbound(xfyun.Config{
			BaseURL:     channel.BaseURL,
			Credentials: xfyun.Credentials{AppID: channelKey.AppID, APIKey: channelKey.Key, APISecret: channelKey.APISecret},
			Domain:      channelKey.Domain,
		})
		return outbound, protocol, passthrough, err
	default:
		return nil, 0, false, fmt.Errorf("channel grant %d supports no known protocol: %d", grant.ID, grant.Protocols)
	}
}

// clientHeaderPlaceholder 匹配自定义 Header 值中引用的客户端请求头
var clientHeaderPlaceholder = regexp.MustCompile(`\{client_header:[^}]+\}`)

// applyChannelConfig 按渠道配置覆盖上游请求的参数并追加自定义 Header; model 与 stream 由转发流程决定, 不允许覆盖。
func applyChannelConfig(channel model.Channel, request *httpclient.Request) error {
	// multipart 正文(生图 edits)不能按 JSON 路径改写, 参数覆盖只对 JSON 请求生效。
	isMultipart := strings.HasPrefix(strings.ToLower(request.Headers.Get("Content-Type")), "multipart/form-data")
	if channel.ParamOverride != "" && !isMultipart {
		var overrides map[string]json.RawMessage
		if err := json.Unmarshal([]byte(channel.ParamOverride), &overrides); err != nil {
			return fmt.Errorf("invalid channel parameter override: %w", err)
		}
		body := request.Body
		// 覆盖键可能自带点号或冒号, 转义后再作为 sjson 路径使用, 避免被解析成嵌套路径。
		escape := strings.NewReplacer("\\", "\\\\", ".", "\\.", ":", "\\:")
		for key, value := range overrides {
			if key == "model" || key == "stream" {
				continue
			}
			next, err := sjson.SetRawBytes(body, ":"+escape.Replace(key), value)
			if err != nil {
				return fmt.Errorf("apply channel parameter %q: %w", key, err)
			}
			body = next
		}
		request.Body = body
		if len(request.JSONBody) > 0 {
			request.JSONBody = slices.Clone(body)
		}
	}

	// 转换器已经写入的认证等敏感 Header 不允许被自定义配置覆盖。
	for _, header := range channel.CustomHeader {
		if request.Headers.Get(header.HeaderKey) != "" && httpclient.IsSensitiveHeader(header.HeaderKey) {
			continue
		}
		// 值中的 {client_header:xxx} 片段替换为客户端请求头 xxx 的实际值。
		value := clientHeaderPlaceholder.ReplaceAllStringFunc(header.HeaderValue, func(placeholder string) string {
			return request.Headers.Get(placeholder[len("{client_header:") : len(placeholder)-1])
		})
		request.Headers.Set(header.HeaderKey, value)
	}
	return nil
}

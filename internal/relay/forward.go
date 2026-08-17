package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/tidwall/sjson"
)

var errUnsupportedTarget = errors.New("unsupported relay target") // 渠道 Provider 不支持当前文字请求。

// upstreamResult 保存一次上游执行的响应、审计内容、用量和错误。
type upstreamResult struct {
	response     preparedResponse // 已验证但尚未提交给客户端的响应。
	responseBody []byte           // 上游错误或无效响应的审计内容。
	usage        *llm.Usage       // 本次上游调用可确认的用量。
	err          error            // 本次上游执行的最终错误。
}

// forwarder 执行一次真实上游请求，不选择渠道，也不写客户端响应。
type forwarder struct {
	protocol  *relayProtocol // 客户端协议规则。
	request   *relayRequest  // 可重复构造的客户端请求。
	client    *http.Client   // 渠道对应的 HTTP 客户端。
}

// executeUpstream 准备并执行上游请求，验证响应后返回上游阶段结果。
func (f *forwarder) executeUpstream(ctx context.Context, modelName string, channel *model.Channel) upstreamResult {
	passthrough := false
	switch channel.Type {
	case model.ChannelProviderOpenAI:
		passthrough = f.protocol.format == llm.APIFormatOpenAIChatCompletion
	case model.ChannelProviderOpenAIResponses:
		passthrough = f.protocol.format == llm.APIFormatOpenAIResponse
	case model.ChannelProviderAnthropic:
		passthrough = f.protocol.format == llm.APIFormatAnthropicMessage
	}
	if passthrough {
		return f.executePassthrough(ctx, modelName, channel)
	}
	return f.executeConverted(ctx, modelName, channel)
}

// validateUnifiedResponse 检查需要在客户端提交前判定失败的统一响应状态。
func validateUnifiedResponse(protocol llm.APIFormat, response *llm.Response) error {
	if response == nil {
		return errors.New("upstream response is empty")
	}
	if protocol != llm.APIFormatOpenAIResponse || len(response.Choices) == 0 || response.Choices[0].FinishReason == nil {
		return nil
	}
	switch *response.Choices[0].FinishReason {
	case "error":
		return &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response failed", Type: "response_failed"}}
	case "length":
		return &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response incomplete", Type: "response_incomplete"}}
	case "cancelled":
		return &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response cancelled", Type: "response_cancelled"}}
	default:
		return nil
	}
}

// applyChannelOptions 应用渠道参数覆盖和自定义 Header。
func applyChannelOptions(channel *model.Channel, request *httpclient.Request) error {
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		var overrides map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*channel.ParamOverride), &overrides); err != nil {
			return fmt.Errorf("invalid channel parameter override: %w", err)
		}
		modified := request.Body
		pathReplacer := strings.NewReplacer("\\", "\\\\", ".", "\\.", ":", "\\:")
		for key, value := range overrides {
			if key == "model" || key == "stream" {
				continue
			}
			next, err := sjson.SetRawBytes(modified, ":"+pathReplacer.Replace(key), value)
			if err != nil {
				return fmt.Errorf("apply channel parameter %q: %w", key, err)
			}
			modified = next
		}
		request.Body = modified
		if len(request.JSONBody) > 0 {
			request.JSONBody = slices.Clone(modified)
		}
	}
	for _, header := range channel.CustomHeader {
		if request.Headers.Get(header.HeaderKey) != "" && httpclient.IsSensitiveHeader(header.HeaderKey) {
			continue
		}
		request.Headers.Set(header.HeaderKey, header.HeaderValue)
	}
	return nil
}

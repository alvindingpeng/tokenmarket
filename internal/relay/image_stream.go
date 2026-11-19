package relay

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// 生图流式(image_generation_stream)的固有事件名。
// OpenAI Images API 以 stream:true 请求时返回 SSE 事件流: partial_image 事件携带中间结果,
// completed 事件携带一张最终图(事件数据即该图对象), 最后以 data: [DONE] 结束。
// 事件数据形状与同步响应 data[] 的成员一致({"b64_json":...} 或 {"url":...}), 因此把 completed
// 事件按原样收进 {"data":[...]} 即可复用同步响应的全部计费口径(mediaImageCount 数数组长度)。
const (
	imageStreamPartialEvent   = "image_generation.partial_image"
	imageStreamCompletedEvent = "image_generation.completed"
)

// inspectImageStreamEvent 判断一个生图流事件是否结束整个响应流, 并识别以事件形式下发的上游错误。
// 返回 true 表示流已结束; 返回的 error 非空表示该事件本身即失败, 本轮不可提交。
func inspectImageStreamEvent(event *httpclient.StreamEvent) (bool, error) {
	if event == nil || len(event.Data) == 0 {
		return false, nil
	}
	// [DONE] 是 OpenAI SSE 的通用流末标记, 生图流式同样以其收尾。
	if bytes.Equal(event.Data, llm.DoneStreamEvent.Data) {
		return true, nil
	}
	// error 事件或正文带 OpenAI 错误形状: 以事件形式下发的上游失败, 立即终止。
	var failure openai.OpenAIError
	if err := json.Unmarshal(event.Data, &failure); err != nil {
		return true, fmt.Errorf("decode image stream event: %w", err)
	}
	if event.Type == "error" || failure.Detail.Message != "" || failure.Detail.Type != "" || failure.Detail.Code != "" {
		if failure.Detail.Message == "" {
			failure.Detail.Message = "openai image stream error"
		}
		return true, &llm.ResponseError{Detail: failure.Detail}
	}
	return false, nil
}

// aggregateImageStreamEvents 从已转发的生图流事件聚合出按张计费所需的最终响应正文与用量。
// completed 事件(或匿名事件)且数据解析为含 b64_json/url 的图片对象即为一张最终图, 原样收进
// {"data":[...]}; partial_image 事件是中间态, 绝不计入最终张数。usage 若上游在最终图事件里
// 带回则按首个非空值采用, 否则保持 nil(图片按张计费为主, 不愿造的 token 账目不发明)。
// 返回值与 inbound.AggregateStreamChunks 同形状, 便于并入统一的流末收尾路径。
func aggregateImageStreamEvents(chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta) {
	var data []json.RawMessage
	var usage *llm.Usage
	for _, event := range chunks {
		if event == nil || len(event.Data) == 0 || event.Type == imageStreamPartialEvent {
			continue
		}
		var obj struct {
			B64   string     `json:"b64_json"`
			URL   string     `json:"url"`
			Usage *llm.Usage `json:"usage"`
		}
		if err := json.Unmarshal(event.Data, &obj); err != nil {
			continue
		}
		if obj.B64 == "" && obj.URL == "" {
			// 匿名且不是图片对象的事件(如 [DONE])不构成一张图。
			continue
		}
		if usage == nil && obj.Usage != nil {
			usage = obj.Usage
		}
		data = append(data, event.Data)
	}
	body, err := json.Marshal(struct {
		Data []json.RawMessage `json:"data"`
	}{Data: data})
	if err != nil {
		body = []byte("{\"data\":[]}")
	}
	return body, llm.ResponseMeta{Usage: usage}
}

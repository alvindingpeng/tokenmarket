package relay

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
)

// defaultVideoReserveSeconds 是客户端未声明 seconds 时用于冻结额度的保守时长。
// 只影响预扣金额: 上游返回时长时按返回值结算, 未返回时回落到请求声明值, 都不看这个常量。
// 取 10 秒是因为主流视频接口的默认时长在 4-8 秒之间, 略高于常见值可以少冻结几次超限的请求。
const defaultVideoReserveSeconds = 10

// isMediaProtocol 判断请求协议是否属于媒体生成。媒体请求按张或按秒计费, 正文与 token 无关,
// 限流按 (1,1) 预留即可: RPM 与并发才是真正的约束, 提示词不该被折算成 token 额度。
func isMediaProtocol(protocol model.Protocol) bool {
	return protocol == model.ProtocolOpenAIImage || protocol == model.ProtocolOpenAIVideo
}

// videoReserveSeconds 返回预扣用的时长: 客户端声明了就用声明的, 未声明才用保守默认值。
// 结算不看它, 因此偏大只会多冻结一会儿, 不会多收钱。
func videoReserveSeconds(seconds float64) float64 {
	if seconds > 0 {
		return seconds
	}
	return defaultVideoReserveSeconds
}

// parseVideoRequestUnits 从生视频请求体解析客户端声明的计费用量: seconds 是时长, resolution 是分辨率档位。
// 请求体的形状由 OpenAI Videos 协议规定, 与上游实现无关, 因此预扣阶段就能算准, 不必像生图那样按张数猜倍数。
func parseVideoRequestUnits(body []byte) model.MediaUnits {
	var parsed struct {
		Seconds    json.RawMessage `json:"seconds"`
		Size       string          `json:"size"`
		Resolution string          `json:"resolution"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return model.MediaUnits{}
	}
	return model.MediaUnits{
		Seconds:    videoSecondsValue(parsed.Seconds),
		Resolution: videoResolutionKey(parsed.Resolution, parsed.Size),
	}
}

// mediaVideoUnits 从生视频响应体解析实际计费用量。
// 上游回显 seconds 时以它为准(实际产出时长可能与请求不同); 未回显时回落到请求声明的时长。
// 缺少 id 说明响应不是任务对象(创建失败或上游返回了别的东西), 返回零值让调用方释放预扣而不计费。
func mediaVideoUnits(body string, requested model.MediaUnits) model.MediaUnits {
	if body == "" {
		return model.MediaUnits{}
	}
	var parsed struct {
		ID         string          `json:"id"`
		Seconds    json.RawMessage `json:"seconds"`
		Size       string          `json:"size"`
		Resolution string          `json:"resolution"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil || strings.TrimSpace(parsed.ID) == "" {
		return model.MediaUnits{}
	}
	units := model.MediaUnits{
		Videos:     1, // OpenAI Videos 协议一次创建只产出一条视频。
		Seconds:    videoSecondsValue(parsed.Seconds),
		Resolution: videoResolutionKey(parsed.Resolution, parsed.Size),
	}
	if units.Seconds <= 0 {
		units.Seconds = requested.Seconds
	}
	if units.Resolution == "" {
		units.Resolution = requested.Resolution
	}
	return units
}

// videoSecondsValue 解析 seconds 字段: OpenAI Videos 协议把它定义成字符串(如 "4"), 部分实现直接发数字, 两者都要认。
// 解析不出或非正数一律返回 0, 由调用方决定回落到请求声明值还是按 0 处理。
func videoSecondsValue(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		value, parseErr := strconv.ParseFloat(strings.TrimSpace(asString), 64)
		if parseErr != nil || value <= 0 {
			return 0
		}
		return value
	}
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err != nil || asNumber <= 0 {
		return 0
	}
	return asNumber
}

// videoResolutionKey 得出分档定价的查表键: 显式 resolution 优先, 否则由 size(如 1280x720) 取短边推出 720p。
// 定价表由管理员按 480p / 720p / 1080p 这类键填写, 因此这里统一归一到该形状;
// 两处都缺或格式不认时返回空串, 计费回落到不分档的每秒单价。
func videoResolutionKey(resolution, size string) string {
	if key := strings.TrimSpace(resolution); key != "" {
		return key
	}
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(size)), "x", 2)
	if len(parts) != 2 {
		return ""
	}
	width, widthErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, heightErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return ""
	}
	return strconv.Itoa(min(width, height)) + "p"
}

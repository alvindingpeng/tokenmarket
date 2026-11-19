package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// defaultVideoReserveSeconds 是客户端未声明 seconds 时用于冻结额度的保守时长。
// 只影响预扣金额: 上游返回时长时按返回值结算, 未返回时回落到请求声明值, 都不看这个常量。
// 取 10 秒是因为主流视频接口的默认时长在 4-8 秒之间, 略高于常见值可以少冻结几次超限的请求。
const defaultVideoReserveSeconds = 10

// maxVideoSeconds 是时长的有限上界: 解析、预扣、结算与派发前校验只接受 (0, maxVideoSeconds] 内的秒数。
// 在售生视频接口的时长普遍在 4-600 秒之间(OpenAI Videos 为 4/8/12 秒), 取 4 小时不会误伤任何合法时长,
// 同时把「秒数 × 每秒单价」锁进有限区间 —— NaN / Inf / 1e308 这类值一旦进冻结或结算, 金额就变成
// 非有限数, 钱账无法恢复。区间外的值不是"截断到上界", 而是整个当作非法处理:
// 计费的时长必须与转发给上游的时长一致, 任何静默改写都会造成两套口径。
const maxVideoSeconds = 4 * 60 * 60

// validVideoSeconds 判断时长是否落在可计费区间 (0, maxVideoSeconds]。
// 单条比较链同时排除 NaN(两个比较都为假)与 ±Inf(非正或越过上界), 无需单独查 IsNaN/IsInf。
func validVideoSeconds(seconds float64) bool {
	return seconds > 0 && seconds <= maxVideoSeconds
}

// isMediaProtocol 判断请求协议是否属于媒体生成。媒体请求按张或按秒计费, 正文与 token 无关,
// 限流按 (1,1) 预留即可: RPM 与并发才是真正的约束, 提示词不该被折算成 token 额度。
func isMediaProtocol(protocol model.Protocol) bool {
	return protocol == model.ProtocolOpenAIImage || protocol == model.ProtocolOpenAIVideo
}

// videoReserveSeconds 返回预扣用的时长: 客户端声明了可计费时长就用声明的, 否则用保守默认值。
// 结算不看它, 因此偏大只会多冻结一会儿, 不会多收钱; 不可计费的声明值(非有限或越界)不会参与冻结计算。
func videoReserveSeconds(seconds float64) float64 {
	if validVideoSeconds(seconds) {
		return seconds
	}
	return defaultVideoReserveSeconds
}

// boundedVideoSeconds 返回可计费的时长, 区间外的值一律归 0, 用于外部传入值(如请求声明时长)的兜底。
func boundedVideoSeconds(seconds float64) float64 {
	if validVideoSeconds(seconds) {
		return seconds
	}
	return 0
}

// parseVideoSeconds 把 seconds 原始值解析成数值: OpenAI Videos 协议把它定义成字符串(如 "4"),
// 部分实现直接发数字, 两者都要认。只负责解析, 不做区间判断 —— NaN/Inf 也可能解析"成功",
// 是否可计费由 validVideoSeconds 定夺。解析不出(布尔、对象、非数值文本)返回 ok=false,
// 由调用方决定按未声明处理还是拒绝请求。
func parseVideoSeconds(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		value, parseErr := strconv.ParseFloat(strings.TrimSpace(asString), 64)
		if parseErr != nil {
			return 0, false
		}
		return value, true
	}
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err != nil {
		return 0, false
	}
	return asNumber, true
}

// videoSecondsValue 解析 seconds 字段用于计费: 解析结果不在 (0, maxVideoSeconds] 内 ——
// 解析不出、非正、NaN/Inf 或越界 —— 一律返回 0, 由调用方决定回落到请求声明值还是按 0 处理。
// 这里绝不截断或改写合法值: 计费时长必须与转发给上游的时长一致。
func videoSecondsValue(raw json.RawMessage) float64 {
	value, ok := parseVideoSeconds(raw)
	if !ok || !validVideoSeconds(value) {
		return 0
	}
	return value
}

// videoSecondsError 校验请求声明的 seconds 原始值能否安全地预扣与结算。
// 缺字段、显式 null 与空串视为未声明(合法, 预扣走保守默认值); 其余必须解析成 (0, maxVideoSeconds] 内的数。
// 不合法的声明值应当在派发前拒绝整个请求, 而不是"按默认/上限计费却把原值转发上游":
// 计费时长与上游实际执行的时长必须一致, 静默改写任何一侧都会造成冻结、结算与上游成本三套口径。
func videoSecondsError(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == `""` {
		return nil
	}
	value, ok := parseVideoSeconds(raw)
	if !ok {
		return errors.New("video seconds must be a number")
	}
	if !validVideoSeconds(value) {
		return fmt.Errorf("video seconds %v is out of range (0, %v]", value, maxVideoSeconds)
	}
	return nil
}

// validateVideoRequestDuration 在派发前校验生视频请求声明的时长, 有错即应拒绝请求(400)。
// 正文没有 seconds 字段、显式置空或不是 JSON 都不算错 —— 前两者是"未声明", 后者由协议校验层负责;
// 这里只挡会污染预扣与结算的时长。调用方必须在预扣余额与转发上游之前调用, 否则非法时长已经生效。
func validateVideoRequestDuration(body []byte) error {
	var parsed struct {
		Seconds json.RawMessage `json:"seconds"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	return videoSecondsError(parsed.Seconds)
}

// maxVideoGuardBodyBytes 是时长闸门最多读取的正文字节数, 与上游读取侧的请求体上限同值。
// 更大的正文不再做本地校验, 由读取侧的大小上限统一拒绝; 闸门绝不做无上限的整包缓冲。
const maxVideoGuardBodyBytes = 64 << 20

// VideoCreateDurationGuard 是挂在 POST /v1/videos 路由上、转发之前的时长闸门。
// 声明了 seconds 却不是 (0, maxVideoSeconds] 内的有限数时以 400 拒绝: 请求既不冻结余额也不发往上游 ——
// 拒绝发生在派发之前, 不存在"按默认时长计费却把原值转发上游"的两套口径, 因此无需在转发流程内部再拦一次。
//
// 校验用的正文按 Content-Encoding 解开(借用与上游读取同一套解码), 压缩正文不能绕过闸门;
// 解码只发生在克隆出的请求上(解码会剥掉 Content-Encoding 头), 真实请求的头与原始字节原样交还给转发流程。
func VideoCreateDurationGuard() gin.HandlerFunc {
	inbound := openai.NewVideoInboundTransformer()
	return func(c *gin.Context) {
		if c.Request.Body == nil {
			c.Next()
			return
		}
		head, err := io.ReadAll(io.LimitReader(c.Request.Body, maxVideoGuardBodyBytes+1))
		// 已读字节与未读余量拼回原流: 闸门之后的流程看到的正文与进来时逐字节一致。
		c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), c.Request.Body))
		if err != nil {
			rejectRequest(c, inbound, err)
			return
		}
		if int64(len(head)) > maxVideoGuardBodyBytes {
			c.Next() // 超大正文由读取侧的大小上限统一拒绝, 这里不重复判定。
			return
		}
		clone := c.Request.Clone(c.Request.Context())
		clone.Body = io.NopCloser(bytes.NewReader(head))
		parsed, readErr := httpclient.ReadHTTPRequest(clone)
		if readErr != nil {
			rejectRequest(c, inbound, readErr)
			return
		}
		if err := validateVideoRequestDuration(parsed.Body); err != nil {
			rejectRequest(c, inbound, err)
			return
		}
		c.Next()
	}
}

// parseVideoRequestUnits 从生视频请求体解析客户端声明的计费用量: seconds 是时长, resolution 是分辨率档位。
// 请求体的形状由 OpenAI Videos 协议规定, 与上游实现无关, 因此预扣阶段就能算准, 不必像生图那样按张数猜倍数。
// 时长一律经 videoSecondsValue 收敛到可计费区间; 派发前的 validateVideoRequestDuration 负责拒绝非法声明,
// 这里回落 0 只是纵深防御: 任何路径都不会把非有限或越界的时长带进金额计算。
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
// 上游回显 seconds 时以它为准(实际产出时长可能与请求不同); 未回显或回显的是不可计费的值
// (NaN/Inf/越界)时按"未回显"回落到请求声明的时长, 声明值同样只在可计费区间内生效。
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
	if units.Seconds == 0 {
		// 未回显时长, 或回显了不可计费的值: 回落到请求声明的时长。
		units.Seconds = boundedVideoSeconds(requested.Seconds)
	}
	if units.Resolution == "" {
		units.Resolution = requested.Resolution
	}
	return units
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

package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// Gemini CLI 订阅渠道的 v1internal RPC 封装。
//
// ## 为什么走 Antigravity 身份
//
// 实测结论: Gemini CLI 免费层已被 Google 停用。用 pluginType:GEMINI 调 onboardUser 会返回
// 403 UNSUPPORTED_CLIENT(error_number 1013), 对个人账号必然失败。改用 Antigravity 身份
// (ideType:ANTIGRAVITY) 后同一端点返回 200, currentTier = free-tier "Antigravity",
// 且 onboardUser 会自动分配 project。因此本文件只实现 Antigravity 一条路径, 不保留
// IDE_UNSPECIFIED 分支 —— 留着它只会让人以为那条路能用。
//
// ## 协议形态(实测, 不要改)
//
// 方法调用是 RPC 冒号形态 {base}/v1internal:{method}, 不是 REST 斜杠形态。
// 写成 /v1internal/{method} 会 404, 而 404 看起来像"渠道地址配错了"。
// 例外是 LRO 轮询: GET {base}/v1internal/{lroName} 用的是斜杠, 两者不是同一种东西。
//
// ## 两个基址, 且不对称
//
// loadCodeAssist 走 prod(cloudcode-pa.googleapis.com);
// onboardUser / fetchAvailableModels / generateContent 走 daily(daily-cloudcode-pa.googleapis.com)。
// 这个不对称是实测结论, 不要为了"整齐"统一成一个 —— 统一后其中一侧会 404 或 403。
//
// ## 字段命名在一个协议里就不统一
//
// loadCodeAssist 的 metadata 用 camelCase(ideType);
// onboardUser 用 snake_case(tier_id / ide_type / ide_version / ide_name)。
// 这不是笔误, 是实测两种写法各自被接受、互相写错就被拒。故下面分两个构造函数。
//
// ## 代理
//
// 本文件所有函数只接收调用方传入的 *http.Client, 不自己建客户端。
// 客户端必须由调用方用 rhttp.ForChannel(channel) 构建 —— 那是全仓唯一的出口选择实现。
// 代理对 Gemini CLI 渠道是**硬需求**而非可选优化: 不支持的地区直连会返回
// 403 "not available in your location", 经代理后该错误消失(已实测)。
// 所以探测与转发都必须走渠道自己的 ChannelProxy / 出口池, 任一环节直连都会让渠道表现为
// "莫名其妙 403"。

// ErrGeminiCLIBaseURLRequired 渠道地址缺失: 属请求参数错误, 调用方按 400 返回。
var ErrGeminiCLIBaseURLRequired = errors.New("gemini cli: base url is required")

// ErrGeminiCLIAuthRequired 凭据(access token)缺失。
var ErrGeminiCLIAuthRequired = errors.New("gemini cli: authorization key is required")

// ErrGeminiCLIProjectRequired 缺少 GCP project id。
// Antigravity 路径通常自动分配, 但 project 为空时发请求没有意义, 故本地直接拒绝并指明该配哪个字段。
var ErrGeminiCLIProjectRequired = errors.New("gemini cli: project id is required")

// ErrGeminiCLITierRequired onboardUser 缺少 tier_id。
var ErrGeminiCLITierRequired = errors.New("gemini cli: tier id is required")

// 两个端点基址(实测)。
const (
	// GeminiCLIBaseProd 承载 loadCodeAssist(以及 LRO 轮询)。
	GeminiCLIBaseProd = "https://cloudcode-pa.googleapis.com"
	// GeminiCLIBaseDaily 承载 onboardUser / fetchAvailableModels / generateContent。
	GeminiCLIBaseDaily = "https://daily-cloudcode-pa.googleapis.com"
)

// Antigravity 身份常量(实测)。
const (
	// GeminiCLIIdeType Antigravity 的 ideType 取值; loadCodeAssist 用这个大小写原样提交。
	GeminiCLIIdeType = "ANTIGRAVITY"
	// GeminiCLIIdeVersion / GeminiCLIIdeName 只在 onboardUser 的 metadata 里出现, 用小写值。
	GeminiCLIIdeVersion = "1.1.0"
	GeminiCLIIdeName    = "antigravity"
	// GeminiCLIDefaultTier Antigravity 的免费层级 id(实测 currentTier = free-tier)。
	GeminiCLIDefaultTier = "free-tier"

	// Antigravity 必需的三个请求头。少任一个都可能被拒, 且错误体不说明原因。
	// 注意 UA 里是 google-api-nodejs-client(带 s), 写错成 google-api-monjs-client 之类不会报错, 只会被拒。
	geminiCLIAntigravityUA        = "antigravity/1.1.0 google-api-nodejs-client/10.3.0"
	geminiCLIAntigravityAPIClient = "gl-node/22.21.1"
	// geminiCLIAntigravityClientMetadata 声明客户端是 Antigravity IDE。
	//
	// 它决定 loadCodeAssist/onboardUser 走哪条 tier 分支, 不是装饰性头:
	// 声明 ANTIGRAVITY 才会拿到 currentTier=free-tier(Antigravity), 并由 onboardUser 自动分配
	// project(aicode-consumers); 走 pluginType=GEMINI 的路径会撞上 Google 已停用的个人免费层,
	// onboardUser 返回 403 UNSUPPORTED_CLIENT(error_number 1013)。
	geminiCLIAntigravityClientMetadata = `{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`
)

// GeminiCLIBases 解析本渠道该用的两个基址: (loadBase, dataBase)。
//
// 默认组合是实测可用组合: load 走 prod, 数据面走 daily。
// 渠道 BaseURL 显式配置时的处理:
//   - 留空            -> 默认组合;
//   - 等于 daily 地址 -> 默认组合(load 仍走 prod);
//   - 等于 prod 地址  -> 两侧都走 prod(用户显式要求全走 prod 时尊重之);
//   - 其它(自建镜像)  -> 两侧都用该地址, 不去猜镜像的 prod/daily 划分。
func GeminiCLIBases(target model.ChannelConfig) (loadBase, dataBase string) {
	base := strings.TrimSpace(target.BaseURL)
	switch base {
	case "", GeminiCLIBaseDaily:
		return GeminiCLIBaseProd, GeminiCLIBaseDaily
	case GeminiCLIBaseProd:
		return GeminiCLIBaseProd, GeminiCLIBaseProd
	default:
		return base, base
	}
}

// geminiCLIMethodURL 拼装 RPC 冒号方法 URL: {base}/v1internal:{method}。
func geminiCLIMethodURL(base, method string) string {
	return strings.TrimRight(base, "/") + "/v1internal:" + method
}

// geminiCLILROURL 拼装 LRO 轮询 URL: {base}/v1internal/{lroName}。
// 这里是斜杠而非冒号: 轮询的是操作资源, 不是方法调用。
func geminiCLILROURL(base, lroName string) string {
	return strings.TrimRight(base, "/") + "/v1internal/" + strings.TrimLeft(lroName, "/")
}

// loadCodeAssistMetadata 是 loadCodeAssist 的 metadata: camelCase(实测接受这种写法)。
type loadCodeAssistMetadata struct {
	IdeType string `json:"ideType"`
}

// onboardMetadata 是 onboardUser 的 metadata: 全 snake_case, 且比 loadCodeAssist 多两个字段。
// ide_version / ide_name 缺了会被当成不明客户端; 大小写混用会被拒。
type onboardMetadata struct {
	IdeType    string `json:"ide_type"`
	IdeVersion string `json:"ide_version"`
	IdeName    string `json:"ide_name"`
}

// newOnboardMetadata 固定为 Antigravity 身份。
func newOnboardMetadata() onboardMetadata {
	return onboardMetadata{
		IdeType:    GeminiCLIIdeType,
		IdeVersion: GeminiCLIIdeVersion,
		IdeName:    GeminiCLIIdeName,
	}
}

// LoadCodeAssistResult 是 loadCodeAssist 的响应, 只取本渠道用得上的字段。
//
// 注意 currentTier 在实测响应里是对象(含 id/name)而非字符串; 这里用宽松类型接收,
// 由 TierID() 统一取值, 免得把"结构变了"当成"没层级可用"。
type LoadCodeAssistResult struct {
	AllowedTiers            []LoadCodeAssistTier `json:"allowedTiers"`
	CurrentTier             json.RawMessage      `json:"currentTier"`
	IneligibleTiers         []LoadCodeAssistTier `json:"ineligibleTiers"`
	CloudaicompanionProject json.RawMessage      `json:"cloudaicompanionProject"`
}

// LoadCodeAssistTier 层级描述。线上同时出现过字符串与对象两种形态, 故两者都能解析。
type LoadCodeAssistTier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TierID 取当前层级的 id: 响应既可能是 {"id":"free-tier"} 也可能直接是 "free-tier"。
// 取不到时回退实测默认值 free-tier —— 那个值在 Antigravity 路径上稳定存在。
func (r *LoadCodeAssistResult) TierID() string {
	if r == nil {
		return GeminiCLIDefaultTier
	}
	if id := rawTierID(r.CurrentTier); id != "" {
		return id
	}
	for _, tier := range r.AllowedTiers {
		if strings.TrimSpace(tier.ID) != "" {
			return tier.ID
		}
	}
	return GeminiCLIDefaultTier
}

// ProjectID 取 loadCodeAssist 已经给出的 project(可能为空, 需 onboardUser 才分配)。
func (r *LoadCodeAssistResult) ProjectID() string {
	if r == nil {
		return ""
	}
	return rawProjectID(r.CloudaicompanionProject)
}

// rawTierID 从 json.RawMessage 里取层级 id, 兼容字符串与对象两种形态。
func rawTierID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asObject LoadCodeAssistTier
	if err := json.Unmarshal(raw, &asObject); err == nil {
		return strings.TrimSpace(asObject.ID)
	}
	return ""
}

// rawProjectID 从 json.RawMessage 里取 project id, 兼容 {"id":..} 与裸字符串。
func rawProjectID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asObject struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &asObject); err == nil && strings.TrimSpace(asObject.ID) != "" {
		return strings.TrimSpace(asObject.ID)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	return ""
}

// LoadCodeAssist 调用 POST {loadBase}/v1internal:loadCodeAssist。
// body: {"metadata":{"ideType":"ANTIGRAVITY"}}
func LoadCodeAssist(ctx context.Context, client *http.Client, loadBase, key string) (*LoadCodeAssistResult, error) {
	if strings.TrimSpace(loadBase) == "" {
		return nil, ErrGeminiCLIBaseURLRequired
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrGeminiCLIAuthRequired
	}
	body := map[string]any{"metadata": loadCodeAssistMetadata{IdeType: GeminiCLIIdeType}}
	raw, err := doGeminiCLIRPC(ctx, client, loadBase, "loadCodeAssist", key, body)
	if err != nil {
		return nil, err
	}
	var result LoadCodeAssistResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode loadCodeAssist: %w", err)
	}
	return &result, nil
}

// OnboardUserResult 是 onboardUser 的 LRO 响应。
type OnboardUserResult struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Response *struct {
		CloudaicompanionProject json.RawMessage `json:"cloudaicompanionProject"`
	} `json:"response,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// ProjectID 取 LRO 完成时分配的 project id; 未分配返回空串。
// Antigravity 路径实测返回 aicode-consumers, 但不要把它写死成默认值:
// 不同账号/地区分配结果可能不同, 写死会让"分配到了别的 project"表现为一直用错 project。
func (r *OnboardUserResult) ProjectID() string {
	if r == nil || r.Response == nil {
		return ""
	}
	return rawProjectID(r.Response.CloudaicompanionProject)
}

// OnboardUserOptions onboardUser 的可选参数。
type OnboardUserOptions struct {
	// TierID 必填; 取 loadCodeAssist 的 TierID(), 未指定时由 OnboardUser 回退默认 free-tier。
	TierID string
	// ProjectID 可选: 留空即让 Google 自动分配(Antigravity 路径的常规做法)。
	ProjectID string
	// PollTimeout LRO 轮询总超时; 0 用默认值。
	PollTimeout time.Duration
}

// LRO 轮询参数: 入驻通常数秒完成, 2s 间隔足够且不至于打爆端点; 总超时给足余量但必须有上限。
const (
	onboardPollInterval       = 2 * time.Second
	onboardDefaultPollTimeout = 60 * time.Second
)

// OnboardUser 调用 POST {dataBase}/v1internal:onboardUser, 未完成则轮询 LRO 至 done。
//
// body: {"tier_id":"free-tier","metadata":{"ide_type":"ANTIGRAVITY","ide_version":"1.1.0","ide_name":"antigravity"}}
// 注意 tier_id 是下划线写法, 不是 loadCodeAssist 那种 camelCase。
//
// 轮询是必须的: 首次响应常常 done=false 且不带 project, 不轮询会永远拿到空 project。
func OnboardUser(ctx context.Context, client *http.Client, dataBase, key string, opts OnboardUserOptions) (*OnboardUserResult, error) {
	if strings.TrimSpace(dataBase) == "" {
		return nil, ErrGeminiCLIBaseURLRequired
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrGeminiCLIAuthRequired
	}
	tier := strings.TrimSpace(opts.TierID)
	if tier == "" {
		tier = GeminiCLIDefaultTier
	}
	body := map[string]any{
		"tier_id":  tier,
		"metadata": newOnboardMetadata(),
	}
	if project := strings.TrimSpace(opts.ProjectID); project != "" {
		body["cloudaicompanionProject"] = map[string]string{"id": project}
	}
	raw, err := doGeminiCLIRPC(ctx, client, dataBase, "onboardUser", key, body)
	if err != nil {
		return nil, err
	}
	var lro OnboardUserResult
	if err := json.Unmarshal(raw, &lro); err != nil {
		return nil, fmt.Errorf("decode onboardUser: %w", err)
	}
	if lro.Done {
		return finishOnboard(&lro)
	}
	if strings.TrimSpace(lro.Name) == "" {
		return nil, errors.New("gemini cli: onboardUser returned done=false without an operation name")
	}
	timeout := opts.PollTimeout
	if timeout <= 0 {
		timeout = onboardDefaultPollTimeout
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	final, err := pollGeminiCLILRO(pollCtx, client, dataBase, key, lro.Name)
	if err != nil {
		return nil, fmt.Errorf("poll onboardUser LRO: %w", err)
	}
	return finishOnboard(final)
}

// finishOnboard 统一处理 LRO 终态: 带 error 即转成 error。
func finishOnboard(lro *OnboardUserResult) (*OnboardUserResult, error) {
	if lro.Error != nil {
		return nil, fmt.Errorf("gemini cli: onboardUser failed: code=%d status=%s msg=%s",
			lro.Error.Code, lro.Error.Status, lro.Error.Message)
	}
	return lro, nil
}

// pollGeminiCLILRO 轮询 GET {base}/v1internal/{name} 直到 done==true。
func pollGeminiCLILRO(ctx context.Context, client *http.Client, base, key, name string) (*OnboardUserResult, error) {
	url := geminiCLILROURL(base, name)
	ticker := time.NewTicker(onboardPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		applyGeminiCLIHeaders(req, key)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("gemini cli LRO %s: status=%d body=%s", url, resp.StatusCode, string(raw))
		}
		var lro OnboardUserResult
		if err := json.Unmarshal(raw, &lro); err != nil {
			return nil, fmt.Errorf("decode LRO: %w", err)
		}
		if lro.Error != nil {
			return nil, fmt.Errorf("gemini cli: onboardUser failed: code=%d status=%s msg=%s",
				lro.Error.Code, lro.Error.Status, lro.Error.Message)
		}
		if lro.Done {
			return &lro, nil
		}
	}
}

// RetrieveUserQuotaResult 是 retrieveUserQuota 的响应。
// 形如 {"quota":{"buckets":[{"modelId":"gemini-3-flash","remainingFraction":0.8,...}]}}
type RetrieveUserQuotaResult struct {
	Quota *UserQuota `json:"quota,omitempty"`
}

// UserQuota 配额体。
type UserQuota struct {
	Buckets []UserQuotaBucket `json:"buckets"`
}

// UserQuotaBucket 单个模型的配额桶。
type UserQuotaBucket struct {
	ModelID           string  `json:"modelId"`
	RemainingFraction float64 `json:"remainingFraction"`
	RemainingAmount   float64 `json:"remainingAmount"`
	ResetTime         string  `json:"resetTime,omitempty"`
}

// RetrieveUserQuota 调用 POST {dataBase}/v1internal:retrieveUserQuota。
//
// 这是 gemini-cli 实际使用的模型清单来源: refreshUserQuota() -> requestPost('retrieveUserQuota', {project}),
// 读 quota.buckets[].modelId / remainingFraction / resetTime。
// 对 @google/gemini-cli@0.68.0 发行包取证, fetchAvailableModels 全包 0 次命中, CLI 从不调用它 ——
// 因此本包只实现 retrieveUserQuota, 不保留 fetchAvailableModels, 避免两套并存。
func RetrieveUserQuota(ctx context.Context, client *http.Client, dataBase, key, projectID string) (*RetrieveUserQuotaResult, error) {
	if strings.TrimSpace(dataBase) == "" {
		return nil, ErrGeminiCLIBaseURLRequired
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrGeminiCLIAuthRequired
	}
	project := strings.TrimSpace(projectID)
	if project == "" {
		return nil, ErrGeminiCLIProjectRequired
	}
	body := map[string]any{"project": project}
	raw, err := doGeminiCLIRPC(ctx, client, dataBase, "retrieveUserQuota", key, body)
	if err != nil {
		return nil, err
	}
	var result RetrieveUserQuotaResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode retrieveUserQuota: %w", err)
	}
	return &result, nil
}

// AvailableModels 从配额桶里取出可用模型清单。
//
// 过滤规则: remainingFraction <= 0 的模型视为不可用(已耗尽)。
// 不在 buckets 里的模型同样不可用 —— 配额列表就是该账号的模型白名单,
// "没出现在列表里"意味着没有额度, 而不是"漏了"。
// 同名桶去重并按 modelId 稳定排序: 界面排列与测试断言都不随刷新变化。
func (r *RetrieveUserQuotaResult) AvailableModels() []string {
	if r == nil || r.Quota == nil {
		return nil
	}
	models := make([]string, 0, len(r.Quota.Buckets))
	seen := make(map[string]struct{}, len(r.Quota.Buckets))
	for _, bucket := range r.Quota.Buckets {
		id := strings.TrimSpace(bucket.ModelID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if bucket.RemainingFraction <= 0 {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	sort.Strings(models)
	return models
}

// ParseRetrieveUserQuota 解析响应并直接给出可用模型清单;
// 单独导出以便单测直接喂真实响应样例, 不必起 HTTP 服务。
func ParseRetrieveUserQuota(raw []byte) ([]string, error) {
	var result RetrieveUserQuotaResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode retrieveUserQuota: %w", err)
	}
	return result.AvailableModels(), nil
}

// GenerateContentEnvelope 是 generateContent/streamGenerateContent 的请求信封。
//
// 逐字形态(取证自 CLI 0.68.0):
//
//	{ model, project, user_prompt_id, request: { contents, systemInstruction, tools, generationConfig, session_id }, enabled_credit_types? }
//
// 内层 request 里是 session_id(下划线), 不是 sessionId。
// enabled_credit_types 仅在 credits 开关时才发, 值为 "GOOGLE_ONE_AI"; 本渠道默认不发。
// user_prompt_id 可留空, 这里接受调用方传入。
type GenerateContentEnvelope struct {
	Model              string          `json:"model"`
	Project            string          `json:"project"`
	UserPromptID       string          `json:"user_prompt_id,omitempty"`
	Request            json.RawMessage `json:"request"`
	EnabledCreditTypes []string        `json:"enabled_credit_types,omitempty"`
}

// BuildGenerateContentEnvelope 构造信封。
//
// requestBody 是标准 Gemini generateContent 请求体(contents/systemInstruction/tools/generationConfig),
// 原样塞进 request 字段 —— 中间再走一次结构体往返只会丢字段。
// sessionID 会写进内层 request 的 session_id; 为空时不写该键。
func BuildGenerateContentEnvelope(projectID, modelName, userPromptID, sessionID string, requestBody []byte) (GenerateContentEnvelope, error) {
	project := strings.TrimSpace(projectID)
	if project == "" {
		return GenerateContentEnvelope{}, ErrGeminiCLIProjectRequired
	}
	model := strings.TrimSpace(modelName)
	if model == "" {
		return GenerateContentEnvelope{}, errors.New("gemini cli: model is required")
	}
	trimmed := strings.TrimSpace(string(requestBody))
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return GenerateContentEnvelope{}, errors.New("gemini cli: request body is not valid JSON")
	}
	inner := json.RawMessage(trimmed)
	if sid := strings.TrimSpace(sessionID); sid != "" {
		// 内层 request 是任意 JSON 对象; 只在能解析成对象时注入 session_id,
		// 否则原样保留 —— 为一个可选字段破坏请求体不值得。
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &fields); err == nil && fields != nil {
			if encoded, err := json.Marshal(sid); err == nil {
				fields["session_id"] = encoded
				if merged, err := json.Marshal(fields); err == nil {
					inner = merged
				}
			}
		}
	}
	return GenerateContentEnvelope{
		Model:        model,
		Project:      project,
		UserPromptID: strings.TrimSpace(userPromptID),
		Request:      inner,
	}, nil
}

// GenerateContentResponse 是 generateContent 的**双层**响应。
//
// 真实形状(取证): { traceId, response: { candidates[], usageMetadata, promptFeedback, modelVersion,
// automaticFunctionCallingHistory }, consumedCredits[], remainingCredits[] }
// 业务字段全部在 response 里; 直接读顶层 candidates 会永远是 null 且不报错,
// 表现为"上游返回了但解析不出内容", 极难定位。
type GenerateContentResponse struct {
	TraceID          string          `json:"traceId,omitempty"`
	Response         *GeminiResponse `json:"response,omitempty"`
	ConsumedCredits  []any           `json:"consumedCredits,omitempty"`
	RemainingCredits []any           `json:"remainingCredits,omitempty"`
}

// GeminiResponse 是内层标准 Gemini generateContent 响应, 字段原样保留供 relay 层转换。
type GeminiResponse struct {
	Candidates     []json.RawMessage `json:"candidates"`
	UsageMetadata  json.RawMessage   `json:"usageMetadata,omitempty"`
	PromptFeedback json.RawMessage   `json:"promptFeedback,omitempty"`
	ModelVersion   string            `json:"modelVersion,omitempty"`
}

// UnwrapGenerateContentResponse 拆开双层包装, 返回内层标准 Gemini 响应原文。
//
// 单独抽出来是因为这层包装最容易写错, 且写错了不报错(只是 candidates 为 null)。
// 集中一处解包, relay 层与测试都只认这一条路径。
// response 缺失时原样返回入参(交给调用方按标准 Gemini 响应处理): 硬报错会让某些镜像端点完全不可用。
func UnwrapGenerateContentResponse(raw []byte) ([]byte, error) {
	var envelope GenerateContentResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode generateContent response: %w", err)
	}
	if envelope.Response == nil {
		return raw, nil
	}
	inner, err := json.Marshal(envelope.Response)
	if err != nil {
		return nil, fmt.Errorf("re-encode generateContent response: %w", err)
	}
	return inner, nil
}

// GenerateContent 调用 POST {dataBase}/v1internal:generateContent (非流式)。
// 返回上游**原始**响应体(仍是双层信封): 解包由 UnwrapGenerateContentResponse 负责,
// 协议转换由 relay 层负责, 本层不碰协议差异。
func GenerateContent(ctx context.Context, client *http.Client, dataBase, key, projectID, modelName, userPromptID, sessionID string, requestBody []byte) ([]byte, error) {
	if strings.TrimSpace(dataBase) == "" {
		return nil, ErrGeminiCLIBaseURLRequired
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrGeminiCLIAuthRequired
	}
	envelope, err := BuildGenerateContentEnvelope(projectID, modelName, userPromptID, sessionID, requestBody)
	if err != nil {
		return nil, err
	}
	return doGeminiCLIRPC(ctx, client, dataBase, "generateContent", key, envelope)
}

// StreamGenerateContent 已停用于转发路径(relay 现用 axonhub antigravity.Transformer, SSE 解包由库内部处理)。
// 保留仅供: 1) 内部探测/调试时直接调用; 2) 单测覆盖信封构造与流式双层解包逻辑。
// 如需在探测外部使用, 请先评估是否会与定时任务的 token 刷新产生冲突(见注释)。
func StreamGenerateContent(ctx context.Context, client *http.Client, dataBase, key, projectID, modelName, userPromptID, sessionID string, requestBody []byte) (*http.Response, error) {
	if strings.TrimSpace(dataBase) == "" {
		return nil, ErrGeminiCLIBaseURLRequired
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrGeminiCLIAuthRequired
	}
	envelope, err := BuildGenerateContentEnvelope(projectID, modelName, userPromptID, sessionID, requestBody)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal generateContent envelope: %w", err)
	}
	url := geminiCLIMethodURL(dataBase, "streamGenerateContent") + "?alt=sse"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	applyGeminiCLIHeaders(req, key)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("gemini cli streamGenerateContent: status=%d body=%s", resp.StatusCode, string(body))
	}
	return resp, nil
}

// applyGeminiCLIHeaders 设置 Antigravity 身份所需的全部请求头。
//
// 之前按 ideType 分支只在 ANTIGRAVITY 时加, 现在本渠道只有这一条路径, 故无条件加。
// 少任一必填头都会 403; 观察到错误体不提示缺哪个头, 所以这里集中一处维护。
func applyGeminiCLIHeaders(req *http.Request, key string) {
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", geminiCLIAntigravityUA)
	req.Header.Set("X-Goog-Api-Client", geminiCLIAntigravityAPIClient)
	// 缺这个头会退回 pluginType=GEMINI 的 tier 分支, 撞上已停用的个人免费层。
	req.Header.Set("Client-Metadata", geminiCLIAntigravityClientMetadata)
}

// doGeminiCLIRPC 执行 RPC 冒号方法调用并返回响应体。
//
// 非 2xx 一律转成带状态码与上游原文的错误。上游错误体里通常写明了具体原因
// (例如 403 的地区限制文案 "not available in your location"), 丢掉它会让界面只剩"探测失败",
// 而地区限制与外网不可达的排查方向完全不同。
func doGeminiCLIRPC(ctx context.Context, client *http.Client, base, method, key string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal %s request: %w", method, err)
	}
	url := geminiCLIMethodURL(base, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	applyGeminiCLIHeaders(req, key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini cli %s: status=%d body=%s", method, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

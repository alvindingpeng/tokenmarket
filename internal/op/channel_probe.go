package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/dlclark/regexp2"
)

// ErrProbeBaseURLRequired 渠道地址缺失: 属请求参数错误而非上游故障, 调用方按 400 返回。
var ErrProbeBaseURLRequired = errors.New("channel base url is required")

// ErrProbeInvalidFilter 过滤表达式无法编译: 属请求参数错误, 调用方按 400 返回。
var ErrProbeInvalidFilter = errors.New("invalid filter regex")

// ProbeChannelModels 按渠道配置与凭据拉取上游模型列表, 并按过滤表达式筛选后返回。
// 同时探测 OpenAI 与 Anthropic 两侧, 谁返回了哪些模型, 就给对应协议位打勾: 协议支持由探测结果决定, 无需用户声明。
// OpenAI 侧记为 Responses 而不是 Chat: Chat Completions 已被官方标记弃用, 新渠道应默认走 Responses,
// 仍需 Chat 的渠道由用户在界面上手动勾选。两侧的 /models 地址与认证形态不同, 故必须分别探测:
// 单协议上游只有一侧会成功, "哪侧成功" 本身就是协议支持的证据。
// 只有两侧都失败才算失败; 一侧失败属正常情况, 单协议上游本就只有一侧讲得通, 按成功那侧的结果返回。
// 编辑表单的手动刷新与渠道保存后的自动添加共用此函数, 保证两侧看到同一份结果。
func ProbeChannelModels(client *http.Client, target model.ChannelConfig, key string, ctx context.Context) ([]model.ChannelFetchModel, error) {
	// 探测收的是尚未落库的提交配置, 不经 normalizeChannelConfig, 故在此自行去空白;
	// 其中只有地址是硬需求: 渠道尚未命名时也可试拉, 故名称不在此校验。
	target.BaseURL = strings.TrimSpace(target.BaseURL)
	target.ChannelProxy = strings.TrimSpace(target.ChannelProxy)
	target.MatchRegex = strings.TrimSpace(target.MatchRegex)
	if target.BaseURL == "" {
		return nil, ErrProbeBaseURLRequired
	}

	// 客户端由调用方按代理配置构建(编辑表单与自动添加共用), 本函数只管探测与合并。
	var err error

	var openaiModels, anthropicModels []string
	var openaiErr, anthropicErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		openaiModels, openaiErr = fetchOpenAIModels(client, ctx, target, key, modelsURL(target.BaseURL, target.OpenAIResponsePath))
	}()
	go func() {
		defer wg.Done()
		anthropicModels, anthropicErr = fetchAnthropicModels(client, ctx, target, key, modelsURL(target.BaseURL, target.AnthropicMessagePath))
	}()
	wg.Wait()

	if openaiErr != nil && anthropicErr != nil {
		// 上游鉴权失败或地址不通属于调用方配置问题, 带上上游原文, 便于在界面上直接看到原因。
		return nil, fmt.Errorf("openai: %v; anthropic: %v", openaiErr, anthropicErr)
	}

	var re, reGlobal *regexp2.Regexp
	if target.MatchRegex != "" {
		if re, err = regexp2.Compile(target.MatchRegex, regexp2.ECMAScript); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrProbeInvalidFilter, err)
		}
	}
	// 全局过滤由设置页维护, 与渠道过滤同取 AND: 模型须同时通过两枚正则才保留, 留空的一侧不生效。
	// 设置缺失按不过滤处理: 启动初始化会补齐默认值, 缺行只可能出现在旧库尚未刷新的瞬间。
	globalFilter, _ := SettingGetString(model.SettingKeyModelFilter)
	if globalFilter != "" {
		if reGlobal, err = regexp2.Compile(globalFilter, regexp2.ECMAScript); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrProbeInvalidFilter, err)
		}
	}

	// 模型名须同时通过渠道与全局两枚过滤正则, 编译与匹配错误统一按错误返回。
	matches := func(name string) (bool, error) {
		if re != nil {
			matched, err := re.MatchString(name)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		if reGlobal != nil {
			matched, err := reGlobal.MatchString(name)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}

	// 两侧结果按名称合并成一份有序集合: 同名模型在两侧都出现时, 协议位取并集。
	// 保持首次出现的顺序, 界面上模型的排列才与上游返回的一致;
	// 先并入 OpenAI 再并入 Anthropic, 顺序写死而不用 map 遍历, 否则界面上的模型排列会随每次刷新变化。
	protocolsByModel := make(map[string]model.Protocol, len(openaiModels)+len(anthropicModels))
	order := make([]string, 0, len(openaiModels)+len(anthropicModels))
	for _, name := range openaiModels {
		matched, err := matches(name)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		if _, ok := protocolsByModel[name]; !ok {
			order = append(order, name)
		}
		protocolsByModel[name] |= model.ProtocolOpenAIResponse
	}
	for _, name := range anthropicModels {
		matched, err := matches(name)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		if _, ok := protocolsByModel[name]; !ok {
			order = append(order, name)
		}
		protocolsByModel[name] |= model.ProtocolAnthropicMessage
	}

	models := make([]model.ChannelFetchModel, 0, len(order))
	for _, name := range order {
		// 生图模型无法从 /models 端点确认能力, 按名称关键词补一个生图协议位的默认勾选;
		// 管理员在授权矩阵中仍可自由增删该位, 这里只影响探测后的初始状态。
		if matchesImageModelName(name) {
			protocolsByModel[name] |= model.ProtocolOpenAIImage
		}
		models = append(models, model.ChannelFetchModel{Name: name, Protocols: protocolsByModel[name]})
	}
	return models, nil
}

// imageModelNamePattern 常见生图模型的名称关键词; 命中即默认勾选生图协议位。
// 覆盖 OpenAI dall-e / gpt-image, Black Forest Labs flux, 阿里 seedream/wanx, Google imagen,
// Stability sd 系列, Midjourney 代理站命名与 recraft / ideogram 等主流供应商。
var imageModelNamePattern = regexp2.MustCompile(`dall-e|gpt-image|chatgpt-image|image-|imagen|flux|seedream|seededit|wanx|stable-diffusion|sdxl|sd3|midjourney|mj-|ideogram|recraft|playground-v`, regexp2.None)

// matchesImageModelName 判断模型名是否命中生图关键词; regexp2 编译失败不可能发生(模式是常量)。
func matchesImageModelName(name string) bool {
	matched, err := imageModelNamePattern.MatchString(name)
	return err == nil && matched
}

// modelsURL 取协议请求路径的父级目录, 与地址拼成同级的 /models 地址。
// 例如 /v1/chat/completions 与 /v1/messages 都得到 /v1/models, /chat/completions 得到 /models。
func modelsURL(baseURL, protocolPath string) string {
	parent := path.Dir(strings.TrimRight(protocolPath, "/"))
	// Anthropic 的 /v1/messages 只有一层, 父级即 /v1; Chat 的 /v1/chat/completions 需要再上一层。
	if strings.HasSuffix(parent, "/chat") {
		parent = path.Dir(parent)
	}
	if parent == "." || parent == "/" {
		parent = ""
	}
	return strings.TrimRight(baseURL, "/") + parent + "/models"
}

// refer: https://platform.openai.com/docs/api-reference/models/list
func fetchOpenAIModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}

	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	result, err := decodeModelList[model.OpenAIModelList](response)
	if err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// refer: https://platform.claude.com/docs
func fetchAnthropicModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	var allModels []string
	var afterID string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		for _, header := range target.CustomHeader {
			if header.HeaderKey != "" {
				req.Header.Set(header.HeaderKey, header.HeaderValue)
			}
		}
		if afterID != "" {
			q := req.URL.Query()
			q.Set("after_id", afterID)
			req.URL.RawQuery = q.Encode()
		}

		response, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		// 分页时每轮都会新建响应, 必须当轮读完即关; 用 defer 会攒到整个函数返回才释放。
		result, err := decodeModelList[model.AnthropicModelList](response)
		if err != nil {
			return nil, err
		}
		for _, m := range result.Data {
			allModels = append(allModels, m.ID)
		}
		if !result.HasMore {
			break
		}
		afterID = result.LastID
	}
	return allModels, nil
}

// decodeModelList 关闭响应并把响应体解成模型列表; 非 2xx 时按上游错误返回。
// 两侧解析流程一致, 只有目标结构不同, 故用类型参数收敛; 分页调用要求当轮读完即关, 关闭点放在此处最稳。
func decodeModelList[T any](response *http.Response) (T, error) {
	defer response.Body.Close()
	var result T
	// 上游报错时响应体常是能被正常解码的 JSON, 若不先拦下, 模型列表会解成空列表并当作成功;
	// 响应体截断到 512 字节: 部分上游在鉴权失败时返回整页 HTML, 全文带到界面上无用。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := io.ReadAll(io.LimitReader(response.Body, 512))
		if err != nil {
			return result, fmt.Errorf("upstream %s", response.Status)
		}
		return result, fmt.Errorf("upstream %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}


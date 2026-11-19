package op

import (
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// MarketBrandOf 解析模型名的厂商品牌键, 供广场按品牌筛选与选图标。
//
// 三段式, 依次放宽:
//  1. model_capabilities.family 的首段(官方登记, 最可信): 如 claude-sonnet → anthropic, gpt-luna → openai;
//  2. 名称关键词兜底: 官方登记缺失或 family 为空时(如 step-3.7-flash、deepseek-v4.1-flash-eco);
//  3. 都不中 → model.MarketBrandOther, 界面显示"其他"。
//
// 为什么真源放后端: 品牌是**筛选维度**, 前端只按品牌键选图标与颜色。
// 若前后端各带一份关键词表, 同一个模型会出现"后端说 openai、前端画 deepseek 图标"的分叉,
// 且筛选结果与图标不一致时无法判断哪边对。故 web/src/lib/model-icons.tsx 只保留「品牌键 → 图标」映射。
func MarketBrandOf(modelName string) string {
	key := strings.ToLower(strings.TrimSpace(modelName))
	if key == "" {
		return model.MarketBrandOther
	}
	if brand, ok := marketBrandOfFamily(key); ok {
		return brand
	}
	return marketBrandOfKeyword(key)
}

// marketBrandOfFamily 查官方登记的 family 首段。family 取自 capabilityCache(与渠道探测预勾选同源),
// 形如 claude-sonnet / deepseek-flash / gpt-terra / gemini-flash, 首段即厂商标识。
//
// 注意: 这里不按模型名精确查 capability 的完整条目, 只读 family 这一列 —— 能力条目的 key 可能是
// 带厂商前缀的 openai/gpt-image-1, 而渠道里注册的常是裸名; CapabilityGet 会为裸名做宽松匹配,
// 那套"猜"的语义用于预勾选协议位是合理的, 用于品牌筛选却会串味(把别人的 family 按过来)。
func marketBrandOfFamily(key string) (string, bool) {
	capability, ok := CapabilityGet(key)
	if !ok || capability.Family == "" {
		return "", false
	}
	head := strings.ToLower(capability.Family)
	if at := strings.Index(head, "-"); at >= 0 {
		head = head[:at]
	}
	brand, ok := marketFamilyBrands[head]
	return brand, ok
}

// marketFamilyBrands 是 family 首段到品牌键的对照表。
//
// 只收"首段就是厂商"的登记: family 首段偶尔是产品线而不是厂商(qwen3.5、mm-poly 之类),
// 未命中的落到关键词兜底, 再不行给 other —— 猜错品牌比显示"其他"更糟, 用户会按错的品牌筛掉真模型。
var marketFamilyBrands = map[string]string{
	"claude":     model.MarketBrandAnthropic,
	"deepseek":   model.MarketBrandDeepSeek,
	"gpt":        model.MarketBrandOpenAI,
	"o":          model.MarketBrandOpenAI, // o1/o3 系列的 family 首段。
	"gemini":     model.MarketBrandGoogle,
	"gemma":      model.MarketBrandGoogle,
	"imagen":     model.MarketBrandGoogle,
	"veo":        model.MarketBrandGoogle,
	"lyria":      model.MarketBrandGoogle,
	"glm":        model.MarketBrandZhipu,
	"minimax":    model.MarketBrandMiniMax,
	"mimo":       model.MarketBrandXiaomi,
	"qwen":       model.MarketBrandAlibaba,
	"qvq":        model.MarketBrandAlibaba,
	"grok":       model.MarketBrandXAI,
	"kimi":       model.MarketBrandMoonshot,
	"mistral":    model.MarketBrandMistral,
	"ministral":  model.MarketBrandMistral,
	"codestral":  model.MarketBrandMistral,
	"devstral":   model.MarketBrandMistral,
	"pixtral":    model.MarketBrandMistral,
	"voxtral":    model.MarketBrandMistral,
	"llama":      model.MarketBrandMeta,
	"nova":       model.MarketBrandAmazon,
	"sonar":      model.MarketBrandPerplexity,
	"command":    model.MarketBrandCohere,
	"cohere":     model.MarketBrandCohere,
	"phi":        model.MarketBrandMicrosoft,
	"nemotron":   model.MarketBrandNVIDIA,
	"step":       model.MarketBrandStepFun,
	"ernie":      model.MarketBrandBaidu,
	"hunyuan":    model.MarketBrandTencent,
	"doubao":     model.MarketBrandByteDance,
	"seed":       model.MarketBrandByteDance,
	"yi":         model.MarketBrandYi,
	"internlm":   model.MarketBrandInternLM,
	"spark":      model.MarketBrandIflytek,
	"kat":        model.MarketBrandKuaishou,
	"whisper":    model.MarketBrandOpenAI,
	"sora":       model.MarketBrandOpenAI,
	"dall":       model.MarketBrandOpenAI,
	"titan":      model.MarketBrandAmazon,
	"v0":         model.MarketBrandVercel,
	"flux":       model.MarketBrandBlackForest,
	"runway":     model.MarketBrandRunway,
	"recraft":    model.MarketBrandRecraft,
	"ideogram":   model.MarketBrandIdeogram,
	"elevenlabs": model.MarketBrandElevenLabs,
}

// marketBrandKeywords 是名称关键词兜底表, 顺序即优先级(先命中先用)。
// 只收厂商专属、不会误伤的关键词: "spark" 之类的短词不放进兜底, 它已由 family 处理,
// 放进兜底只会把带 spark 字样的无关模型误判成星火。
var marketBrandKeywords = []struct {
	brand    string
	keywords []string
}{
	{model.MarketBrandAnthropic, []string{"claude", "anthropic"}},
	{model.MarketBrandOpenAI, []string{"gpt-", "chatgpt", "dall-e"}},
	{model.MarketBrandGoogle, []string{"gemini", "gemma", "palm"}},
	{model.MarketBrandDeepSeek, []string{"deepseek"}},
	{model.MarketBrandXAI, []string{"grok", "xai"}},
	{model.MarketBrandAlibaba, []string{"qwen", "qwq"}},
	{model.MarketBrandZhipu, []string{"glm", "chatglm", "z-ai"}},
	{model.MarketBrandMiniMax, []string{"minimax", "abab"}},
	{model.MarketBrandMoonshot, []string{"moonshot", "kimi"}},
	{model.MarketBrandMistral, []string{"mistral", "mixtral", "codestral", "pixtral"}},
	{model.MarketBrandMeta, []string{"llama", "meta-llama"}},
	{model.MarketBrandXiaomi, []string{"mimo", "xiaomi"}},
	{model.MarketBrandByteDance, []string{"doubao", "skylark", "bytedance"}},
	{model.MarketBrandYi, []string{"yi-", "01-ai"}},
	{model.MarketBrandTencent, []string{"hunyuan"}},
	{model.MarketBrandIflytek, []string{"spark-x", "iflytek"}},
	{model.MarketBrandBaidu, []string{"ernie", "wenxin"}},
	{model.MarketBrandInternLM, []string{"internlm"}},
	{model.MarketBrandStepFun, []string{"step-", "stepfun"}},
	{model.MarketBrandNVIDIA, []string{"nvidia", "nemotron"}},
	{model.MarketBrandCohere, []string{"cohere", "command"}},
	{model.MarketBrandPerplexity, []string{"perplexity", "sonar"}},
	{model.MarketBrandMicrosoft, []string{"phi-"}},
	{model.MarketBrandKuaishou, []string{"kat-"}},
}

// marketBrandOfKeyword 按关键词兜底; 都不中给 other。
func marketBrandOfKeyword(key string) string {
	for _, entry := range marketBrandKeywords {
		for _, keyword := range entry.keywords {
			if strings.Contains(key, keyword) {
				return entry.brand
			}
		}
	}
	return model.MarketBrandOther
}

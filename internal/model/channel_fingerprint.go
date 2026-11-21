package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
)

// ChannelConfigFingerprint 计算渠道关键配置的口径指纹: 只对「会改变供给行为」的字段取值。
//
// 判据是"这一项改了, 用户拿到的商品会不会变", 而不是"这一项是不是配置":
//   - dialect 决定出站转换器, base_url 与六个协议路径决定请求打到哪、拼成什么端点 ——
//     任何一项被改, 同一个模型名在用户侧代表的就不再是同一件商品;
//   - sub_platform 决定订阅渠道的上游供给面(账号平台、凭据种类、地址装配全由平台描述给出),
//     换平台等于整条供给链换供应商, 这与"改了个标识"是两件事;
//   - 模型清单决定可选路由集合本身。
//
// 价格变动不入指纹: 改价已有 models/reprice 审计链路, 且高频调价不应反复卡审核。
// 代理 / 自定义 Header / 参数覆盖属运维配置, 同样不入指纹 —— 它们能改出站形态,
// 但改的是"怎么发出去"而不是"卖给用户的是什么", 且渠道商自己天天在调, 入指纹等于让人反复复审。
// 模型清单排序后哈希, 避免无序抖动误报。
//
// 为什么是纯函数且放在 model 层: 迁移要重算基线, 而 internal/db/migrate 不能 import internal/op
// (op -> db -> migrate -> op 成环)。审计口径只允许有一份实现 —— 两处各写一份必然漂移,
// 而漂移的表现是"迁移算出的基线与运行期算出的指纹不等", 也就是所有已审渠道一次性全转 stale。
func ChannelConfigFingerprint(config ChannelConfig, modelNames []string) string {
	names := append([]string(nil), modelNames...)
	sort.Strings(names)
	// 去重: 同名模型在提交侧与缓存侧可能各有重复, 哈希前归一, 免得同内容两条路径算出不同指纹。
	names = slices.Compact(names)
	h := sha256.New()
	fmt.Fprintf(h, "dialect=%s\n", config.Dialect)
	fmt.Fprintf(h, "base=%s\n", config.BaseURL)
	fmt.Fprintf(h, "chat=%s\n", config.OpenAIChatCompletionPath)
	fmt.Fprintf(h, "resp=%s\n", config.OpenAIResponsePath)
	fmt.Fprintf(h, "imggen=%s\n", config.OpenAIImageGenerationPath)
	fmt.Fprintf(h, "imgedit=%s\n", config.OpenAIImageEditPath)
	fmt.Fprintf(h, "anth=%s\n", config.AnthropicMessagePath)
	// 视频基路径与上面五条同类: 它决定视频请求落在哪个路径, 漏了它等于"视频这一路可以随便改"。
	fmt.Fprintf(h, "videobase=%s\n", config.OpenAIVideoBasePath)
	// 订阅平台标识: 换它等于换上游供给面(见函数头注释), 不是普通配置项。
	fmt.Fprintf(h, "subplatform=%s\n", config.SubPlatform)
	for _, name := range names {
		fmt.Fprintf(h, "model=%s\n", name)
	}
	return hex.EncodeToString(h.Sum(nil))
}

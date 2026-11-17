// Package subplatform 定义"订阅平台描述": 一个订阅平台需要什么, 集中写在一处。
//
// 为什么需要这一层: 订阅渠道的卖点是"建渠道不填调用信息"。要兑现它, 系统必须知道
// 每个平台的地址、方言、账号平台标识从哪来 —— 否则前端只能让用户手填, 手填就必然填错。
//
// 为什么是 Go 文件而不是 YAML/数据库: 用户明确接受"要新协议时写个 Go 文件"。
// 平台描述本身就是代码(它要引用方言常量、协议位、探测分支), 抽成数据格式只会
// 把编译期检查换成一个运行时校验器, 得不偿失。要加第三个平台, 复制本文件改字段即可。
package subplatform

import (
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// Platform 是一个订阅平台的完整描述。
//
// 字段刻意少而具体: 只放"建渠道时必须知道"的东西。价格、模型清单、额度这些
// 都从上游现取(见 op.ProbeChannelModels), 不在这里抄一份会过期的副本。
type Platform struct {
	// ID 是平台标识, 也是 API 与数据库里用的值(如 "gemini")。同一个 ID 必须唯一。
	ID string
	// Name 是界面展示名。
	Name string
	// Dialect 决定出站转换器; 订阅渠道一律为 DialectSubscription,
	// 具体平台的协议差异由 AccountPlatform 对应的转换逻辑处理。
	Dialect model.Dialect
	// AccountPlatform 是账号页与刷新任务用的平台标识(model.AccountPlatform 之一)。
	AccountPlatform string
	// BaseURL 是建渠道时自动填入的上游地址; 空表示该平台不需要地址(完全由代码内置)。
	BaseURL string
	// Description 是界面提示语, 说明这个平台需要什么前置条件(如代理、GCP Project)。
	Description string
	// Protocols 是该平台上游真正支持的协议位。
	//
	// 为什么必须由平台声明而不是在转发里按方言推断: 订阅方言是"入口类型", 底下可以挂
	// Gemini(冒号 RPC)、Claude(Anthropic Messages)、Codex(OpenAI Responses) 等互不相通的协议。
	// 转发只看到 Dialect=="subscription" 时无从知道该走哪一个, 猜错就是拼出不存在的端点(404)。
	// 平台自己声明, 转发按声明收敛, 加平台时这里跟着写, 不会漏。
	Protocols model.Protocol
}

// registrations 是已登记的平台。
//
// 加一个平台 = 往这里加一项 + 确认三件事: 方言可用、账号平台已登记、探测分支存在。
// 三者缺一, 建出来的渠道都会在上游调用时才失败 —— 所以下面有 Validate 在启动时挡。
var registrations = []Platform{
	{
		ID:              "gemini",
		Name:            "Gemini",
		Dialect:         model.DialectSubscription,
		AccountPlatform: model.AccountPlatformAntigravity,
		// daily 端点承载 onboardUser / fetchAvailableModels / generateContent;
		// loadCodeAssist 由 op.GeminiCLIBases 单独切到 prod 端点。
		BaseURL:     "https://daily-cloudcode-pa.googleapis.com",
		Description: "Google 订阅账号(Gemini/Antigravity)。国内需配置代理; GCP Project 可留空由上游自动分配。",
		Protocols:   model.ProtocolGeminiCLI,
	},
}

// All 返回全部已登记平台(按 ID 排序, 保证接口输出稳定)。
//
// 排序是为了让前端下拉顺序确定: map 遍历顺序随机会让下拉项每次刷新都变位置,
// 用户会以为自己的选项丢了。
func All() []Platform {
	out := append([]Platform(nil), registrations...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Of 按 ID 取平台描述; 未登记返回 false。
func Of(id string) (Platform, bool) {
	id = strings.TrimSpace(id)
	for _, p := range registrations {
		if p.ID == id {
			return p, true
		}
	}
	return Platform{}, false
}

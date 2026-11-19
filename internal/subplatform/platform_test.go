package subplatform

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestRegistrationsValid 钉住全部已登记平台的描述自洽。
//
// 这是"加平台"的守卫: 新增平台若漏了方言/账号平台, 这条会立刻失败,
// 而不是等用户建完渠道点登录才发现。
func TestRegistrationsValid(t *testing.T) {
	if problems := Validate(); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}

// TestGeminiPlatformMatchesExistingConstants 钉住 gemini 描述与既有常量一致。
//
// 平台描述若与探测/转发用的常量漂移, 建出来的渠道会指向错误端点 ——
// 这类错误在单测里看不出来, 只有在真实调用时才变成 404/403。
func TestGeminiPlatformMatchesExistingConstants(t *testing.T) {
	p, ok := Of("gemini")
	if !ok {
		t.Fatal("gemini 平台未登记")
	}
	if p.Dialect != model.DialectSubscription {
		t.Errorf("方言: got %q want %q", p.Dialect, model.DialectSubscription)
	}
	// 账号平台必须是 antigravity: gemini 的旧 client 已被 Google 停用(UNSUPPORTED_CLIENT),
	// 个人订阅账号只能走 antigravity 身份。写成 gemini 会让登录成功后数据面 403。
	if p.AccountPlatform != model.AccountPlatformAntigravity {
		t.Errorf("账号平台: got %q want %q", p.AccountPlatform, model.AccountPlatformAntigravity)
	}
	// 平台声明的协议位决定转发走哪条上游协议。声明缺失或认错协议,
	// 请求会被打到不存在的端点并得到 404 —— 而 404 指不出根因在声明上。
	if p.Protocols != model.ProtocolGeminiCLI {
		t.Errorf("协议位: got %d want %d(ProtocolGeminiCLI)", p.Protocols, model.ProtocolGeminiCLI)
	}
}

// TestProtocolsDeclaredForEveryPlatform 所有已登记平台都必须声明协议位。
//
// 空白声明不会被 Validate 之外的任何地方发现: 转发只会收敛成空协议、换下一个成员,
// 表现为"渠道有模型但永远不可用", 排查时完全看不到协议这一层。
func TestProtocolsDeclaredForEveryPlatform(t *testing.T) {
	for _, p := range All() {
		if p.Protocols == 0 {
			t.Errorf("平台 %q 未声明协议位", p.ID)
		}
	}
}

// TestAllSorted 接口输出的顺序必须稳定。
//
// 随机顺序会让前端下拉项每次刷新换位置, 用户会以为选项丢了。
func TestAllSorted(t *testing.T) {
	all := All()
	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Fatalf("未按 ID 排序: %q 在 %q 之前", all[i-1].ID, all[i].ID)
		}
	}
	if len(all) == 0 {
		t.Fatal("至少应登记一个平台")
	}
}

// TestOfUnknown 未登记的平台必须明确返回 false, 不能返回零值平台。
//
// 返回零值会让调用方拿空方言去建渠道, 错误要到转发时才暴露。
func TestOfUnknown(t *testing.T) {
	if _, ok := Of("not-a-platform"); ok {
		t.Fatal("未登记的平台应返回 false")
	}
	if _, ok := Of("  "); ok {
		t.Fatal("空白 ID 应返回 false")
	}
}

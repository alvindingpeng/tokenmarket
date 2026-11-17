package subplatform

import (
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// Validate 检查平台描述是否自洽, 并返回全部问题(而不是只报第一个)。
//
// 为什么在这里校验而不是让调用方自己看: 加平台时最容易漏的是两项 ——
// 方言是否可用于订阅渠道、账号平台是否已登记。它们都不会在编译期报错,
// 只会在用户建完渠道、点了登录之后才炸。一次把问题列全, 比修一个跑一次快。
func Validate() []error {
	var problems []error
	seen := map[string]bool{}
	for _, p := range registrations {
		if strings.TrimSpace(p.ID) == "" {
			problems = append(problems, fmt.Errorf("平台描述缺少 ID"))
			continue
		}
		if seen[p.ID] {
			problems = append(problems, fmt.Errorf("平台 ID %q 重复登记", p.ID))
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Name) == "" {
			problems = append(problems, fmt.Errorf("平台 %q 缺少展示名", p.ID))
		}
		if !p.Dialect.Subscribable() {
			problems = append(problems, fmt.Errorf("平台 %q 的方言 %q 不是订阅方言", p.ID, p.Dialect))
		}
		if p.Protocols == 0 {
			problems = append(problems, fmt.Errorf("平台 %q 未声明协议位: 转发无从判断该走哪条上游协议", p.ID))
		}
		if p.AccountPlatform == "" {
			problems = append(problems, fmt.Errorf("平台 %q 缺少账号平台标识", p.ID))
		} else if _, err := model.AccountOAuthSpecOf(p.AccountPlatform); err != nil {
			problems = append(problems, fmt.Errorf("平台 %q 的账号平台 %q 未登记到 accountOAuthSpecs: %w", p.ID, p.AccountPlatform, err))
		}
	}
	return problems
}

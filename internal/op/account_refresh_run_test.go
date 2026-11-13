
package op

import (
	"errors"
	"testing"
)

// 分类逻辑守的是"别把可重试错误当成账号已失效"。
// 误判方向的代价极不对称: 把网络抖动当失效 = 一次抖动永久摘掉一个有效账号;
// 把失效当可重试 = 每轮拿一个作废令牌打上游, 招风控且永不自愈。
// 所以这里逐条钉住两个方向的判定。

func TestAccountRefreshShouldInvalidate(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// 明确表示续期令牌作废: 必须判定失效, 停止重试并告警。
		{"invalid_grant", &AccountRefreshError{Status: 400, Body: `{"error":"invalid_grant"}`}, true},
		{"invalid_client", &AccountRefreshError{Status: 401, Body: `{"error":"invalid_client"}`}, true},
		{"refresh token expired", &AccountRefreshError{Status: 400, Body: "refresh token expired"}, true},
		{"unauthorized_client", &AccountRefreshError{Status: 400, Body: "unauthorized_client"}, true},

		// 不可判定的 4xx: 按可重试处理。宁可多试一轮, 也别误杀一个有效账号。
		{"400 无关键词", &AccountRefreshError{Status: 400, Body: "bad request"}, false},
		{"429 限流", &AccountRefreshError{Status: 429, Body: "too many requests"}, false},
		{"503 上游故障", &AccountRefreshError{Status: 503, Body: "service unavailable"}, false},

		// 网络错误与上下文取消: 都是可重试的。
		{"网络错误", errors.New("dial tcp: connection refused"), false},

		// 回写失败绝不标记失效: 令牌其实换到了, 只是没存下来,
		// 这时该做的是救人(从上游/log 里救回新令牌), 不是把账号判死。
		{"回写失败", &AccountRefreshPersistError{ChannelKeyID: 1, Err: errors.New("db locked")}, false},
	}
	for _, testCase := range cases {
		if got := accountRefreshShouldInvalidate(testCase.err); got != testCase.want {
			t.Fatalf("%s: got %v want %v", testCase.name, got, testCase.want)
		}
	}
}

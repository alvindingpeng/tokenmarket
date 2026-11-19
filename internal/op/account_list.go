package op

import (
	"sort"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// AccountList 返回当前作用域可见的全部上游账号, 供账号管理面展示与运维排查。
//
// 只遍历缓存不查库: 账号的状态(到期时间、最近刷新、熔断)全部随凭据一起在 channelKeyCache 里,
// 定时任务刷新时也会同步写回缓存。list 每次都去查库反而会与转发路径实际使用的值不一致 ——
// 列表说"还剩 2 小时", 转发拿到的却是另一个值, 那种不一致比性能更贵。
//
// 返回体刻意不含 Key 明文(见 model.AccountListEntry 的说明)。
func AccountList(scope model.Scope) []model.AccountListEntry {
	// 渠道名要显示归属, 但 ChannelKey 本身不带渠道名 —— 它只有 ChannelID。
	// 先建一份 ID->名称的索引, 避免在内层循环里每次去 channelCache 找。
	channelNames := make(map[int]string, channelCache.Len())
	for _, channel := range channelCache.GetAll() {
		channelNames[channel.ID] = channel.Name
	}
	accounts := make([]model.AccountListEntry, 0, len(channelKeyCache.GetAll()))
	for _, channelKey := range channelKeyCache.GetAll() {
		// 非账号凭据不进这个列表: 那是普通静态 Key, 没有到期与续期的概念,
		// 混进来只会让"账号"这个词失去意义。
		if channelKey.AccountKind == "" {
			continue
		}
		// 归属过滤与渠道列表一致: 管理员看全部, 其余只看自有渠道。
		// 这里不能只按 ChannelKey.ChannelID 判断 —— 凭据的归属由渠道的 UserID 决定。
		channel, ok := channelCache.Get(channelKey.ChannelID)
		if !ok {
			// 渠道已不存在但凭据还在: 属于数据不一致, 不该出现在任何人的列表里。
			continue
		}
		if !scope.Owns(channel.UserID) {
			continue
		}
		accounts = append(accounts, model.AccountListEntry{
			ChannelKeyID:   channelKey.ID,
			ChannelID:      channelKey.ChannelID,
			ChannelName:    channelNames[channelKey.ChannelID],
			Name:           channelKey.Name,
			Enabled:        channelKey.Enabled,
			Kind:           channelKey.AccountKind,
			Platform:       channelKey.AccountPlatform,
			Email:          channelKey.AccountEmail,
			TokenExpiresAt: channelKey.TokenExpiresAt,
			TokenRefreshAt: channelKey.TokenRefreshAt,
			DisabledUntil:  channelKey.DisabledUntil,
			BreakerTrips:   channelKey.BreakerTrips,
			LastErrorAt:    channelKey.LastErrorAt,
		})
	}
	// 按"最需要关注"的顺序排: 已失效与将到期排在前面, 正常账号沉底。
	// 账号管理面的用途就是找问题账号, 让人自己在一长串"正常"里翻找是把用途弄反了。
	// 同一紧急程度内按渠道名与凭据名排, 保证顺序稳定(缓存遍历顺序不保证)。
	now := time.Now()
	sort.Slice(accounts, func(i, j int) bool {
		left, right := accountUrgency(accounts[i], now), accountUrgency(accounts[j], now)
		if left != right {
			return left < right
		}
		if accounts[i].ChannelName != accounts[j].ChannelName {
			return accounts[i].ChannelName < accounts[j].ChannelName
		}
		return accounts[i].Name < accounts[j].Name
	})
	return accounts
}

// accountUrgency 给出排序键: 数值越小越靠前。
//
// 判据与前端 accountState.ts 的四态保持一致(失效 > 需人工 > 将到期 > 正常),
// 但这里不直接复用那套判断: 后端只做排序, 真正的状态由界面推导 ——
// 界面拿得到 now, 而这里的"现在"从请求发出到渲染之间已经过去了。
func accountUrgency(account model.AccountListEntry, now time.Time) int {
	if account.DisabledUntil != nil && account.DisabledUntil.After(now) {
		return 0 // 熔断中: 此刻转发必然失败。
	}
	if account.TokenExpiresAt != nil && !account.TokenExpiresAt.After(now) {
		return 1 // 已过期。
	}
	if account.Kind == model.AccountKindCookie {
		return 2 // cookie 类无法自动续期, 迟早需要人工。
	}
	if account.TokenExpiresAt != nil && account.TokenExpiresAt.Sub(now) <= 24*time.Hour {
		return 3 // 将到期。
	}
	return 4
}

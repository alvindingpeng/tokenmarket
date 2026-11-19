package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// RecordChannelQuotaError immediately pauses every key/model/grant of a channel
// after explicit upstream balance/quota exhaustion, without a failure threshold.
// Only the channel deadline is persisted; Enabled and child breakers are untouched.
// Recovery is by deadline expiry, never by a successful single-model probe.
func RecordChannelQuotaError(channelID int) error {
	channelBreakerWriteMu.Lock()
	defer channelBreakerWriteMu.Unlock()
	minutes := breakerKeyHardMinutes()
	// Zero is accepted by generic breaker settings, but an explicit exhausted
	// balance must still pause the channel for a meaningful interval.
	if minutes <= 0 {
		minutes = defaultBreakerKeyHardMinutes
	}
	until := time.Now().Add(time.Duration(minutes) * time.Minute)
	var channel model.Channel
	if err := db.GetDB().First(&channel, channelID).Error; err != nil {
		return fmt.Errorf("load quota-exhausted channel: %w", err)
	}
	// In-flight failures and setting changes must not shorten an existing pause.
	result := db.GetDB().Model(&model.Channel{}).Where("id = ? AND (disabled_until IS NULL OR disabled_until < ?)", channelID, until).Update("disabled_until", until)
	if result.Error != nil {
		return fmt.Errorf("persist channel quota pause: %w", result.Error)
	}
	if channel.DisabledUntil != nil && channel.DisabledUntil.After(until) {
		until = *channel.DisabledUntil
	}
	channelStatsNeedUpdateLock.Lock()
	defer channelStatsNeedUpdateLock.Unlock()
	if cached, ok := channelCache.Get(channelID); ok {
		cached.DisabledUntil = &until
		channelCache.Set(channelID, cached)
	} else {
		channel.DisabledUntil = &until
		channelCache.Set(channelID, channel)
	}
	return nil
}

// setChannelCachePreservingQuota keeps unrelated configuration writers from
// overwriting a quota pause with a snapshot obtained before the quota error.
func setChannelCachePreservingQuota(channel model.Channel) {
	channelStatsNeedUpdateLock.Lock()
	defer channelStatsNeedUpdateLock.Unlock()
	if cached, ok := channelCache.Get(channel.ID); ok && cached.DisabledUntil != nil {
		if channel.DisabledUntil == nil || cached.DisabledUntil.After(*channel.DisabledUntil) {
			channel.DisabledUntil = cached.DisabledUntil
		}
	}
	channelCache.Set(channel.ID, channel)
}

// ChannelQuotaPaused uses the live shared cache; expiry requires no sweep.
func ChannelQuotaPaused(channelID int) bool {
	channel, ok := channelCache.Get(channelID)
	return ok && isCircuitBroken(channel.DisabledUntil)
}

// ChannelQuotaResume 提前解除渠道级额度暂停, 供归属方在上游补足额度后立刻恢复。
// 只清 channels.disabled_until 这一列: Enabled 是人工开关, 凭据与模型级熔断各自代表一项未修好的故障,
// 顺手一起清会把"我没关过这个渠道"变成既成事实, 也掩盖掉真正的单项问题。
// 返回是否真的解除了一次尚未到期的暂停; 没暂停或已过期返回 false 且不报错 ——
// 重复点击不该报错, 回执里的条数也只统计实际生效的解锁。
func ChannelQuotaResume(channelID int, scope model.Scope, ctx context.Context) (bool, error) {
	channel, ok := channelCache.Get(channelID)
	// 越权一律按不存在处理: 与 ChannelBreakerReset 同口径, 不暴露渠道是否存在。
	if !ok || !scope.Owns(channel.UserID) {
		return false, fmt.Errorf("channel not found")
	}
	resumed, err := clearChannelQuotaPause(channelID, ctx)
	if err != nil || !resumed {
		return resumed, err
	}
	// 分组成员按实时渠道状态判可用, 渠道缓存恢复后重建一次分组快照,
	// 界面才能立刻看到成员重新可用, 不必等下一次渠道变更顺带刷新。
	if err := groupRefreshCache(ctx); err != nil {
		return resumed, err
	}
	return resumed, nil
}

// clearChannelQuotaPause 在写锁内清空暂停列并同步缓存, 返回是否真解除了一次尚未到期的暂停。
// 写锁只护住判定与落库: 分组重建要另读库, 放在锁里会无谓延长转发侧的等待。
func clearChannelQuotaPause(channelID int, ctx context.Context) (bool, error) {
	channelBreakerWriteMu.Lock()
	defer channelBreakerWriteMu.Unlock()
	cached, ok := channelCache.Get(channelID)
	// 以缓存判活: 转发与展示都以缓存为准, 它说没暂停就没有需要解除的东西。
	if !ok || !isCircuitBroken(cached.DisabledUntil) {
		return false, nil
	}
	// 落库条件按库内时间再判一次活: 缓存可能慢一拍, 暂停也可能正好在两次判断之间到期。
	result := db.GetDB().WithContext(ctx).Model(&model.Channel{}).
		Where("id = ? AND disabled_until > ?", channelID, time.Now()).
		Updates(map[string]any{"disabled_until": nil})
	if result.Error != nil {
		return false, fmt.Errorf("clear channel quota pause: %w", result.Error)
	}
	// 已到期或被并发解除: 本次没有解除任何东西, 不计入条数。
	if result.RowsAffected == 0 {
		return false, nil
	}
	channelStatsNeedUpdateLock.Lock()
	defer channelStatsNeedUpdateLock.Unlock()
	// 重新取一次: 配置写入只拿这把锁, 期间可能已替换过缓存条目。
	if latest, ok := channelCache.Get(channelID); ok {
		latest.DisabledUntil = nil
		channelCache.Set(channelID, latest)
	}
	return true, nil
}

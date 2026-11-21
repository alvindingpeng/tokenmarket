package op

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 告警静默窗口: 计划内维护期间告警照常落库但不外发。
//
// 判定放在 RaiseAlertFacts 的外发决策点而不是告警产生点:
// 落库永远发生(故障记录不能丢), 只把"发出去"这一步按窗口跳过。
// 窗口数量很少(个位数), 每次告警时查一次全表再内存判定, 不值得为它建缓存。

// alertSilenceMaxWindow 单窗口最长 72 小时: 静默是维护工具不是常关方式,
// 真要长期关某类告警应该去调 alert_channels / 去重窗口, 而不是开一个无限静默。
const alertSilenceMaxWindow = 72 * time.Hour

// AlertKindFxRateRefreshFailed 牌价源连续采集失败的告警类型。
//
// 收口成常量而不在各处写字符串字面量: 这个 kind 曾长期"发得出但登记不全" ——
// fxrate.go 发了它, 闭集与 determineAlertEvent 都没有, 于是静默窗口建不了(400)、
// 外发事件名掉成 system_alert。用同一符号能让"发出点"与"登记点"在编译器里绑在一起,
// 下次再漏登记时至少改常量这一处会在 diff 里显形。
const AlertKindFxRateRefreshFailed = "fx_rate_refresh_failed"

// 下面五条是同一类历史缺陷的第二批: 它们都由真实告警点发出, 却漏登记在闭集里。
// 后果与 fx_rate_refresh_failed 完全一样 —— 管理员为它们建静默窗口直接 400,
// 外发事件名掉成 system_alert(消费端分不出这是计费口径缺失还是别的系统杂项)。
// 每条都用常量把"发出点"与"登记点"绑在编译器上: 再漏一处就编译不过, 而不是运行时静默降级。
const (
	// AlertKindUsageMissing 成功请求却没拿到用量回传: 上游不回用量等于免费调用, 与计费正确性强相关。
	AlertKindUsageMissing = "usage_missing"
	// AlertKindAccountInvalidated 上游账号续期令牌失效, 需要重新导入(可修复, 不修复则该账号悄悄退出轮巡)。
	AlertKindAccountInvalidated = "account_invalidated"
	// AlertKindBillingArchiveFailed 资金表归档失败: 归档与清理绑定, 失败时那批明细没落地。
	AlertKindBillingArchiveFailed = "billing_archive_failed"
	// AlertKindAuditArchiveFailed 审计表归档失败: 合规归档未推进, 同理不能静默降级。
	AlertKindAuditArchiveFailed = "audit_archive_failed"
	// AlertKindFxRateStale 牌价采集失败且快照已过陈旧阈值: fx_rate_refresh_failed 的"已陈旧"版本,
	// 前者说采集挂了, 这条说"折算已经在用旧价", 处置动作不同。
	AlertKindFxRateStale = "fx_rate_stale"
)

// AlertValidKind 判断告警类型是否受支持, 闭集与 determineAlertEvent 的 case 清单一致
// (snapshot_test 之外再加一条一致性测试钉住两处清单, 防止加新 kind 时只改一处)。
// kind 为空表示"静默全部", 由调用方在创建时放行, 不走这个闭集。
func AlertValidKind(kind string) bool {
	switch kind {
	case "down", "recovered", "degraded", "key_disabled", "model_disabled",
		"low_balance", "quota_exceeded", "mismatch", "billing_failed",
		"backup_failed", "archive_failed", "watchdog_alert",
		"route_persist_failed", "route_persist_recovered",
		"task_failed", "task_recovered",
		"key_degraded", "model_degraded", "payment_amount_mismatch", "reserve",
		"input_tokens", "output_tokens", "requests", "update_available", "test",
		"withdrawal_approve_timeout", "payment_after_expiry",
		// 牌价采集连续失败: fxrate.go 一直在发它, 但此前漏登记 —— 管理员为它建静默窗口时
		// 创建直接被拒(400), 而牌价停更恰恰是最需要"维护期间先别刷屏"的那一类。
		AlertKindFxRateRefreshFailed,
		// 第二批同类缺陷: 都是真实发出但此前漏登记的类型(见上方常量注释)。
		AlertKindUsageMissing, AlertKindAccountInvalidated,
		AlertKindBillingArchiveFailed, AlertKindAuditArchiveFailed,
		AlertKindFxRateStale:
		return true
	}
	return false
}

// AlertSilenced 判定此刻给定的告警类型是否处于静默窗口内。
// DB 错误按"不静默"处理: 静默是运维便利, 不能因为它的故障反而吞掉告警外发。
func AlertSilenced(ctx context.Context, kind string) bool {
	var windows []model.AlertSilenceWindow
	if err := db.GetDB().WithContext(ctx).
		Where("end_at > ?", time.Now()).
		Find(&windows).Error; err != nil {
		return false
	}
	now := time.Now()
	for _, w := range windows {
		if w.Kind != "" && w.Kind != kind {
			continue
		}
		if w.AlertSilenceActive(now) {
			return true
		}
	}
	return false
}

// AlertSilenceList 返回全部窗口(含已过期的, 供回看); 新的在前。
func AlertSilenceList(ctx context.Context) ([]model.AlertSilenceWindow, error) {
	var windows []model.AlertSilenceWindow
	err := db.GetDB().WithContext(ctx).Order("start_at DESC").Find(&windows).Error
	return windows, err
}

// AlertSilenceCreate 新建静默窗口。
// 校验放在这里而不是 handler: 任何未来入口(导入/脚本)都绕不过同一套约束。
func AlertSilenceCreate(ctx context.Context, input model.AlertSilenceWindowInput, createdBy uint) (model.AlertSilenceWindow, error) {
	start, err := time.Parse(time.RFC3339, input.StartAt)
	if err != nil {
		return model.AlertSilenceWindow{}, fmt.Errorf("invalid start_at: %w", err)
	}
	end, err := time.Parse(time.RFC3339, input.EndAt)
	if err != nil {
		return model.AlertSilenceWindow{}, fmt.Errorf("invalid end_at: %w", err)
	}
	if !end.After(start) {
		return model.AlertSilenceWindow{}, errors.New("end_at must be after start_at")
	}
	if end.Sub(start) > alertSilenceMaxWindow {
		return model.AlertSilenceWindow{}, fmt.Errorf("silence window longer than %s; use shorter windows", alertSilenceMaxWindow)
	}
	if input.Kind != "" && !AlertValidKind(input.Kind) {
		return model.AlertSilenceWindow{}, fmt.Errorf("unknown alert kind %q; leave empty to silence all kinds", input.Kind)
	}
	if len(input.Note) > 200 {
		return model.AlertSilenceWindow{}, errors.New("note too long (max 200 bytes)")
	}
	// 并发的活动窗口数上限: 防止把静默当全局开关堆叠。
	var active int64
	if err := db.GetDB().WithContext(ctx).Model(&model.AlertSilenceWindow{}).
		Where("end_at > ?", time.Now()).Count(&active).Error; err != nil {
		return model.AlertSilenceWindow{}, err
	}
	if active >= model.AlertSilenceMaxActive {
		return model.AlertSilenceWindow{}, fmt.Errorf("too many active silence windows (max %d)", model.AlertSilenceMaxActive)
	}
	window := model.AlertSilenceWindow{
		StartAt:   start,
		EndAt:     end,
		Kind:      input.Kind,
		Note:      input.Note,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
	}
	if err := db.GetDB().WithContext(ctx).Create(&window).Error; err != nil {
		return model.AlertSilenceWindow{}, err
	}
	return window, nil
}

// AlertSilenceDelete 删除窗口(取消静默)。删除不存在的窗口不算错误 —— 幂等,
// 管理员双击或两个标签页同时删不应报错。
func AlertSilenceDelete(ctx context.Context, id uint) error {
	return db.GetDB().WithContext(ctx).Delete(&model.AlertSilenceWindow{}, id).Error
}

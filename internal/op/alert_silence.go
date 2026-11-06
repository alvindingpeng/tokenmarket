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
		"input_tokens", "output_tokens", "requests", "update_available", "test":
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

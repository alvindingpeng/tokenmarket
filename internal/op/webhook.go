package op

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/netguard"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm"
)

// Webhook 订阅管理(P0-5): 用户隔离 CRUD、测试发送、投递日志。
//
// 三条硬边界:
//  1. 密钥只进不出: 创建时必给、更新留空保留, 库里是 secretstore 信封密文,
//     任何返回路径都只有 has_secret。
//  2. URL 只收公网 HTTPS 443: 保存时 netguard.CheckHost 全量解析校验,
//     拨号时 DialGuard 再按解析结果复核(DNS rebinding 最终防线)。
//  3. 越权即 404: 所有查询都带 user_id 条件, 不区分"不存在"与"不是你的"。

var (
	ErrWebhookNotFound        = errors.New("webhook not found")
	ErrWebhookURLInvalid      = errors.New("invalid webhook url")
	ErrWebhookEventsInvalid   = errors.New("invalid webhook events")
	ErrWebhookSecretInvalid   = errors.New("invalid webhook secret")
	ErrWebhookRetryInvalid    = errors.New("invalid webhook retry_count")
	ErrWebhookQuota           = errors.New("webhook quota exceeded")
	ErrWebhookTestRateLimited = errors.New("webhook test rate limited")
)

// webhookValidateURL 校验订阅地址: 仅 https、仅 443(默认或显式)、禁 userinfo/fragment、
// 禁私网/环回/链路本地/元数据地址。查询串允许(URL 里的 token 属于用户配置, 但绝不进日志)。
func webhookValidateURL(ctx context.Context, raw string) error {
	if raw == "" || len(raw) > model.WebhookURLMaxLen {
		return ErrWebhookURLInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ErrWebhookURLInvalid
	}
	if parsed.Scheme != "https" || parsed.Opaque != "" {
		return ErrWebhookURLInvalid
	}
	if parsed.User != nil {
		return ErrWebhookURLInvalid
	}
	if parsed.Fragment != "" {
		return ErrWebhookURLInvalid
	}
	host := parsed.Hostname()
	if host == "" {
		return ErrWebhookURLInvalid
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return ErrWebhookURLInvalid
	}
	// netguard.CheckHost: 解析全部地址, 任一私网即拒; 解析失败放行保存, 拨号层再复核。
	if err := netguard.CheckHost(ctx, host, false); err != nil {
		return ErrWebhookURLInvalid
	}
	return nil
}

// webhookValidateEvents 收敛订阅事件: 闭集校验 + 去重 + 数量上限。
func webhookValidateEvents(events []string) ([]string, error) {
	if len(events) == 0 || len(events) > model.WebhookMaxEvents {
		return nil, ErrWebhookEventsInvalid
	}
	seen := make(map[string]struct{}, len(events))
	unique := make([]string, 0, len(events))
	for _, event := range events {
		if !model.WebhookValidEvent(event) {
			return nil, ErrWebhookEventsInvalid
		}
		if _, dup := seen[event]; dup {
			continue
		}
		seen[event] = struct{}{}
		unique = append(unique, event)
	}
	return unique, nil
}

// webhookValidateSecret 校验签名密钥长度(按 UTF-8 字节数, 16..128)。
func webhookValidateSecret(secret string) (string, error) {
	size := len([]byte(secret))
	if size < model.WebhookSecretMinBytes || size > model.WebhookSecretMaxBytes {
		return "", ErrWebhookSecretInvalid
	}
	return secret, nil
}

// webhookSealSecret 明文密钥 -> 信封密文, AAD 绑定 webhook 行归属(secretstore.WebhookSecretBinding)。
func webhookSealSecret(webhookID uint64, userID uint, secret string) (string, error) {
	return model.SealSecretValue(secretstore.WebhookSecretBinding(webhookID, userID), secret)
}

// webhookOpenSecret 信封密文 -> 明文; 归属不符或主密钥不对一律报错, 不降级。
func webhookOpenSecret(hook model.Webhook) (string, error) {
	if hook.SecretEncrypted == "" {
		return "", nil
	}
	return model.OpenSecretValue(secretstore.WebhookSecretBinding(hook.ID, hook.UserID), hook.SecretEncrypted)
}

// webhookToView 组装 API 视图: 事件解码失败即报错(存量损坏行不静默投空), 密钥只出 has_secret。
func webhookToView(hook model.Webhook) (model.WebhookView, error) {
	events, err := model.WebhookDecodeEvents(hook.Events)
	if err != nil {
		return model.WebhookView{}, err
	}
	last := hook.LastStatus
	if last == "" {
		last = model.WebhookLastStatusNone
	}
	return model.WebhookView{
		ID:         hook.ID,
		URL:        hook.URL,
		Events:     events,
		Enabled:    hook.Enabled,
		RetryCount: hook.RetryCount,
		HasSecret:  hook.SecretEncrypted != "",
		CreatedAt:  hook.CreatedAt,
		UpdatedAt:  hook.UpdatedAt,
		LastStatus: last,
	}, nil
}

// WebhookCreate 创建订阅。密钥在此封存入库, 明文不落任何日志与返回值。
func WebhookCreate(ctx context.Context, userID uint, req model.WebhookCreateRequest) (model.WebhookView, error) {
	if err := webhookValidateURL(ctx, req.URL); err != nil {
		return model.WebhookView{}, err
	}
	events, err := webhookValidateEvents(req.Events)
	if err != nil {
		return model.WebhookView{}, err
	}
	secret, err := webhookValidateSecret(req.Secret)
	if err != nil {
		return model.WebhookView{}, err
	}
	retryCount := 2
	if req.RetryCount != nil {
		retryCount = *req.RetryCount
		if retryCount < 0 || retryCount > model.WebhookMaxRetryCount {
			return model.WebhookView{}, ErrWebhookRetryInvalid // 由 binding 挡, 这里兜底
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	encoded, err := model.WebhookEncodeEvents(events)
	if err != nil {
		return model.WebhookView{}, err
	}

	var view model.WebhookView
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.Webhook{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return fmt.Errorf("count webhooks: %w", err)
		}
		if count >= model.WebhookMaxPerUser {
			return ErrWebhookQuota
		}
		hook := model.Webhook{
			UserID:     userID,
			URL:        req.URL,
			Events:     encoded,
			Enabled:    enabled,
			RetryCount: retryCount,
			Version:    1,
			LastStatus: model.WebhookLastStatusNone,
		}
		// 显式列插入: retry_count=0 / enabled=false 是合法零值,
		// 不带 Select 的 Create 会跳过零值列并落成 DB default(retry=2 / enabled=true)。
		if err := tx.Select("UserID", "URL", "Events", "Enabled", "RetryCount", "Version", "LastStatus").
			Create(&hook).Error; err != nil {
			return fmt.Errorf("create webhook: %w", err)
		}
		// 密钥封存要行 ID 做归属绑定, 因此先建行再回填密文; 同一事务内完成, 中途失败整单回滚。
		sealed, err := webhookSealSecret(hook.ID, userID, secret)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.Webhook{}).Where("id = ?", hook.ID).
			Update("secret_encrypted", sealed).Error; err != nil {
			return fmt.Errorf("store webhook secret: %w", err)
		}
		hook.SecretEncrypted = sealed
		view, err = webhookToView(hook)
		return err
	})
	if err != nil {
		return model.WebhookView{}, err
	}
	return view, nil
}

// WebhookList 分页列出当前用户的订阅。
func WebhookList(ctx context.Context, userID uint, req model.WebhookListRequest) (model.WebhookPage, error) {
	page, pageSize := webhookNormalizePage(req)
	query := db.GetDB().WithContext(ctx).Model(&model.Webhook{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return model.WebhookPage{}, fmt.Errorf("count webhooks: %w", err)
	}
	var hooks []model.Webhook
	if err := query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&hooks).Error; err != nil {
		return model.WebhookPage{}, fmt.Errorf("list webhooks: %w", err)
	}
	items := make([]model.WebhookView, 0, len(hooks))
	for _, hook := range hooks {
		view, err := webhookToView(hook)
		if err != nil {
			return model.WebhookPage{}, err
		}
		items = append(items, view)
	}
	return model.WebhookPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// WebhookGet 取单条订阅(用户隔离)。
func WebhookGet(ctx context.Context, userID uint, id uint64) (model.WebhookView, error) {
	hook, err := webhookGetRow(ctx, nil, userID, id)
	if err != nil {
		return model.WebhookView{}, err
	}
	return webhookToView(hook)
}

// webhookGetRow 读一行订阅; 不存在或不属于该用户一律 ErrWebhookNotFound(越权即 404)。
func webhookGetRow(ctx context.Context, tx *gorm.DB, userID uint, id uint64) (model.Webhook, error) {
	conn := tx
	if conn == nil {
		conn = db.GetDB().WithContext(ctx)
	}
	var hook model.Webhook
	err := conn.Where("id = ? AND user_id = ?", id, userID).First(&hook).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Webhook{}, ErrWebhookNotFound
	}
	if err != nil {
		return model.Webhook{}, fmt.Errorf("get webhook: %w", err)
	}
	return hook, nil
}

// WebhookUpdate 部分更新。Secret 留空/省略即保留原密钥; 改 URL 递增版本并作废旧地址的未完成投递;
// 置为禁用即取消全部未完成投递 —— 旧投递永远不会发往旧 URL。
func WebhookUpdate(ctx context.Context, userID uint, id uint64, req model.WebhookUpdateRequest) (model.WebhookView, error) {
	// 事务外先做纯校验, 非法输入不碰库。
	var newURL string
	if req.URL != nil {
		newURL = *req.URL
		if err := webhookValidateURL(ctx, newURL); err != nil {
			return model.WebhookView{}, err
		}
	}
	var newEvents string
	if req.Events != nil {
		events, err := webhookValidateEvents(*req.Events)
		if err != nil {
			return model.WebhookView{}, err
		}
		newEvents, err = model.WebhookEncodeEvents(events)
		if err != nil {
			return model.WebhookView{}, err
		}
	}
	newSecret := ""
	if req.Secret != "" {
		secret, err := webhookValidateSecret(req.Secret)
		if err != nil {
			return model.WebhookView{}, err
		}
		newSecret = secret
	}
	if req.RetryCount != nil && (*req.RetryCount < 0 || *req.RetryCount > model.WebhookMaxRetryCount) {
		return model.WebhookView{}, ErrWebhookRetryInvalid
	}

	var view model.WebhookView
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		hook, err := webhookGetRow(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		changes := map[string]interface{}{}
		urlChanged := false
		if req.URL != nil && *req.URL != hook.URL {
			changes["url"] = newURL
			changes["version"] = hook.Version + 1
			urlChanged = true
		}
		if req.Events != nil {
			changes["events"] = newEvents
		}
		if req.Enabled != nil {
			changes["enabled"] = *req.Enabled
		}
		if req.RetryCount != nil {
			changes["retry_count"] = *req.RetryCount
		}
		if newSecret != "" {
			sealed, err := webhookSealSecret(hook.ID, userID, newSecret)
			if err != nil {
				return err
			}
			changes["secret_encrypted"] = sealed
		}
		if len(changes) > 0 {
			if err := tx.Model(&model.Webhook{}).Where("id = ?", hook.ID).Updates(changes).Error; err != nil {
				return fmt.Errorf("update webhook: %w", err)
			}
		}
		// URL 改绑或禁用: 未完成投递(含已被 lease 抢占的)一律取消, 挡在"发往旧地址"之前。
		// 版本检查是第二道闸: 投递前还会比对 HookVersion, 已被抢走又晚到的取消也拦得住。
		disabled := req.Enabled != nil && !*req.Enabled
		if urlChanged || disabled {
			if err := webhookCancelPendingTx(tx, hook.ID); err != nil {
				return err
			}
		}
		updated, err := webhookGetRow(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		view, err = webhookToView(updated)
		return err
	})
	if err != nil {
		return model.WebhookView{}, err
	}
	return view, nil
}

// webhookCancelPendingTx 取消一个订阅的全部未完成投递。
func webhookCancelPendingTx(tx *gorm.DB, webhookID uint64) error {
	if err := tx.Model(&model.WebhookDelivery{}).
		Where("webhook_id = ? AND status IN ?", webhookID,
			[]string{model.WebhookDeliveryStatusPending, model.WebhookDeliveryStatusProcessing}).
		Updates(map[string]interface{}{
			"status":           model.WebhookDeliveryStatusCancelled,
			"lease_expires_at": nil,
		}).Error; err != nil {
		return fmt.Errorf("cancel webhook deliveries: %w", err)
	}
	return nil
}

// WebhookDelete 删除订阅并清空其队列与日志(取消未完成投递, 已投递记录一并删除)。
func WebhookDelete(ctx context.Context, userID uint, id uint64) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&model.Webhook{})
		if result.Error != nil {
			return fmt.Errorf("delete webhook: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrWebhookNotFound
		}
		if err := tx.Where("webhook_id = ?", id).Delete(&model.WebhookDelivery{}).Error; err != nil {
			return fmt.Errorf("delete webhook deliveries: %w", err)
		}
		return nil
	})
}

// WebhookTest 测试发送: 入队一条 event=test 的投递, 每订阅至少间隔 60 秒。
// 返回只代表已入队, 不代表远端已收到(结果看日志)。频率限制用行级 CAS, 并发连点只放行一次。
func WebhookTest(ctx context.Context, userID uint, id uint64) (model.WebhookTestResult, error) {
	var result model.WebhookTestResult
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		hook, err := webhookGetRow(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		now := time.Now()
		cutoff := now.Add(-model.WebhookTestMinInterval)
		claim := tx.Model(&model.Webhook{}).
			Where("id = ? AND user_id = ? AND (last_test_at IS NULL OR last_test_at <= ?)", id, userID, cutoff).
			Update("last_test_at", now)
		if claim.Error != nil {
			return fmt.Errorf("claim webhook test slot: %w", claim.Error)
		}
		if claim.RowsAffected == 0 {
			return ErrWebhookTestRateLimited
		}
		deliveryID, err := webhookEnqueueTx(tx, hook, model.WebhookEventTest, 0, model.WebhookPayloadData{
			Title:   "Webhook 测试",
			Content: "这是一条 Webhook 测试投递, 收到即表示连通正常。",
		})
		if err != nil {
			return err
		}
		result = model.WebhookTestResult{DeliveryID: deliveryID, Status: model.WebhookDeliveryStatusPending}
		return nil
	})
	if err != nil {
		return model.WebhookTestResult{}, err
	}
	return result, nil
}

// WebhookLogList 分页列出一个订阅的投递日志(用户隔离, 不含响应正文与 URL)。
func WebhookLogList(ctx context.Context, userID uint, id uint64, req model.WebhookListRequest) (model.WebhookLogPage, error) {
	page, pageSize := webhookNormalizePage(req)
	if _, err := webhookGetRow(ctx, nil, userID, id); err != nil {
		return model.WebhookLogPage{}, err
	}
	query := db.GetDB().WithContext(ctx).Model(&model.WebhookDelivery{}).
		Where("webhook_id = ? AND user_id = ?", id, userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return model.WebhookLogPage{}, fmt.Errorf("count webhook deliveries: %w", err)
	}
	var deliveries []model.WebhookDelivery
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&deliveries).Error; err != nil {
		return model.WebhookLogPage{}, fmt.Errorf("list webhook deliveries: %w", err)
	}
	items := make([]model.WebhookDeliveryLogView, 0, len(deliveries))
	for _, d := range deliveries {
		items = append(items, model.WebhookDeliveryLogView{
			DeliveryID:   d.DeliveryID,
			Event:        d.Event,
			Status:       d.Status,
			Attempts:     d.Attempts,
			ResponseCode: d.LastStatusCode,
			DurationMs:   d.DurationMs,
			Error:        d.LastError,
			CreatedAt:    d.CreatedAt,
			UpdatedAt:    d.UpdatedAt,
		})
	}
	return model.WebhookLogPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// webhookNormalizePage 分页参数兜底: 缺省 1/20, 上限 100(与通知列表同口径)。
func webhookNormalizePage(req model.WebhookListRequest) (int, int) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

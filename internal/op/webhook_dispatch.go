package op

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/netguard"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// Webhook 投递引擎(P0-5): 持久化队列 + 原子 lease 抢占 + 有界重试。
//
// 交付语义是至少一次: 入队时生成稳定 DeliveryID(重试间不变, 接收端按它去重),
// 通知写入与入队同事务 —— 通知落了库, 对应投递一定在队里; 入队失败则通知写入整体回滚,
// 绝不"用户收到了通知、订阅方永远等不到"。
//
// worker 每轮批量有界处理, 抢占走条件更新(乐观锁), 多 worker 并发不会对同一条投递重复发网络请求;
// 崩溃遗留的 processing 由 lease 过期回收, 重新抢占后按至少一次语义补发。

const (
	// WebhookDispatchBatch 单轮批量上限。
	WebhookDispatchBatch = 20
	// WebhookDispatchConcurrency 单轮内的并发投递数。
	WebhookDispatchConcurrency = 4
	// WebhookLeaseDuration 抢占租约: 覆盖单次网络尝试(5s 超时)+ 收尾写库, 过期即视为 worker 已死。
	WebhookLeaseDuration = time.Minute
	// WebhookRetryBaseDelay / WebhookRetryMaxDelay 指数退避: 30s 起倍增, 封顶 10 分钟。
	WebhookRetryBaseDelay = 30 * time.Second
	WebhookRetryMaxDelay  = 10 * time.Minute
	// WebhookResponseReadLimit 响应最多读这么些字节即丢弃: 只为驱动连接复用/完整读取,
	// 正文既不入库也不入日志(防泄露与 SSRF 回显)。
	WebhookResponseReadLimit = 4096
	// WebhookLogRetention / WebhookLogMaxPerHook / WebhookPendingMaxPerHook 容量控制:
	// 终态日志保留 7 天; 每订阅终态日志最多留最近 500 条; 非终态积压最多 1000 条(超出取消最旧)。
	WebhookLogRetention      = 7 * 24 * time.Hour
	WebhookLogMaxPerHook     = 500
	WebhookPendingMaxPerHook = 1000
)

var (
	// webhookHTTPTimeout 单次投递的总超时(含连接、TLS、写请求、读响应头)。
	// 测试会临时改小以加速; 它不是防护开关, 生产无任何配置入口改写。
	webhookHTTPTimeout = 5 * time.Second
	// webhookDialAllowPrivate 拨号层"放行私网目标"的判定。生产就是下面这个显式 false 字面量。
	//
	// 必须显式 func() bool { return false }, 不能图省事传 nil: netguard.DialGuard 对
	// AllowPrivate == nil 的语义是「放行」(管理员例外开关), 传 nil 等于把内网大开。
	// 也绝不接管理员「允许私网」设置、配置文件或环境变量 —— webhook 目标永远只允许公网,
	// 管理员例外只属于上游渠道, 不得复用到这里。只有同包 _test.go 替换本变量
	// (让 httptest 环回监听可达), 生产没有任何入口能改写它。
	webhookDialAllowPrivate = func() bool { return false }
	// webhookDispatchRunning 单飞标志: 上一轮还在跑就跳过本轮 tick。
	webhookDispatchRunning int32
)

var webhookTerminalStatuses = []string{
	model.WebhookDeliveryStatusSucceeded,
	model.WebhookDeliveryStatusFailed,
	model.WebhookDeliveryStatusCancelled,
}

// webhookSignature 计算 X-Switchyard-Signature: HMAC-SHA256(secret, timestamp + "." + rawBody),
// 输出 "sha256=<hex>"; 时间戳与 body 都由签名覆盖, 接收端可完整复算。
func webhookSignature(secret string, timestamp int64, rawBody string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write([]byte(rawBody))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookNewDeliveryID 生成稳定投递 ID(128 位随机, hex)。
func webhookNewDeliveryID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate delivery id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// webhookBuildPayload 组装投递请求体: 只有该用户自己的通知字段,
// 不带请求原文、API Key、密钥或任何外部回显。
func webhookBuildPayload(deliveryID, event string, at time.Time, userID uint, data model.WebhookPayloadData) (string, error) {
	payload := model.WebhookPayload{
		ID:        deliveryID,
		Event:     event,
		Timestamp: at.Unix(),
		UserID:    userID,
		Data:      data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// webhookEnqueueTx 入队一条投递(与业务写入同事务)。payload 字节自此不变, 签名每次重签。
func webhookEnqueueTx(tx *gorm.DB, hook model.Webhook, event string, notificationID uint64, data model.WebhookPayloadData) (string, error) {
	deliveryID, err := webhookNewDeliveryID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	payload, err := webhookBuildPayload(deliveryID, event, now, hook.UserID, data)
	if err != nil {
		return "", err
	}
	delivery := model.WebhookDelivery{
		DeliveryID:     deliveryID,
		WebhookID:      hook.ID,
		UserID:         hook.UserID,
		HookVersion:    hook.Version,
		Event:          event,
		NotificationID: notificationID,
		Payload:        payload,
		Status:         model.WebhookDeliveryStatusPending,
		NextAttemptAt:  now,
	}
	if err := tx.Create(&delivery).Error; err != nil {
		return "", fmt.Errorf("enqueue webhook delivery: %w", err)
	}
	return deliveryID, nil
}

// webhookEventSubscribed 判断订阅事件表是否包含某事件。
func webhookEventSubscribed(events []string, event string) bool {
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}

// webhookEnqueueForNotificationTx 通知落库时同步入队: 只匹配该用户自己的、且订阅了该事件的订阅。
// 与通知写入同事务; 任何失败都上抛(不吞错), 由调用方整单回滚。
func webhookEnqueueForNotificationTx(tx *gorm.DB, n *model.Notification) error {
	if n == nil || n.UserID == 0 {
		return nil
	}
	var hooks []model.Webhook
	if err := tx.Where("user_id = ? AND enabled = ?", n.UserID, true).Find(&hooks).Error; err != nil {
		return fmt.Errorf("load webhooks for notification: %w", err)
	}
	if len(hooks) == 0 {
		return nil
	}
	data := model.WebhookPayloadData{
		Title:     n.Title,
		Content:   n.Content,
		Link:      n.Link,
		RelatedID: n.RelatedID,
	}
	for _, hook := range hooks {
		events, err := model.WebhookDecodeEvents(hook.Events)
		if err != nil {
			// 存量损坏订阅行: 报错可见, 不静默投空也不静默跳过。
			return fmt.Errorf("webhook %d events: %w", hook.ID, err)
		}
		if !webhookEventSubscribed(events, string(n.Type)) {
			continue
		}
		if _, err := webhookEnqueueTx(tx, hook, string(n.Type), n.ID, data); err != nil {
			return err
		}
	}
	return nil
}

// webhookHTTPClient 构造一次性投递客户端:
//   - Proxy 恒 nil: 忽略 HTTP(S)_PROXY 等环境代理, 防止经内部代理绕过拨号防护;
//   - DialContext 走 netguard.DialGuard: 拨号前按解析结果复核, 防 DNS rebinding;
//   - CheckRedirect 不跟随重定向(3xx 按终结失败处理), 防重定向打到内网;
//   - 总超时 5s。
func webhookHTTPClient() *http.Client {
	guard := netguard.DialGuard{AllowPrivate: func() bool {
		// 显式判定而非 nil: 即便钩子被清空也回落到 false(拦截), 决不回落到 DialGuard 的 nil=放行。
		return webhookDialAllowPrivate != nil && webhookDialAllowPrivate()
	}}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            guard.DialContext,
		MaxResponseHeaderBytes: 32 << 10,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   webhookHTTPTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// webhookSend 发一次投递: 签名覆盖 timestamp + "." + rawBody, 响应读一小段即丢弃。
// 返回 HTTP 状态码(0 = 未获得响应)。错误文本可能内嵌完整 URL, 由调用方脱敏后才可落库/日志。
func webhookSend(ctx context.Context, rawURL, secret, rawBody, deliveryID string) (int, error) {
	timestamp := time.Now().Unix()
	signature := webhookSignature(secret, timestamp, rawBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(rawBody))
	if err != nil {
		return 0, fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Switchyard-Delivery-ID", deliveryID)
	req.Header.Set("X-Switchyard-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-Switchyard-Signature", signature)

	resp, err := webhookHTTPClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// 读一小段即丢弃: 正文绝不保存(防泄露/SSRF 回显), 但要读走让连接正常收尾。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, WebhookResponseReadLimit))
	return resp.StatusCode, nil
}

// webhookClaim 抢占结果: 投递行 + 订阅快照 + 本次尝试序号(= 抢占后的 attempts)。
type webhookClaim struct {
	delivery model.WebhookDelivery
	hook     model.Webhook
	attempts int
}

// webhookClaimDelivery 原子抢占一条到期投递(或回收 lease 过期的 processing)。
//
// 抢占是一条带乐观锁的条件更新(status + attempts 同时匹配才生效): 多 worker 并发只有一个能拿到。
// 同一事务里先做订阅现状检查 —— 订阅已删除、URL 已改绑(版本不符)、已禁用(测试发送除外)
// 的投递直接取消, 保证旧投递永远不会发往旧 URL。
func webhookClaimDelivery(ctx context.Context, id uint64) (webhookClaim, bool, error) {
	var out webhookClaim
	owned := false
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var d model.WebhookDelivery
		if err := tx.First(&d, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		now := time.Now()
		due := d.Status == model.WebhookDeliveryStatusPending && !d.NextAttemptAt.After(now)
		stale := d.Status == model.WebhookDeliveryStatusProcessing &&
			d.LeaseExpiresAt != nil && d.LeaseExpiresAt.Before(now)
		if !due && !stale {
			return nil
		}

		var hook model.Webhook
		herr := tx.Where("id = ?", d.WebhookID).First(&hook).Error
		cancel := false
		switch {
		case errors.Is(herr, gorm.ErrRecordNotFound):
			cancel = true // 订阅已被删除: 残留投递取消。
		case herr != nil:
			return herr
		case hook.Version != d.HookVersion:
			cancel = true // URL 已改绑: 旧版本投递取消, 不发往新旧任何一个地址。
		case !hook.Enabled && d.Event != model.WebhookEventTest:
			cancel = true // 已禁用: 只放行用户显式的测试发送。
		}
		if cancel {
			if err := webhookMarkDoneTx(tx, d.ID, d.Status, model.WebhookDeliveryStatusCancelled, 0, "delivery target no longer valid", now); err != nil {
				return err
			}
			if herr == nil {
				return webhookSetLastStatusTx(tx, hook.ID, model.WebhookDeliveryStatusCancelled)
			}
			return nil
		}

		res := tx.Model(&model.WebhookDelivery{}).
			Where("id = ? AND status = ? AND attempts = ?", d.ID, d.Status, d.Attempts).
			Updates(map[string]interface{}{
				"status":           model.WebhookDeliveryStatusProcessing,
				"attempts":         d.Attempts + 1,
				"lease_expires_at": now.Add(WebhookLeaseDuration),
				"updated_at":       now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return nil // 刚被别的 worker 抢走。
		}
		owned = true
		out = webhookClaim{delivery: d, hook: hook, attempts: d.Attempts + 1}
		return webhookSetLastStatusTx(tx, hook.ID, model.WebhookDeliveryStatusProcessing)
	})
	if err != nil {
		return webhookClaim{}, false, err
	}
	return out, owned, nil
}

// webhookMarkDoneTx 收尾写一条投递的终态/待重试状态。
// 条件带 status+attempts 乐观锁: 归属被回收(lease 过期后他人重新抢占)时写不进去, 旧 worker 收尾自动作废。
func webhookMarkDoneTx(tx *gorm.DB, id uint64, fromStatus, toStatus string, attempts int, lastErr string, next time.Time) error {
	updates := map[string]interface{}{
		"status":           toStatus,
		"last_error":       lastErr,
		"next_attempt_at":  next,
		"lease_expires_at": nil,
		"updated_at":       time.Now(),
	}
	query := tx.Model(&model.WebhookDelivery{}).Where("id = ?", id)
	if attempts > 0 {
		query = query.Where("attempts = ? AND status = ?", attempts, model.WebhookDeliveryStatusProcessing)
	} else {
		query = query.Where("status = ?", fromStatus)
	}
	res := query.Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	return nil
}

// webhookSetLastStatusTx 订阅级"最近投递状态"。
func webhookSetLastStatusTx(tx *gorm.DB, webhookID uint64, status string) error {
	if err := tx.Model(&model.Webhook{}).Where("id = ?", webhookID).
		Update("last_status", status).Error; err != nil {
		return fmt.Errorf("update webhook last status: %w", err)
	}
	return nil
}

// webhookDispatchOne 处理一条投递: 抢占 -> 解钥 -> 发送 -> 按结果收尾。
func webhookDispatchOne(ctx context.Context, id uint64) {
	claim, owned, err := webhookClaimDelivery(ctx, id)
	if err != nil {
		log.Warnf("webhook delivery %d claim failed: %v", id, err)
		return
	}
	if !owned {
		return
	}
	secret, err := webhookOpenSecret(claim.hook)
	if err != nil {
		// 密钥解不开(主密钥不对/密文被搬动): 配置性故障, 重试无意义, 直接终结。
		webhookFinalize(ctx, claim, 0, fmt.Errorf("webhook secret unavailable: %w", err))
		return
	}
	statusCode, sendErr := webhookSend(ctx, claim.hook.URL, secret, claim.delivery.Payload, claim.delivery.DeliveryID)
	webhookFinalize(ctx, claim, statusCode, sendErr)
}

// webhookFinalize 投递收尾: 2xx 成功; 429/408/5xx/网络失败按指数退避重试(不超过 retry_count);
// 其余状态(3xx 不跟随、其它 4xx)终结失败。收尾条件写回防止归属易手后的覆盖。
func webhookFinalize(ctx context.Context, claim webhookClaim, statusCode int, sendErr error) {
	now := time.Now()
	status := model.WebhookDeliveryStatusSucceeded
	lastErr := ""
	next := now
	if sendErr != nil || statusCode < 200 || statusCode > 299 {
		retryable := sendErr != nil || statusCode == http.StatusTooManyRequests ||
			statusCode == http.StatusRequestTimeout || statusCode >= 500
		if retryable && claim.attempts <= claim.hook.RetryCount {
			status = model.WebhookDeliveryStatusPending
			next = now.Add(webhookBackoff(claim.attempts))
		} else {
			status = model.WebhookDeliveryStatusFailed
		}
		lastErr = webhookSanitizeError(sendErr, statusCode, claim.hook.URL)
	}

	res := db.GetDB().WithContext(ctx).Model(&model.WebhookDelivery{}).
		Where("id = ? AND attempts = ? AND status = ?", claim.delivery.ID, claim.attempts, model.WebhookDeliveryStatusProcessing).
		Updates(map[string]interface{}{
			"status":           status,
			"last_status_code": statusCode,
			"last_error":       lastErr,
			"next_attempt_at":  next,
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	if res.Error != nil {
		log.Warnf("webhook delivery %d finalize failed: %v", claim.delivery.ID, res.Error)
		return
	}
	if res.RowsAffected != 1 {
		return // 归属已易手(lease 过期被回收), 旧收尾不覆盖新状态。
	}
	if err := webhookSetLastStatusTx(db.GetDB().WithContext(ctx), claim.hook.ID, status); err != nil {
		log.Warnf("webhook %d last status update failed: %v", claim.hook.ID, err)
	}
}

// webhookBackoff 有界指数退避: base 按尝试次数倍增, 封顶 max。
func webhookBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := WebhookRetryBaseDelay
	for i := 1; i < attempts; i++ {
		if delay >= WebhookRetryMaxDelay/2 {
			return WebhookRetryMaxDelay
		}
		delay *= 2
	}
	if delay > WebhookRetryMaxDelay {
		return WebhookRetryMaxDelay
	}
	return delay
}

// WebhookDispatchRun 一轮批量投递: 挑到期投递(含 lease 过期回收), 有界并发处理。
// 单飞: 上一轮未完不叠加新一轮, tick 间隔小于单轮耗时时不会越积越多协程。
func WebhookDispatchRun(ctx context.Context, limit int) error {
	if !atomic.CompareAndSwapInt32(&webhookDispatchRunning, 0, 1) {
		return nil
	}
	defer atomic.StoreInt32(&webhookDispatchRunning, 0)
	if limit <= 0 || limit > 100 {
		limit = WebhookDispatchBatch
	}
	now := time.Now()
	var ids []uint64
	if err := db.GetDB().WithContext(ctx).Model(&model.WebhookDelivery{}).
		Where("(status = ? AND next_attempt_at <= ?) OR (status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?)",
			model.WebhookDeliveryStatusPending, now,
			model.WebhookDeliveryStatusProcessing, now).
		Order("next_attempt_at ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("list webhook deliveries: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	sem := make(chan struct{}, WebhookDispatchConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("webhook delivery %d dispatch panic: %v", id, r)
				}
			}()
			webhookDispatchOne(ctx, id)
		}(id)
	}
	wg.Wait()
	return nil
}

// webhookSanitizeError 生成可落库的脱敏错误摘要: 状态码或网络错误经 scrub 后截断,
// 绝不包含响应正文、密钥或完整 URL(URL 可能带 token, 响应可能是 SSRF 回显的内网内容)。
func webhookSanitizeError(sendErr error, statusCode int, rawURL string) string {
	if sendErr != nil {
		return webhookScrubText(sendErr.Error(), rawURL)
	}
	return fmt.Sprintf("endpoint responded %d", statusCode)
}

// webhookScrubText 脱敏错误文本: 先抹掉已知 URL 原文, 再把一切 scheme:// 形态的片段换成 [url],
// 去控制字符后限长。网络错误(dial/TLS/超时)天然内嵌完整 URL, 这一步是日志不泄 token 的关键。
func webhookScrubText(text, rawURL string) string {
	if rawURL != "" {
		text = strings.ReplaceAll(text, rawURL, "[url]")
	}
	text = scrubURLSpans(text)
	text = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	const maxErrorRunes = 300
	if runes := []rune(text); len(runes) > maxErrorRunes {
		text = string(runes[:maxErrorRunes])
	}
	return text
}

// scrubURLSpans 把 scheme://... 形态的连续片段替换成 [url]。
// 不用正则: 这里只需"冒号双斜杠起, 吃到空白/引号止"的字节扫描, 语义直观且无转义歧义。
func scrubURLSpans(text string) string {
	var b strings.Builder
	i := 0
	for {
		idx := strings.Index(text[i:], "://")
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		start := i + idx
		s := start
		for s > 0 {
			c := text[s-1]
			isScheme := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
			if !isScheme {
				break
			}
			s--
		}
		e := start + 3
		for e < len(text) {
			c := text[e]
			if c <= ' ' || c == 34 || c == 39 { // 空白/控制字符或引号即止。
				break
			}
			e++
		}
		b.WriteString(text[i:s])
		b.WriteString("[url]")
		i = e
	}
	return b.String()
}

// WebhookCleanupRun 队列/日志容量控制: 超保留期的终态记录删除;
// 每订阅终态日志只留最近 WebhookLogMaxPerHook 条; 非终态积压超上限时取消最旧。
// 为什么在清理任务而不是入队时截断: 入队是通知写入事务的一部分, 必须确定性成功;
// 有界的容量收敛放后台任务, 高峰过载按"最旧先放弃"收敛, 不阻塞业务写入。
func WebhookCleanupRun(ctx context.Context) error {
	conn := db.GetDB().WithContext(ctx)
	cutoff := time.Now().Add(-WebhookLogRetention)
	if err := conn.Where("status IN ? AND updated_at < ?", webhookTerminalStatuses, cutoff).
		Delete(&model.WebhookDelivery{}).Error; err != nil {
		return fmt.Errorf("purge expired webhook deliveries: %w", err)
	}

	var hookIDs []uint64
	if err := conn.Model(&model.WebhookDelivery{}).Distinct().Pluck("webhook_id", &hookIDs).Error; err != nil {
		return fmt.Errorf("list webhook ids: %w", err)
	}
	for _, hookID := range hookIDs {
		if err := webhookTrimHook(ctx, hookID); err != nil {
			return err
		}
	}
	return nil
}

// webhookTrimHook 对单个订阅做容量收敛。
func webhookTrimHook(ctx context.Context, hookID uint64) error {
	conn := db.GetDB().WithContext(ctx)

	// 终态日志: 只留最近 N 条(按自增 id, 与创建顺序一致)。
	var terminal []model.WebhookDelivery
	if err := conn.Select("id").Where("webhook_id = ? AND status IN ?", hookID, webhookTerminalStatuses).
		Order("id DESC").Find(&terminal).Error; err != nil {
		return fmt.Errorf("list terminal webhook deliveries: %w", err)
	}
	if len(terminal) > WebhookLogMaxPerHook {
		drop := make([]uint64, 0, len(terminal)-WebhookLogMaxPerHook)
		for _, row := range terminal[WebhookLogMaxPerHook:] {
			drop = append(drop, row.ID)
		}
		if err := conn.Where("id IN ?", drop).Delete(&model.WebhookDelivery{}).Error; err != nil {
			return fmt.Errorf("trim terminal webhook deliveries: %w", err)
		}
	}

	// 非终态积压: 超上限取消最旧(活儿永远干不完时先放弃最老的, 队列有界)。
	var backlog []model.WebhookDelivery
	if err := conn.Select("id").Where("webhook_id = ? AND status IN ?", hookID,
		[]string{model.WebhookDeliveryStatusPending, model.WebhookDeliveryStatusProcessing}).
		Order("id ASC").Find(&backlog).Error; err != nil {
		return fmt.Errorf("list pending webhook deliveries: %w", err)
	}
	if len(backlog) > WebhookPendingMaxPerHook {
		drop := make([]uint64, 0, len(backlog)-WebhookPendingMaxPerHook)
		for _, row := range backlog[:len(backlog)-WebhookPendingMaxPerHook] {
			drop = append(drop, row.ID)
		}
		if err := conn.Model(&model.WebhookDelivery{}).Where("id IN ?", drop).
			Updates(map[string]interface{}{
				"status":           model.WebhookDeliveryStatusCancelled,
				"last_error":       "delivery cancelled: queue capacity limit",
				"lease_expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("cancel overflowing webhook deliveries: %w", err)
		}
	}
	return nil
}

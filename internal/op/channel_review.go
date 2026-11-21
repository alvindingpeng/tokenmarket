package op

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// 渠道准入审核(P1): 提交 → 审批 → 发布闸口 + 关键配置指纹复审。
// 门禁只管「新暴露」(发布、进公开候选), 已在分组里跑的成员不受审核状态影响。

// channelConfigFingerprint 是审核口径指纹在 op 层的入口: 实现只有一份, 在 model.ChannelConfigFingerprint。
//
// 为什么留这个转调而不直接调 model: 这里承载的是"审核按什么判"这个业务判断, op 内十几处调用点
// 都以它为准; 之后口径若再要变(例如某些方言下路径并不同等重要), 改的是这一层的语义,
// 而"怎么算"始终留在 model 层与迁移共用。
//
// 分开的理由是硬约束: 迁移要重算基线, 而 internal/db/migrate 不能 import internal/op
// (op -> db -> migrate -> op 成环)。审计口径只能有一份实现 —— 两处各写一份必然漂移,
// 而漂移的表现是"迁移算出的基线与运行期算出的指纹不等", 也就是所有已审渠道一次性全转 stale。
func channelConfigFingerprint(config model.ChannelConfig, modelNames []string) string {
	return model.ChannelConfigFingerprint(config, modelNames)
}

// channelModelNames 返回渠道当前模型名清单(缓存读取), 供指纹计算。
func channelModelNames(channelID int) []string {
	names := make([]string, 0)
	for _, cm := range channelModelCache.GetAll() {
		if cm.ChannelID == channelID {
			names = append(names, cm.Name)
		}
	}
	return names
}

// channelFingerprintOf 按渠道当前缓存状态计算指纹(配置 + 模型清单)。
func channelFingerprintOf(channel model.Channel) string {
	return channelConfigFingerprint(channel.ChannelConfig, channelModelNames(channel.ID))
}

// channelReviewBypass 判断渠道是否走管理员免审: 开关开启(默认开)且渠道归属者是管理员。
// 免审在创建与提交两处生效, 管理员自用渠道全程不被审核流程卡住。
func channelReviewBypass(ownerID uint) bool {
	if enabled, err := SettingGetBool(model.SettingKeyChannelReviewAdminBypass); err == nil && !enabled {
		return false
	}
	user, err := UserGetByID(ownerID)
	return err == nil && user.Role == model.RoleAdmin
}

// ChannelReviewSubmit 提交审核: draft|rejected|stale → pending。
// 已通过的渠道走「改配置触发 stale」的复审路径, 不接受重复提交; pending 只允许一条。
// 管理员免审渠道提交即通过, 直接写入当前配置指纹。
func ChannelReviewSubmit(id int, note string, scope model.Scope, ctx context.Context) (model.Channel, error) {
	channel, ok := channelCache.Get(id)
	if !ok || !scope.Owns(channel.UserID) {
		return model.Channel{}, fmt.Errorf("channel not found")
	}
	switch channel.ReviewStatus {
	case model.ChannelReviewDraft, model.ChannelReviewRejected, model.ChannelReviewStale:
	case model.ChannelReviewPending:
		return model.Channel{}, fmt.Errorf("channel review already pending")
	default:
		return model.Channel{}, fmt.Errorf("channel already approved")
	}

	now := time.Now()
	updates := map[string]any{
		"review_note":   strings.TrimSpace(note),
		"submitted_at":  &now,
		"review_status": model.ChannelReviewPending,
	}
	if channelReviewBypass(channel.UserID) {
		updates["review_status"] = model.ChannelReviewApproved
		updates["config_fingerprint"] = channelFingerprintOf(channel)
		updates["reviewed_by"] = scope.ID
		updates["reviewed_at"] = &now
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		return model.Channel{}, fmt.Errorf("failed to submit channel review: %w", err)
	}
	channel.ReviewStatus = updates["review_status"].(string)
	channel.ReviewNote = updates["review_note"].(string)
	channel.SubmittedAt = &now
	if fp, ok := updates["config_fingerprint"].(string); ok {
		channel.ConfigFingerprint = fp
		channel.ReviewedBy = scope.ID
		channel.ReviewedAt = &now
		// 管理员免审提交也必须启动观察期, 与人工审批通过保持同一质量门槛。
		if err := ChannelTrustStart(db.GetDB().WithContext(ctx), id); err != nil {
			return model.Channel{}, fmt.Errorf("failed to start channel observation: %w", err)
		}
		var fresh model.Channel
		if err := db.GetDB().WithContext(ctx).Select("trust_level", "observation_start", "observation_until").First(&fresh, id).Error; err == nil {
			channel.TrustLevel = fresh.TrustLevel
			channel.ObservationStart = fresh.ObservationStart
			channel.ObservationUntil = fresh.ObservationUntil
		}
	}
	setChannelCachePreservingQuota(channel)
	return channel, nil
}

// ChannelReviewApprove 批准审核: pending → approved, 写入当前配置指纹作为复审基线。
// 只接受 pending: rejected/stale 须由归属者重新提交后再审, 审核人不代提交。
func ChannelReviewApprove(id int, note string, reviewerID uint, scope model.Scope, ctx context.Context) (model.Channel, error) {
	if !scope.IsAdmin() {
		return model.Channel{}, fmt.Errorf("admin role required")
	}
	channel, ok := channelCache.Get(id)
	if !ok {
		return model.Channel{}, fmt.Errorf("channel not found")
	}
	if channel.ReviewStatus != model.ChannelReviewPending {
		return model.Channel{}, fmt.Errorf("channel is not pending review")
	}

	now := time.Now()
	updates := map[string]any{
		"review_status":      model.ChannelReviewApproved,
		"review_note":        strings.TrimSpace(note),
		"config_fingerprint": channelFingerprintOf(channel),
		"reviewed_by":        reviewerID,
		"reviewed_at":        &now,
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		return model.Channel{}, fmt.Errorf("failed to approve channel review: %w", err)
	}
	// 通过审核即进入观察期(P2): 允许发布, 但先按观察期标注并在达标前受评分折减。
	// 已转正的渠道不重置(复审不应让稳定渠道从头跑一轮), ChannelTrustStart 内部按 trust_level 判断。
	if err := ChannelTrustStart(db.GetDB().WithContext(ctx), id); err != nil {
		// 观察期启动失败不该回滚已完成的审核: 审核状态是人工决策, 观察期只是后续质量跟踪。
		// 最坏情况是渠道以旧的 trust_level 继续运行, 下一轮评估任务会把它纠正过来。
		log.Warnf("failed to start observation for channel %d: %v", id, err)
	}
	channel.ReviewStatus = model.ChannelReviewApproved
	channel.ReviewNote = updates["review_note"].(string)
	channel.ConfigFingerprint = updates["config_fingerprint"].(string)
	channel.ReviewedBy = reviewerID
	channel.ReviewedAt = &now
	// 从库里回读观察期三列: ChannelTrustStart 直接写库, 缓存里那份还是旧值。
	// 用缓存回填等于把改动覆盖回旧值 —— 卡片会恒显示「已转正」而数据库里其实是 observing。
	var fresh model.Channel
	if err := db.GetDB().WithContext(ctx).Select("trust_level", "observation_start", "observation_until").
		First(&fresh, id).Error; err == nil {
		channel.TrustLevel = fresh.TrustLevel
		channel.ObservationStart = fresh.ObservationStart
		channel.ObservationUntil = fresh.ObservationUntil
	}
	setChannelCachePreservingQuota(channel)
	return channel, nil
}

// ChannelReviewReject 驳回审核: pending → rejected。驳回原因必填, 归属者按原因修改后重新提交。
func ChannelReviewReject(id int, note string, reviewerID uint, scope model.Scope, ctx context.Context) (model.Channel, error) {
	if !scope.IsAdmin() {
		return model.Channel{}, fmt.Errorf("admin role required")
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return model.Channel{}, fmt.Errorf("reject reason is required")
	}
	channel, ok := channelCache.Get(id)
	if !ok {
		return model.Channel{}, fmt.Errorf("channel not found")
	}
	if channel.ReviewStatus != model.ChannelReviewPending {
		return model.Channel{}, fmt.Errorf("channel is not pending review")
	}

	now := time.Now()
	updates := map[string]any{
		"review_status": model.ChannelReviewRejected,
		"review_note":   note,
		"reviewed_by":   reviewerID,
		"reviewed_at":   &now,
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		return model.Channel{}, fmt.Errorf("failed to reject channel review: %w", err)
	}
	channel.ReviewStatus = model.ChannelReviewRejected
	channel.ReviewNote = note
	channel.ReviewedBy = reviewerID
	channel.ReviewedAt = &now
	setChannelCachePreservingQuota(channel)
	return channel, nil
}

// ChannelReviewList 列出审核队列: 管理员全量, 渠道商只看自有渠道; status 为空给全部状态。
// 队列条目只带审核决策需要的字段, 不外露路径与凭据。
func ChannelReviewList(status string, scope model.Scope) []model.ChannelReviewItem {
	items := make([]model.ChannelReviewItem, 0, channelCache.Len())
	for _, channel := range channelCache.GetAll() {
		if !scope.Owns(channel.UserID) {
			continue
		}
		if status != "" && channel.ReviewStatus != status {
			continue
		}
		ownerName := fmt.Sprintf("user#%d", channel.UserID)
		if user, err := UserGetByID(channel.UserID); err == nil {
			ownerName = user.Username
		}
		reviewerName := ""
		if channel.ReviewedBy != 0 {
			if user, err := UserGetByID(channel.ReviewedBy); err == nil {
				reviewerName = user.Username
			}
		}
		items = append(items, model.ChannelReviewItem{
			ChannelID:    channel.ID,
			UserID:       channel.UserID,
			OwnerName:    ownerName,
			ChannelName:  channel.Name,
			ReviewStatus: channel.ReviewStatus,
			ReviewNote:   channel.ReviewNote,
			SubmittedAt:  channel.SubmittedAt,
			ReviewedBy:   channel.ReviewedBy,
			ReviewerName: reviewerName,
			ReviewedAt:   channel.ReviewedAt,
			Shared:       channel.Shared,
			Enabled:      channel.Enabled,
			ModelCount:   len(channelModelNames(channel.ID)),
		})
	}
	// 审核队列按提交时间倒序, 未提交(draft)排最后; 同一时间按主键定序保证稳定。
	sort.Slice(items, func(i, j int) bool {
		si, sj := items[i].SubmittedAt, items[j].SubmittedAt
		if si == nil && sj == nil {
			return items[i].ChannelID < items[j].ChannelID
		}
		if si == nil {
			return false
		}
		if sj == nil {
			return true
		}
		if !si.Equal(*sj) {
			return si.After(*sj)
		}
		return items[i].ChannelID < items[j].ChannelID
	})
	return items
}

// channelReviewMarkStale 把已过审渠道转 stale: 关键配置指纹失配, 复审前不新增暴露。
// 只改审核态与审计, 不碰 shared/enabled/分组成员 —— 存量分组流量零中断。
func channelReviewMarkStale(ctx context.Context, actorID uint, channelID int) error {
	now := time.Now()
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", channelID).
		Updates(map[string]any{"review_status": model.ChannelReviewStale, "reviewed_at": &now}).Error; err != nil {
		return fmt.Errorf("failed to mark channel stale: %w", err)
	}
	if channel, ok := channelCache.Get(channelID); ok {
		channel.ReviewStatus = model.ChannelReviewStale
		channel.ReviewedAt = &now
		setChannelCachePreservingQuota(channel)
	}
	if err := AuditLogAdd(ctx, actorID, "channel.review_stale", fmt.Sprintf("channel#%d", channelID), "config fingerprint mismatch"); err != nil {
		return fmt.Errorf("failed to write stale audit: %w", err)
	}
	return nil
}

// channelReviewSyncBaseline 把当前配置指纹写回基线: 管理员免审渠道的改动不进复审流程,
// 基线随人工改动推进, 避免免审渠道反复误报失配。
func channelReviewSyncBaseline(ctx context.Context, channelID int) error {
	channel, ok := channelCache.Get(channelID)
	if !ok || channel.ReviewStatus != model.ChannelReviewApproved {
		return nil // 未过审渠道无基线语义; approved 的基线才代表"审核看到的样子"。
	}
	fp := channelFingerprintOf(channel)
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", channelID).
		Update("config_fingerprint", fp).Error; err != nil {
		return fmt.Errorf("failed to sync channel fingerprint: %w", err)
	}
	channel.ConfigFingerprint = fp
	setChannelCachePreservingQuota(channel)
	return nil
}

// ChannelReviewBackfill 存量渠道一次性回填: 已发布(shared=1)的渠道本就挂在公开面,
// 直接置 approved 并写入当前配置指纹作复审基线, 其余保持 draft。
// 幂等条件是 shared=1 且 review_status=draft: 发布闸口保证新渠道只有 approved 才能置 shared,
// 故该条件永远只命中上线前的存量行, 重复执行零副作用。
// 返回回填条数, 为 0 时不写审计。
func ChannelReviewBackfill(ctx context.Context) (int, error) {
	conn := db.GetDB().WithContext(ctx)
	var channels []model.Channel
	if err := conn.Where("shared = ? AND review_status = ?", true, model.ChannelReviewDraft).
		Find(&channels).Error; err != nil {
		return 0, fmt.Errorf("failed to load legacy channels: %w", err)
	}
	if len(channels) == 0 {
		return 0, nil
	}

	// 模型名清单从库里取: 回填早于缓存预热顺序无关, 自带查询不依赖缓存状态。
	namesByChannel := make(map[int][]string)
	var channelModels []model.ChannelModel
	if err := conn.Find(&channelModels).Error; err != nil {
		return 0, fmt.Errorf("failed to load channel models: %w", err)
	}
	for _, cm := range channelModels {
		namesByChannel[cm.ChannelID] = append(namesByChannel[cm.ChannelID], cm.Name)
	}

	now := time.Now()
	for _, channel := range channels {
		fp := channelConfigFingerprint(channel.ChannelConfig, namesByChannel[channel.ID])
		updates := map[string]any{
			"review_status":      model.ChannelReviewApproved,
			"config_fingerprint": fp,
			"reviewed_at":        &now,
		}
		if err := conn.Model(&model.Channel{}).Where("id = ?", channel.ID).
			Updates(updates).Error; err != nil {
			return 0, fmt.Errorf("failed to backfill channel review: %w", err)
		}
		channel.ReviewStatus = model.ChannelReviewApproved
		channel.ConfigFingerprint = fp
		channel.ReviewedAt = &now
		// 存量回填也必须进入观察期, 与管理员免审创建/提交保持同一质量门槛。
		{
			if err := ChannelTrustStart(conn, channel.ID); err != nil {
				return 0, fmt.Errorf("failed to start channel observation: %w", err)
			}
			channel.TrustLevel = model.ChannelTrustObserving
			var fresh model.Channel
			if err := conn.Select("trust_level", "observation_start", "observation_until").Where("id = ?", channel.ID).Take(&fresh).Error; err == nil {
				channel.ObservationStart = fresh.ObservationStart
				channel.ObservationUntil = fresh.ObservationUntil
			}
		}
		setChannelCachePreservingQuota(channel)
	}

	detail := fmt.Sprintf("backfilled=%d", len(channels))
	if err := AuditLogAdd(ctx, 0, "channel.review_backfill", "channels", detail); err != nil {
		return len(channels), fmt.Errorf("failed to write backfill audit: %w", err)
	}
	return len(channels), nil
}

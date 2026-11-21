package migrate

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 46, Up: Migration046_ChannelFingerprintSupplyFields})
}

// 迁移 46 的审计动作标识: 让"哪天把指纹基线动过"在审计表里查得到。
const auditActionChannelFingerprintBaseline = "channel.review_fingerprint_baseline"

// Migration046_ChannelFingerprintSupplyFields 给「当前已过审」的公开渠道重算配置指纹基线。
//
// 背景: 审核口径指纹(channelConfigFingerprint)本轮补进了两个会改变供给行为的字段 ——
// openai_video_base_path(视频请求的落地路径, 与其余五个协议路径同类)与
// sub_platform(订阅渠道的平台标识, 决定账号平台/凭据种类/上游地址装配)。
// 补进去之后, 运行期算出的指纹与库里那份旧基线必然不等 —— 若不重算,
// 所有已过审渠道会在下一次人工保存时**批量**转 stale, 而它们其实一个字都没改。
// 这正是"改算法必须配一次基线迁移"的原因: 否则修的是漏审, 换来的是大面积误停审。
//
// 本迁移的语义是**认现状为基线**: 把迁移当日的库里配置当作"审核看到的样子"写进 config_fingerprint。
// 取舍与能力上限写在 docs/channel-fingerprint-supply-fields.md, 这里只留一句结论:
// 迁移之前这两项被静默改过的话无法追溯 —— 库里没有历史配置快照, 也没有当时这两个字段的审计;
// 我们选择不制造大面积停审(把全部已审渠道一次打成 stale 会让公开面几乎全部需要人工复审,
// 且没有任何证据说它们真被改过), 认现状继续。此后它俩的任何改动都会触发 stale/复审。
//
// 只动 config_fingerprint 一列: 不改审核状态, 不把任何渠道置 stale, 不碰其它列。
// 幂等: 逐条比对, 与库里已存值相同的行不写库; 全部相同则不写审计 —— 重跑零副作用。
// 表/列缺失时安全跳过(HasTable/HasColumn), 照 040/045 的做法。
func Migration046_ChannelFingerprintSupplyFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	const table = "channels"
	if !db.Migrator().HasTable(table) {
		// 表不存在 = 该库还没跑到建表这一步, 或这张表不该由本版本管: 安全跳过, 不制造错误。
		return nil
	}
	// 列缺失时无法算出与运行期一致的指纹: 缺哪一列都意味着这份库不是本迁移要服务的形态,
	// 按"不可知即不猜"跳过, 而不是用空串凑一个看起来对、实际与运行期不等的新基线。
	if !db.Migrator().HasColumn(table, "config_fingerprint") ||
		!db.Migrator().HasColumn(table, "review_status") ||
		!db.Migrator().HasColumn(table, "shared") ||
		!db.Migrator().HasColumn(table, "dialect") ||
		!db.Migrator().HasColumn(table, "base_url") ||
		!db.Migrator().HasColumn(table, "openai_video_base_path") ||
		!db.Migrator().HasColumn(table, "sub_platform") {
		log.Infof("migrate 46: 缺列, 跳过指纹基线重算")
		return nil
	}

	// 只处理「已过审 且 已发布」的渠道: 它们的基线才代表"审核看到的样子"。
	// draft/pending/rejected 没有基线语义; stale 是有待复审的失配态, 重算会把"待复审"抹成"已审",
	// 那是在替管理员做决定 —— 本迁移不做这件事。
	var channels []model.Channel
	if err := db.Where("review_status = ? AND shared = ?", model.ChannelReviewApproved, true).
		Find(&channels).Error; err != nil {
		return fmt.Errorf("load approved channels: %w", err)
	}
	if len(channels) == 0 {
		return nil
	}

	// 模型名一律从库里取, 不读进程缓存: 迁移的执行时机在缓存预热之前, 读缓存只会拿到空清单。
	namesByChannel := make(map[int][]string, len(channels))
	if db.Migrator().HasTable("channel_models") && db.Migrator().HasColumn("channel_models", "name") {
		var rows []struct {
			ChannelID int
			Name      string
		}
		if err := db.Table("channel_models").Select("channel_id, name").
			Where("channel_id IN ?", channelIDsOf(channels)).Scan(&rows).Error; err != nil {
			return fmt.Errorf("load channel models: %w", err)
		}
		for _, row := range rows {
			namesByChannel[row.ChannelID] = append(namesByChannel[row.ChannelID], row.Name)
		}
	}

	updated := 0
	for _, channel := range channels {
		fingerprint := model.ChannelConfigFingerprint(channel.ChannelConfig, namesByChannel[channel.ID])
		if fingerprint == channel.ConfigFingerprint {
			continue // 已是新口径: 重跑时不产生任何写入。
		}
		if err := db.Model(&model.Channel{}).Where("id = ?", channel.ID).
			Update("config_fingerprint", fingerprint).Error; err != nil {
			return fmt.Errorf("backfill fingerprint for channel %d: %w", channel.ID, err)
		}
		updated++
	}

	if updated > 0 {
		log.Infof("migrate 46: 重算 %d 个已过审渠道的配置指纹基线(openai_video_base_path / sub_platform 纳入指纹)", updated)
	}
	// 留痕: 审计由迁移自己写(actorID=0 系统), 事后方能回答"基线是哪天、因为什么被整体推进过"。
	// 写失败不回滚指纹: 基线已推进且是对的, 少了这条审计不该让启动失败。
	if err := writeFingerprintBaselineAudit(db, updated, len(channels)); err != nil {
		log.Warnf("migrate 46: 写审计失败(指纹基线已推进): %v", err)
	}
	return nil
}

// channelIDsOf 收集主键, 供 IN 查询使用(避免逐渠道一趟查询)。
func channelIDsOf(channels []model.Channel) []int {
	ids := make([]int, 0, len(channels))
	for _, channel := range channels {
		ids = append(ids, channel.ID)
	}
	return ids
}

// writeFingerprintBaselineAudit 写一条"基线被推进"的审计记录; updated 为 0 时不写(重跑不该留一堆空记录)。
// 用 model.AuditLog 而不是 op.AuditLogAdd: 迁移不能 import op(op -> db -> migrate -> op 成环)。
func writeFingerprintBaselineAudit(db *gorm.DB, updated, examined int) error {
	if updated == 0 {
		return nil
	}
	if !db.Migrator().HasTable("audit_logs") {
		return nil
	}
	return db.Create(&model.AuditLog{
		UserID:   0, // 系统动作, 无操作者。
		Username: "",
		Action:   auditActionChannelFingerprintBaseline,
		Target:   "channels",
		Detail: fmt.Sprintf("recomputed=%d examined=%d reason=%s",
			updated, examined, "审核口径指纹纳入 openai_video_base_path 与 sub_platform, 迁移 46 以迁移当日现状为新基线; 之前的静默改动无法追溯"),
	}).Error
}

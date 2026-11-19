package migrate

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 21,
		Up:      grandfatherExistingChannels,
	})
}

// grandfatherExistingChannels 把「观察期功能上线前就已存在」的渠道一次性标为 trusted(P2)。
//
// 为什么必须迁移而不能靠运行期兜底: trust_level 列在 P1 就随 AutoMigrate 建出来了, 默认值 observing。
// 所有存量渠道因此在列上都是 observing, 但它们从未有过 observation_start —— 观察期对它们没有意义。
// 若不迁移, 首次 channel_trust_eval 会把它们全部判为「观察期到期且无样本 ⇒ 不达标 ⇒ probation」,
// 给全站存量渠道的评分统一乘 0.70。这是一次静默的线上回归, 且没有任何错误日志。
//
// 判据用 observation_start IS NULL 而非 created_at: 只有真正走过 ChannelTrustStart 的渠道才有窗口,
// 用它区分「真的在观察期」与「列默认值恰好是 observing」最准确。
func grandfatherExistingChannels(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}
	if !db.Migrator().HasColumn("channels", "trust_level") {
		return nil
	}
	if !db.Migrator().HasColumn("channels", "observation_start") {
		return nil
	}
	// 只放行「已过审且从未进入观察期」的渠道, 两个限定都必要:
	//   - observation_start IS NULL: 真正走过 ChannelTrustStart 的渠道才有窗口, 已进入观察期的保持原状;
	//   - review_status = 'approved': 部署时刻还停在 draft/pending/rejected 的渠道尚未经历新流程,
	//     若一并放行, 它们日后审核通过时 ChannelTrustStart 会因「已是 trusted」而跳过观察期,
	//     等于给这批渠道开后门。让它们保持 observing 且无窗口, 由审核通过时正常开启观察。
	result := db.Exec(
		"UPDATE channels SET trust_level = ? WHERE observation_start IS NULL AND trust_level <> ? AND review_status = ?",
		"trusted", "trusted", "approved",
	)
	if result.Error != nil {
		return fmt.Errorf("grandfather channels: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		fmt.Printf("[migrate] grandfathered %d pre-existing channels to trusted at %s\n",
			result.RowsAffected, time.Now().Format(time.RFC3339))
	}
	// 未过审且无窗口的渠道恢复默认 observing: 它们还没经历任何一次审核, 不该带着
	// probation/revoked 这类「已被判定过」的状态进入新流程。trust_level 的列默认值就是 observing,
	// 这里只是把被误判改坏的行搬回原样。
	restored := db.Exec(
		"UPDATE channels SET trust_level = ? WHERE observation_start IS NULL AND review_status <> ? AND trust_level IN ?",
		"observing", "approved", []string{"probation", "revoked"},
	)
	if restored.Error != nil {
		return fmt.Errorf("restore unapproved channel trust: %w", restored.Error)
	}
	if restored.RowsAffected > 0 {
		fmt.Printf("[migrate] restored %d unapproved no-window channels to observing at %s\n",
			restored.RowsAffected, time.Now().Format(time.RFC3339))
	}
	return nil
}

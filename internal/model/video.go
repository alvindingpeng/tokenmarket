package model

import "time"

// VideoJob 记录一次生视频任务落在哪个渠道授权上, 供后续查询与取消任务时定位上游。
// 生视频的查询请求(/v1/videos/{id})不带模型名, 无法像创建那样按模型解析分组与成员,
// 因此创建成功时把归属落库, 查询时按任务 ID 取回; 这正是异步任务与同步生图的根本差别。
type VideoJob struct {
	JobID          string `json:"job_id" gorm:"primaryKey;column:job_id"`     // 上游返回的任务 ID。
	UserID         uint   `json:"user_id" gorm:"index;not null"`              // 发起任务的用户, 查询时校验归属。
	GroupID        int    `json:"group_id" gorm:"not null"`                   // 创建时命中的分组。
	ChannelGrantID int    `json:"channel_grant_id" gorm:"not null"`           // 创建时落地的渠道授权, 据此还原渠道与模型。
	ChannelID      int    `json:"channel_id" gorm:"not null;default:0;index"` // 创建时所在渠道, 用于校验任务归属不被凭据重绑改变。
	// 创建任务时实际使用的渠道凭据 ID。渠道凭据会轮换(换绑授权、停用旧凭据、按策略挑选),
	// 查询与取消必须用创建时那一把凭据, 拿"当前授权绑定的凭据"去查只会得到 404。
	// 只记 ID, 不记凭据明文; 0 表示老记录未记凭据, 查询侧回落授权绑定的凭据。
	ChannelKeyID int       `json:"channel_key_id" gorm:"not null;default:0"`
	ModelName    string    `json:"model_name" gorm:"not null"`       // 上游模型名。
	CreatedAt    time.Time `json:"created_at" gorm:"index;not null"` // 创建时间, 用于过期清理。
}

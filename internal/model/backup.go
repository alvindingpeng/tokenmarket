package model

import "time"

// DBDump is a full-database JSON export format for Switchyard.
// Import uses incremental semantics (insert new rows, and upsert on tables with natural keys).
//
// 注意: 导出是"解密后"的明文快照 —— 渠道凭据、密钥类设置项在 JSON 里都是可直接使用的明文。
// 这是跨实例恢复的前提(目标实例的主密钥不同也能导入, 导入时用目标实例的主密钥重新加密),
// 但也意味着裸 JSON 文件本身就是泄密面: 只能经既有备份加密封装(backup.passphrase, OCTOBK1)后外传,
// 或在受控通道内流转。不要指望"备份天然不含明文" —— 未经封装的导出文件就是一份凭据清单。
type DBDump struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`

	Channels      []Channel      `json:"channels,omitempty"`       // 渠道数据。
	ChannelKeys   []ChannelKey   `json:"channel_keys,omitempty"`   // 渠道凭据数据。
	ChannelModels []ChannelModel `json:"channel_models,omitempty"` // 渠道模型数据。
	ChannelGrants []ChannelGrant `json:"channel_grants,omitempty"` // 渠道授权数据, 含统计。
	Groups        []Group        `json:"groups,omitempty"`         // 分组数据。
	GroupItems    []GroupItem    `json:"group_items,omitempty"`    // 分组成员数据。
	LLMInfos      []LLMInfo      `json:"llm_infos,omitempty"`      // 模型价格数据。
	APIKeys       []APIKey       `json:"api_keys,omitempty"`       // API Key 数据。
	Settings      []Setting      `json:"settings,omitempty"`       // 系统设置数据。

	StatsTotal  []StatsTotal  `json:"stats_total,omitempty"`
	StatsDaily  []StatsDaily  `json:"stats_daily,omitempty"`
	StatsHourly []StatsHourly `json:"stats_hourly,omitempty"`
	StatsAPIKey []StatsAPIKey `json:"stats_api_key,omitempty"`
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
}

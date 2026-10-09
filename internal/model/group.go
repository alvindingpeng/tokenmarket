package model

// 分组选择上游成员的模式。
type GroupMode string

const (
	GroupModeManual   GroupMode = "manual"   // 只使用人工选中的成员。
	GroupModeFailover GroupMode = "failover" // 按成员排序选择并在失败时切换。
	GroupModePrice    GroupMode = "price"    // 低价优先: 在可用成员中选用户价最低者, 失败时按价递增切换。
	GroupModeLatency  GroupMode = "latency"  // 延迟优先: 按首响应耗时滑动平均选最快者, 失败时自动切换。
	GroupModeSuccess  GroupMode = "success"  // 成功率优先: 按滑动窗口成功率选最稳者, 失败时自动切换。
	GroupModeScore    GroupMode = "score"    // 综合评分: 价格, 延迟与成功率按权重加权评分, 选最高者。
	GroupModeRandom   GroupMode = "random"   // 随机分发: 在可用成员中随机选择, 请求均匀摊开。
)

// IsValidGroupMode 判断模式标识是否受支持, 供导入校验等非绑定场景使用。
func IsValidGroupMode(mode GroupMode) bool {
	switch mode {
	case GroupModeManual, GroupModeFailover, GroupModePrice, GroupModeLatency, GroupModeSuccess, GroupModeScore, GroupModeRandom:
		return true
	}
	return false
}

// 价格口径: 低价优先与综合评分比较成员价格时使用的维度。
const (
	PriceMetricBlended string = "blended" // 读写价格均值。
	PriceMetricInput   string = "input"   // 仅输入价格。
	PriceMetricOutput  string = "output"  // 仅输出价格。
)

// IsValidPriceMetric 判断价格口径标识是否受支持。
func IsValidPriceMetric(metric string) bool {
	switch metric {
	case PriceMetricBlended, PriceMetricInput, PriceMetricOutput:
		return true
	}
	return false
}

// 分组 Relay 的持久化配置，数据库中以 JSON 存储。
type GroupRelayConfig struct {
	MemberStreamIdleTimeoutSeconds        int    `json:"member_stream_idle_timeout_seconds" binding:"omitempty,min=1,max=3600"`
	RequestTimeoutSeconds                 int    `json:"request_timeout_seconds" binding:"omitempty,min=1,max=3600"`           // 整请求最大耗时, 包含重试与流式传输。
	MaxWaitSeconds                        int    `json:"max_wait_seconds" binding:"omitempty,min=0,max=300"`                   // 无可用成员时最大等待秒数, 0 立即返回。
	MaxWaitingRequests                    int    `json:"max_waiting_requests" binding:"omitempty,min=0,max=1000"`              // 同一分组同时排队的请求上限, 0 不排队。
	MaxTotalAttempts                      int    `json:"max_total_attempts" binding:"omitempty,min=1,max=100"`                 // 单请求跨成员最大尝试轮次。
	MemberMaxAttempts                     int    `json:"member_max_attempts" binding:"omitempty,min=1"`                        // 单个成员包含首次请求的总尝试次数, 动态选路模式(非手动)生效。
	MemberRetryIntervalSeconds            int    `json:"member_retry_interval_seconds" binding:"omitempty,min=1"`              // 同一成员相邻两次尝试之间的等待秒数。
	MemberNonStreamResponseTimeoutSeconds int    `json:"member_non_stream_response_timeout_seconds" binding:"omitempty,min=1"` // 单个成员返回完整非流式响应的超时秒数。
	MemberStreamFirstEventTimeoutSeconds  int    `json:"member_stream_first_event_timeout_seconds" binding:"omitempty,min=1"`  // 单个成员返回首个有效流事件的超时秒数。
	MemberCooldownSeconds                 int    `json:"member_cooldown_seconds" binding:"omitempty,min=1"`                    // 单个成员耗尽尝试后被跳过的秒数, 动态选路模式(非手动)生效。
	MemberAffinitySeconds                 int    `json:"member_affinity_seconds" binding:"omitempty,min=0"`                    // 成员亲和时间:动态选路成功后继续保持当前成员的秒数;当前成员失败会立即结束亲和,0 表示不保持。
	PriceMetric                           string `json:"price_metric" binding:"omitempty,oneof=blended input output"`          // 低价优先与综合评分的价格口径: blended 读写均值, input 仅输入, output 仅输出。
	MetricWindowSize                      int    `json:"metric_window_size" binding:"omitempty,min=5,max=200"`                 // 延迟与成功率指标的样本窗口规模, 越大越平滑, 5-200。
	ScorePriceWeight                      int    `json:"score_price_weight" binding:"omitempty,min=0,max=100"`                 // 综合评分中价格的相对权重, 0-100; 0 表示未自定义, 运行时跟随系统默认设置。
	ScoreLatencyWeight                    int    `json:"score_latency_weight" binding:"omitempty,min=0,max=100"`               // 综合评分中延迟的相对权重, 0-100; 0 表示未自定义, 运行时跟随系统默认设置。
	ScoreSuccessWeight                    int    `json:"score_success_weight" binding:"omitempty,min=0,max=100"`               // 综合评分中成功率的相对权重, 0-100; 0 表示未自定义, 运行时跟随系统默认设置。
}

// DefaultGroupRelayConfig 返回手动模式的默认参数, 兼容没有模式上下文的调用方。
func DefaultGroupRelayConfig() GroupRelayConfig {
	return DefaultGroupRelayConfigForMode(GroupModeManual)
}

// DefaultGroupRelayConfigForMode 按策略给新分组设置默认参数; 已有分组的配置不会被改写。
func DefaultGroupRelayConfigForMode(mode GroupMode) GroupRelayConfig {
	config := GroupRelayConfig{
		MemberStreamIdleTimeoutSeconds:        60,
		RequestTimeoutSeconds:                 600,
		MaxWaitSeconds:                        30,
		MaxWaitingRequests:                    0,
		MaxTotalAttempts:                      10,
		MemberMaxAttempts:                     1, // 失败优先换成员, 避免同一故障端点连续阻塞。
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 0,
		PriceMetric:                           PriceMetricBlended,
		MetricWindowSize:                      20,
		// 权重 0 表示运行时使用系统默认设置, 而非固定拷贝一份旧默认。
		ScorePriceWeight:   0,
		ScoreLatencyWeight: 0,
		ScoreSuccessWeight: 0,
	}
	switch mode {
	case GroupModeFailover:
		config.MemberAffinitySeconds = 60 // 备用成员成功后短暂稳定, 再允许主成员恢复探测。
	case GroupModeLatency:
		config.MetricWindowSize = 10 // 对延迟变化反应较快。
	case GroupModeSuccess:
		config.MetricWindowSize = 30 // 成功率避免被个别失败过度扰动。
	case GroupModeRandom:
		config.MemberCooldownSeconds = 30 // 分散流量时较快恢复健康成员的采样。
	}
	return config
}

// NormalizeGroupRelayConfig 补齐没有模式上下文的旧调用方(手动模式默认)。
func NormalizeGroupRelayConfig(config *GroupRelayConfig) {
	NormalizeGroupRelayConfigForMode(config, GroupModeManual)
}

// NormalizeGroupRelayConfigForMode 按模式填充缺失配置, 显式设置的参数保持不变。
func NormalizeGroupRelayConfigForMode(config *GroupRelayConfig, mode GroupMode) {
	defaults := DefaultGroupRelayConfigForMode(mode)
	if *config == (GroupRelayConfig{}) {
		*config = defaults
		return
	}
	if config.MemberStreamIdleTimeoutSeconds < 1 {
		config.MemberStreamIdleTimeoutSeconds = defaults.MemberStreamIdleTimeoutSeconds
	}
	if config.RequestTimeoutSeconds < 1 {
		config.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if config.MaxWaitSeconds < 0 {
		config.MaxWaitSeconds = defaults.MaxWaitSeconds
	}
	if config.MaxWaitingRequests < 0 {
		config.MaxWaitingRequests = defaults.MaxWaitingRequests
	}
	if config.MaxTotalAttempts < 1 {
		config.MaxTotalAttempts = defaults.MaxTotalAttempts
	}
	config.RequestTimeoutSeconds = min(config.RequestTimeoutSeconds, 3600)
	config.MemberStreamIdleTimeoutSeconds = min(config.MemberStreamIdleTimeoutSeconds, 3600)
	config.MaxWaitSeconds = min(config.MaxWaitSeconds, 300)
	config.MaxWaitingRequests = min(config.MaxWaitingRequests, 1000)
	config.MaxTotalAttempts = min(config.MaxTotalAttempts, 100)
	if config.MemberMaxAttempts < 1 {
		config.MemberMaxAttempts = defaults.MemberMaxAttempts
	}
	if config.MemberRetryIntervalSeconds < 1 {
		config.MemberRetryIntervalSeconds = defaults.MemberRetryIntervalSeconds
	}
	if config.MemberNonStreamResponseTimeoutSeconds < 1 {
		config.MemberNonStreamResponseTimeoutSeconds = defaults.MemberNonStreamResponseTimeoutSeconds
	}
	if config.MemberStreamFirstEventTimeoutSeconds < 1 {
		config.MemberStreamFirstEventTimeoutSeconds = defaults.MemberStreamFirstEventTimeoutSeconds
	}
	if config.MemberCooldownSeconds < 1 {
		config.MemberCooldownSeconds = defaults.MemberCooldownSeconds
	}
	if config.MemberAffinitySeconds < 0 {
		config.MemberAffinitySeconds = defaults.MemberAffinitySeconds
	}
	if !IsValidPriceMetric(config.PriceMetric) {
		config.PriceMetric = defaults.PriceMetric
	}
	if config.MetricWindowSize < 5 || config.MetricWindowSize > 200 {
		config.MetricWindowSize = defaults.MetricWindowSize
	}
	config.ScorePriceWeight = min(max(config.ScorePriceWeight, 0), 100)
	config.ScoreLatencyWeight = min(max(config.ScoreLatencyWeight, 0), 100)
	config.ScoreSuccessWeight = min(max(config.ScoreSuccessWeight, 0), 100)
	// 权重按提交值保留: 0 表示未自定义, 运行时由 relay 侧按系统默认设置解析(相对权重, 无须归一)。
}

// 客户端模型名称及其可手动选择或故障转移的上游分组。
type Group struct {
	ID           int              `json:"id" gorm:"primaryKey"`                                                                                             // 分组主键。
	UserID       uint             `json:"user_id" gorm:"uniqueIndex:idx_group_owner_name;not null;default:0"`                                               // 归属用户; 分组名在归属内唯一, 跨用户可重名。
	Name         string           `json:"name" gorm:"uniqueIndex:idx_group_owner_name;not null"`                                                            // 客户端请求使用的模型名称。
	Mode         GroupMode        `json:"mode" gorm:"not null;default:manual" binding:"omitempty,oneof=manual failover price latency success score random"` // 选择成员的模式。
	ActiveItemID int              `json:"active_item_id" gorm:"not null;default:0"`                                                                         // 手动模式指定的成员, 故障转移模式忽略该值, 0 表示未指定; 写入侧字段, 读取一律用响应中的 runtime.current_item_id, 出 JSON 仅为让备份转储带上它。
	RelayConfig  GroupRelayConfig `json:"relay_config" gorm:"serializer:json"`                                                                              // 该分组的 Relay 路由配置。
	Items        []GroupItem      `json:"items" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`                                                      // 该分组可手动选择或故障转移的分组项; 读取时恒为数组, 空集合也给出以免各消费方各自兜底。
}

// 分组内一个可选择的渠道授权分组项。
// 读取时补齐授权两侧的名称, 所属渠道与可用性: 界面只需展示与排序, 由此无需再按主键回查渠道, 模型与凭据。
// 补齐的字段不含上游凭据本身, 转发所需的完整授权由 Relay 另行按主键取。
type GroupItem struct {
	ID             int           `json:"id" gorm:"primaryKey"`                                                         // 分组项主键。
	GroupID        int           `json:"group_id" gorm:"not null;index:idx_group_grant,unique"`                        // 所属分组 ID。
	ChannelGrantID int           `json:"channel_grant_id" gorm:"not null;index:idx_group_grant,unique"`                // 引用的渠道授权 ID。
	ChannelGrant   *ChannelGrant `json:"-" gorm:"foreignKey:ChannelGrantID;references:ID;constraint:OnDelete:CASCADE"` // 仅用于声明级联外键, 授权被删除时成员随之删除; 读取时不填充, 展示所需字段见下方。
	Priority       int           `json:"priority" gorm:"not null"`                                                     // Priority 决定界面展示和故障转移模式下的成员切换顺序。

	ChannelID      int      `json:"channel_id" gorm:"-"`       // 授权所属渠道 ID。
	ChannelKeyID   int      `json:"channel_key_id" gorm:"-"`   // 授权引用的凭据 ID, 供上游限流定位。
	ChannelModelID int      `json:"channel_model_id" gorm:"-"` // 授权引用的渠道模型 ID, 供上游限流定位。
	ChannelName    string   `json:"channel_name" gorm:"-"`     // 展示名: 已发布渠道一律显示发布唯一编码, 未发布渠道显示真名。
	ModelName      string   `json:"model_name" gorm:"-"`       // 授权引用的上游模型名称。
	KeyName        string   `json:"key_name" gorm:"-"`         // 授权引用的凭据名称; 非归属者视角为空(脱敏)。
	Protocols      Protocol `json:"protocols" gorm:"-"`        // 授权支持的协议位掩码。
	Available      bool     `json:"available" gorm:"-"`        // 渠道与凭据均启用且模型, 凭据均存在时为真; 为假表示该成员当前无法转发, 但仍需列出以便移除。
	SupplyPrice    LLMPrice `json:"supply_price" gorm:"-"`     // 供货价(四类)。
	UserPrice      LLMPrice `json:"user_price" gorm:"-"`       // 用户价(上浮后)。
	MediaSupply    MediaPrice `json:"media_supply" gorm:"-"`   // 媒体供货价, 仅模型 kind=image|video 时使用。
	UserMedia      MediaPrice `json:"user_media" gorm:"-"`     // 媒体用户价(上浮后)。
	Kind           MediaKind `json:"kind,omitempty" gorm:"-"`  // 模型媒体形态, 文本留空。
}

// 创建分组请求; 成员顺序即优先级顺序。
// 不收主键与当前成员: 分组主键由数据库分配, 当前成员在创建后另行指定。
type GroupCreateRequest struct {
	Name        string           `json:"name" binding:"required"`                                                           // 客户端请求使用的模型名称。
	Mode        GroupMode        `json:"mode" binding:"omitempty,oneof=manual failover price latency success score random"` // 选择成员的模式, 留空按手动。
	RelayConfig GroupRelayConfig `json:"relay_config"`                                                                      // Relay 路由配置, 零值由后端补默认。
	Items       []GroupItemInput `json:"items"`                                                                             // 初始成员集合。
}

// 分组普通配置, 成员和当前成员的变更请求; 分组主键走路径, 不进请求体。
// 当前成员是分组的一个普通可选字段, 与其余字段共用本请求: 它不需要独立的权限, 审计或并发粒度。
type GroupUpdateRequest struct {
	Name         *string           `json:"name,omitempty"`                                                                              // Name 仅在名称变更时发送。
	Mode         *GroupMode        `json:"mode,omitempty" binding:"omitempty,oneof=manual failover price latency success score random"` // Mode 仅在选择模式变更时发送。
	RelayConfig  *GroupRelayConfig `json:"relay_config,omitempty"`                                                                      // RelayConfig 仅在 Relay 配置变更时发送完整配置。
	Items        *[]GroupItemInput `json:"items,omitempty"`                                                                             // 新的成员集合, 整体替换; 提交顺序即优先级顺序。
	ActiveItemID *int              `json:"active_item_id,omitempty"`                                                                    // 手动模式指定的当前成员, 0 表示取消选择; 用指针以便与"未提交该字段"区分。
}

// 提交分组成员时按渠道授权主键引用。
// 授权在提交前已由渠道页面创建, 主键必然存在, 故成员无需按名称引用;
// 成员自身的主键不参与提交: 整体替换按授权主键匹配, 已有成员的主键与统计由后端保留。
type GroupItemInput struct {
	ChannelGrantID int `json:"channel_grant_id" binding:"required"` // 待引用的渠道授权 ID。
}

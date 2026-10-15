package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

type SettingKey string

const (
	SettingKeyProxyURL                SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval       SettingKey = "stats_save_interval"        // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval SettingKey = "model_info_update_interval" // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins        SettingKey = "cors_allow_origins"         // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyModelFilter             SettingKey = "model_filter"               // 渠道获取模型时的全局过滤表达式; 留空表示不过滤
	SettingKeyUpdateCheckEnabled      SettingKey = "update_check_enabled"       // 是否自动检查本分支 Release 更新
	SettingKeyUpdateCheckMinutes      SettingKey = "update_check_interval"      // 自动检查间隔(分钟)
	// GitHub 访问令牌: 只用来抬高 GitHub API 的匿名限流(60 次/小时), 可选。
	// 环境变量 OCTOPUS_GITHUB_PAT 仍优先, 设置项作为界面可改的后备, 免得改令牌还得进机器改 systemd。
	SettingKeyGithubToken SettingKey = "github_token"
	// 多用户与计费。
	SettingKeyMarkupRatio             SettingKey = "markup_ratio"               // 上浮比例: 用户价 = 供货价 × (1 + 上浮比例)
	SettingKeyRegisterUserEnabled     SettingKey = "register_user_enabled"      // 用户注册开关, 默认关
	SettingKeyRegisterResellerEnabled SettingKey = "register_reseller_enabled"  // 渠道商注册开关, 默认关
	SettingKeyRegisterApproval        SettingKey = "register_approval_required" // 注册后是否需要管理员审批
	SettingKeyBalanceReserveCap       SettingKey = "balance_reserve_output_cap" // 余额预扣估算的默认输出 token 上限。
	SettingKeyBalanceReserveImages    SettingKey = "balance_reserve_images"     // 生图预扣保守张数(无 N 时使用), 默认 4。
	SettingKeyMinBalance              SettingKey = "min_balance"                // 允许的最低余额, 0 表示不足即拒

	// 充值与支付(D1 易支付首发, 网关参数全部后台可配, 商户信息后配即可用)。
	SettingKeyEpayGatewayURL       SettingKey = "epay_gateway_url"        // 易支付网关地址, 如 https://pay.example.com; 留空表示支付未配置
	SettingKeyEpayPID              SettingKey = "epay_pid"                // 商户 ID
	SettingKeyEpayKey              SettingKey = "epay_key"                // 商户密钥(加密存储/掩码回显)
	SettingKeyEpayPayTypes         SettingKey = "epay_pay_types"          // 限定支付方式(逗号分隔: alipay,wechat,qq), 留空用网关默认
	SettingKeyRechargeNotifyBase   SettingKey = "recharge_notify_base"    // 对外回调基址(如 https://api.example.com), 留空按请求 Host 推断
	SettingKeyRechargeRate         SettingKey = "recharge_rate"           // 汇率: 1 余额币种(USD) 折多少 CNY, 默认 7.0
	SettingKeyRechargeMinCNY       SettingKey = "recharge_min_cny"        // 单笔最低充值金额(元), 默认 1
	SettingKeyRechargeAmountTiers  SettingKey = "recharge_amount_tiers"   // 充值弹窗快捷金额(元, 逗号分隔), 留空不出快捷按钮
	SettingKeyRechargeBonusEnabled SettingKey = "recharge_bonus_enabled"   // 满赠开关(D6), 默认关
	SettingKeyRechargeBonusRules   SettingKey = "recharge_bonus_rules"     // 满赠规则 "满X元送Y%" 紧凑格式: 100:5,500:10
	// 路由策略系统默认: 分组内未自定义(权重为 0)时按这里的配比执行。
	SettingKeyScorePriceWeight   SettingKey = "score_price_weight"   // 综合评分默认价格权重(0-100)
	SettingKeyScoreLatencyWeight SettingKey = "score_latency_weight" // 综合评分默认延迟权重(0-100)
	SettingKeyScoreSuccessWeight SettingKey = "score_success_weight" // 综合评分默认成功率权重(0-100)
	// 综合评分可靠性下限(0-100 百分比): 成功率低于该值的成员排到达标成员之后, 防止最便宜但常失败的成员长期霸占首位。
	SettingKeyScoreReliabilityFloor SettingKey = "score_reliability_floor"
	// 系统信息配置: 管理后台展示的站点身份信息与公告, 仅管理员可改。
	SettingKeySiteName                SettingKey = "site_name"                 // 站点名称, 留空则前端回退到默认标题
	SettingKeySiteDescription         SettingKey = "site_description"          // 站点描述, 展示在登录页与关于处
	SettingKeySiteContact             SettingKey = "site_contact"              // 联系方式, 供用户寻求支持
	SettingKeySiteAnnouncement        SettingKey = "site_announcement"         // 全局公告, 展示给全部登录用户, 留空不展示
	SettingKeySiteAnnouncementEnabled SettingKey = "site_announcement_enabled" // 公告开关
	SettingKeyMaintenanceMode         SettingKey = "maintenance_mode"          // 维护模式, 开启后非管理员登录将被拒绝
	SettingKeyMaintenanceNotice       SettingKey = "maintenance_notice"        // 维护提示文案
	// 日志生命周期与备份: 留存、脱敏与自动备份。
	SettingKeyLogRetentionDays      SettingKey = "log_retention_days"       // 调用日志留存天数, 默认 7。
	SettingKeyLogArchiveEnabled     SettingKey = "log_archive_enabled"      // 是否在清理前归档, 默认 true。
	SettingKeyLogStoreBody          SettingKey = "log_store_body"           // 是否保存请求/响应正文, 默认 true。
	SettingKeyLogMaskFields         SettingKey = "log_mask_fields"          // 正文脱敏字段(逗号分隔的 JSON key)。
	SettingKeyAutoBackupEnabled     SettingKey = "auto_backup_enabled"      // 自动备份开关, 默认 true。
	SettingKeyAutoBackupKeep        SettingKey = "auto_backup_keep"         // 自动备份保留份数, 默认 7。
	SettingKeyAutoBackupInterval    SettingKey = "auto_backup_interval"     // 自动备份间隔(小时), 默认 24。
	SettingKeyBackupEncrypt         SettingKey = "backup_encrypt"           // 备份加密开关, 默认 false; 口令在 config.json backup.passphrase。
	SettingKeyBackupRemote          SettingKey = "backup_remote"            // 备份异地推送开关, 默认 false; 地址与凭据在 config.json backup.remote_*。
	SettingKeyHealthCheckEnabled    SettingKey = "health_check_enabled"     // 渠道健康探测开关, 默认 false(可能产生上游费用)。
	SettingKeyHealthCheckMinutes    SettingKey = "health_check_interval"    // 渠道健康探测间隔(分钟), 默认 30。
	SettingKeyHealthLatencyMS       SettingKey = "health_latency_ms"        // 延迟告警阈值(毫秒), 0 表示关闭, 默认 1000。
	SettingKeyAlertWebhookURL       SettingKey = "alert_webhook_url"        // 告警 Webhook 地址, 留空仅站内通知。
	SettingKeyAlertDedupMinutes     SettingKey = "alert_dedup_minutes"      // 告警去重窗口(分钟), 同渠道同类型同原因在窗口内只告警一次, 默认 10。
	SettingKeyAlertFailStreak       SettingKey = "alert_fail_streak"        // 连续失败多少次才判定渠道宕机, 默认 3(抖动抑制)。
	SettingKeyAlertChannels         SettingKey = "alert_channels"           // 启用的告警渠道(逗号分隔): webhook,telegram,email, 默认仅 webhook。
	SettingKeyAlertTelegramBotToken SettingKey = "alert_telegram_bot_token" // Telegram Bot Token。
	SettingKeyAlertTelegramChatID   SettingKey = "alert_telegram_chat_id"   // Telegram Chat ID。
	SettingKeyAlertEmailSMTPHost    SettingKey = "alert_email_smtp_host"    // SMTP 服务器地址。
	SettingKeyAlertEmailSMTPPort    SettingKey = "alert_email_smtp_port"    // SMTP 端口。
	SettingKeyAlertEmailUsername    SettingKey = "alert_email_username"     // SMTP 登录账号。
	SettingKeyAlertEmailPassword    SettingKey = "alert_email_password"     // SMTP 登录密码。
	SettingKeyAlertEmailFrom        SettingKey = "alert_email_from"         // 发件人地址。
	SettingKeyAlertEmailTo          SettingKey = "alert_email_to"           // 收件人地址(逗号分隔)。
	// 探针鉴权: /metrics 默认开放(与 /healthz 一致, 便于本地抓取), 需要收敛时切成 bearer 并要求令牌。
	SettingKeyMetricsAuth  SettingKey = "metrics_auth"  // off | bearer; /healthz 恒不鉴权(存活探针)。
	SettingKeyMetricsToken SettingKey = "metrics_token" // bearer 模式下的抓取令牌, 内部键不外露。
	// 登录防爆破: 按 IP+用户名 计量连续失败次数, 超限后指数退避锁定。在设置页面上独立配置以兼容多实例共享存储的场景。
	SettingKeyLoginMaxAttempts    SettingKey = "login_max_attempts"    // 连续失败次数阈值, 默认 5; 0 表示不按次数锁定(但退避仍然生效)。
	SettingKeyLoginLockoutSeconds SettingKey = "login_lockout_seconds" // 首次锁定的基础秒数, 默认 900 (15 分钟); 每次额外失败后翻倍, 封顶 1 小时。
	// 熔断参数: 上游故障的计数阈值与隔离时长, 全部可调; 阈值 0 表示不按计数熔断(立即熔断与冷却仍生效)。
	SettingKeyBreakerKeyThreshold    SettingKey = "breaker_key_threshold"     // 凭据级熔断的错误次数阈值, 默认 5。
	SettingKeyBreakerKeyMinutes      SettingKey = "breaker_key_minutes"       // 凭据级熔断时长(分钟), 默认 5。
	SettingKeyBreakerKeyHardMinutes  SettingKey = "breaker_key_hard_minutes"  // 鉴权/额度错误的立即熔断时长(分钟), 默认 30。
	SettingKeyBreakerModelThreshold  SettingKey = "breaker_model_threshold"   // 渠道模型级熔断的错误次数阈值, 默认 5。
	SettingKeyBreakerModelMinutes    SettingKey = "breaker_model_minutes"     // 渠道模型级熔断时长(分钟), 默认 5。
	SettingKeyBreakerCoolAuthSeconds SettingKey = "breaker_cool_auth_seconds" // 换路冷却: 鉴权/额度错误后该成员冷却秒数, 默认 60。
	SettingKeyBreakerCoolSoftSeconds SettingKey = "breaker_cool_soft_seconds" // 换路冷却: 过载/模型缺失等软错误后该成员冷却秒数, 默认 30。

	// 内部键: 只存库不出任何读取接口, 也不进备份。
	SettingKeyAuthSecret SettingKey = "auth_secret"
)

// IsInternalSetting 内部键不出读取接口与备份。
// metrics_token 是抓取凭据: 一旦随 /setting/list 外露, 鉴权即形同虚设, 故与 auth_secret 同等对待。
func IsInternalSetting(key SettingKey) bool {
	return key == SettingKeyAuthSecret || key == SettingKeyMetricsToken
}

// TokenConfiguredMask 是密钥类设置项回显给界面的占位符: 真实值不出接口,
// 界面据此知道"已配置", 提交该占位符时后端保持原值不变。
const TokenConfiguredMask = "__configured__"

// IsSecretSetting 是需要占位回显的密钥类设置项。与 IsInternalSetting 的区别:
// 内部键(read-only)连键都不出现, 这里要出现但值被替换成占位符。
func IsSecretSetting(key SettingKey) bool {
	return key == SettingKeyGithubToken ||
		key == SettingKeyAlertTelegramBotToken ||
		key == SettingKeyAlertEmailPassword ||
		key == SettingKeyEpayKey
}

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},          // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},             // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"},    // 默认24小时更新一次模型信息
		{Key: SettingKeyModelFilter, Value: ""},                  // 默认不过滤模型
		{Key: SettingKeyUpdateCheckEnabled, Value: "true"},       // 默认开启更新检查
		{Key: SettingKeyUpdateCheckMinutes, Value: "60"},         // 每小时检查一次
		{Key: SettingKeyGithubToken, Value: ""},                  // 默认匿名访问 GitHub API, 可在此填令牌抬高限流
		{Key: SettingKeyMarkupRatio, Value: "0.2"},               // 默认上浮 20%
		{Key: SettingKeyRegisterUserEnabled, Value: "false"},     // 用户注册默认关闭
		{Key: SettingKeyRegisterResellerEnabled, Value: "false"}, // 渠道商注册默认关闭
		{Key: SettingKeyRegisterApproval, Value: "true"},         // 注册默认需要审批
		{Key: SettingKeyBalanceReserveCap, Value: "4096"},        // 预扣估算默认输出上限
		{Key: SettingKeyBalanceReserveImages, Value: "4"},        // 生图预扣保守张数, 4 张覆盖 dall-e-3 / gpt-image-1 n=4 上限
		{Key: SettingKeyMinBalance, Value: "0"},                  // 不允许透支
		// 充值与支付: 全部留空/默认, 商户信息在管理端补上即启用。
		{Key: SettingKeyEpayGatewayURL, Value: ""},              // 留空表示支付未配置, 充值入口优雅降级
		{Key: SettingKeyEpayPID, Value: ""},                     // 商户 ID 后配
		{Key: SettingKeyEpayKey, Value: ""},                     // 商户密钥后配(密钥类设置)
		{Key: SettingKeyEpayPayTypes, Value: ""},                // 不限定支付方式
		{Key: SettingKeyRechargeNotifyBase, Value: ""},          // 留空按请求 Host 推断回调地址
		{Key: SettingKeyRechargeRate, Value: "7.0"},             // 1 USD = 7 CNY(D2)
		{Key: SettingKeyRechargeMinCNY, Value: "1"},             // 单笔最低 1 元
		{Key: SettingKeyRechargeAmountTiers, Value: "10,50,100,500"}, // 弹窗快捷金额(元)
		{Key: SettingKeyRechargeBonusEnabled, Value: "false"},   // 满赠默认关(D6)
		{Key: SettingKeyRechargeBonusRules, Value: ""},          // 满赠规则, 如 100:5,500:10
		{Key: SettingKeyScorePriceWeight, Value: "40"},           // 综合评分默认配比: 价格 40
		{Key: SettingKeyScoreLatencyWeight, Value: "30"},         // 综合评分默认配比: 延迟 30
		{Key: SettingKeyScoreSuccessWeight, Value: "30"},         // 综合评分默认配比: 成功率 30
		{Key: SettingKeyScoreReliabilityFloor, Value: "70"},      // 成功率低于 70% 视为不健康, 评分排后
		{Key: SettingKeySiteName, Value: ""},                     // 站点名称默认空, 前端回退默认标题
		{Key: SettingKeySiteDescription, Value: ""},              // 站点描述默认空
		{Key: SettingKeySiteContact, Value: ""},                  // 联系方式默认空
		{Key: SettingKeySiteAnnouncement, Value: ""},             // 公告默认空
		{Key: SettingKeySiteAnnouncementEnabled, Value: "false"}, // 公告默认关闭
		{Key: SettingKeyMaintenanceMode, Value: "false"},         // 维护模式默认关闭
		{Key: SettingKeyMaintenanceNotice, Value: ""},            // 维护提示默认空
		{Key: SettingKeyLogRetentionDays, Value: "7"},            // 日志留存 7 天
		{Key: SettingKeyLogArchiveEnabled, Value: "true"},        // 清理前先归档
		{Key: SettingKeyLogStoreBody, Value: "true"},             // 默认保存正文
		{Key: SettingKeyLogMaskFields, Value: ""},                // 默认不额外脱敏
		{Key: SettingKeyAutoBackupEnabled, Value: "true"},        // 默认开启自动备份
		{Key: SettingKeyAutoBackupKeep, Value: "7"},              // 保留 7 份
		{Key: SettingKeyAutoBackupInterval, Value: "24"},         // 每 24 小时一次
		{Key: SettingKeyBackupEncrypt, Value: "false"},           // 备份默认不加密
		{Key: SettingKeyBackupRemote, Value: "false"},            // 备份默认不推异地
		{Key: SettingKeyHealthCheckEnabled, Value: "false"},      // 健康探测默认关闭(可能产生上游费用)
		{Key: SettingKeyHealthCheckMinutes, Value: "30"},         // 探测间隔 30 分钟
		{Key: SettingKeyHealthLatencyMS, Value: "1000"},          // 延迟告警阈值 1 秒, 0 关闭
		{Key: SettingKeyAlertWebhookURL, Value: ""},              // 默认仅站内通知
		{Key: SettingKeyAlertDedupMinutes, Value: "10"},          // 去重窗口 10 分钟
		{Key: SettingKeyAlertFailStreak, Value: "3"},             // 连续 3 次失败判宕机
		// 告警渠道与投递凭据: 必须在默认值里播种, 否则设置键不会落库, 读取与写入都会以 "setting not found" 失败。
		{Key: SettingKeyAlertChannels, Value: ""},            // 留空等同仅 webhook, 与后端回落一致
		{Key: SettingKeyAlertTelegramBotToken, Value: ""},    // Telegram Bot Token, 留空表示未配置
		{Key: SettingKeyAlertTelegramChatID, Value: ""},      // Telegram Chat ID, 留空表示未配置
		{Key: SettingKeyAlertEmailSMTPHost, Value: ""},       // SMTP 服务器地址, 留空表示未配置
		{Key: SettingKeyAlertEmailSMTPPort, Value: "587"},    // 默认 587(STARTTLS), 与发送端缺省一致
		{Key: SettingKeyAlertEmailUsername, Value: ""},       // SMTP 登录账号
		{Key: SettingKeyAlertEmailPassword, Value: ""},       // SMTP 登录密码
		{Key: SettingKeyAlertEmailFrom, Value: ""},           // 发件人地址
		{Key: SettingKeyAlertEmailTo, Value: ""},             // 收件人地址(逗号分隔)
		{Key: SettingKeyMetricsAuth, Value: "off"},           // 探针默认开放
		{Key: SettingKeyMetricsToken, Value: ""},             // 令牌留空, 开启鉴权时自动生成
		{Key: SettingKeyLoginMaxAttempts, Value: "5"},        // 连续 5 次失败即触发退避锁定
		{Key: SettingKeyLoginLockoutSeconds, Value: "900"},   // 锁定 15 分钟, 起效前每超一次翻倍
		{Key: SettingKeyBreakerKeyThreshold, Value: "5"},     // 连续 5 次错误熔断该凭据
		{Key: SettingKeyBreakerKeyMinutes, Value: "5"},       // 凭据熔断 5 分钟
		{Key: SettingKeyBreakerKeyHardMinutes, Value: "30"},  // 鉴权/额度错误立即熔断 30 分钟
		{Key: SettingKeyBreakerModelThreshold, Value: "5"},   // 连续 5 次错误熔断该渠道模型
		{Key: SettingKeyBreakerModelMinutes, Value: "5"},     // 渠道模型熔断 5 分钟
		{Key: SettingKeyBreakerCoolAuthSeconds, Value: "60"}, // 鉴权/额度成员冷却 60 秒
		{Key: SettingKeyBreakerCoolSoftSeconds, Value: "30"}, // 过载/模型缺失成员冷却 30 秒

	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyMarkupRatio:
		ratio, err := strconv.ParseFloat(s.Value, 64)
		if err != nil || ratio < 0 || ratio > 100 {
			return fmt.Errorf("markup ratio must be a number between 0 and 100")
		}
		return nil
	case SettingKeyRegisterUserEnabled, SettingKeyRegisterResellerEnabled, SettingKeyRegisterApproval, SettingKeyUpdateCheckEnabled:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("%s must be a boolean", s.Key)
		}
		return nil
	case SettingKeyBalanceReserveCap:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 {
			return fmt.Errorf("balance reserve output cap must be a non-negative integer")
		}
		return nil
	case SettingKeyBalanceReserveImages:
		// 1~64 张是合理窗口, 过低冻结偏小容易触发预扣后的余额告警, 过大则过度冻结。
		if n, err := strconv.Atoi(s.Value); err != nil || n < 1 || n > 64 {
			return fmt.Errorf("balance reserve images must be an integer between 1 and 64")
		}
		return nil
	case SettingKeyMinBalance:
		if _, err := strconv.ParseFloat(s.Value, 64); err != nil {
			return fmt.Errorf("min balance must be a number")
		}
		return nil
	case SettingKeyRechargeRate:
		// 汇率必须为正且在常识区间, 0 或负值会让充值金额算出负入账。
		rate, err := strconv.ParseFloat(s.Value, 64)
		if err != nil || rate < 0.1 || rate > 1000 {
			return fmt.Errorf("recharge rate must be a number between 0.1 and 1000")
		}
		return nil
	case SettingKeyRechargeMinCNY:
		if amount, err := strconv.ParseFloat(s.Value, 64); err != nil || amount < 0 {
			return fmt.Errorf("minimum recharge amount must be a non-negative number")
		}
		return nil
	case SettingKeyRechargeBonusEnabled:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("%s must be a boolean", s.Key)
		}
		return nil
	case SettingKeyRechargeBonusRules:
		if _, err := ParseBonusRules(s.Value); err != nil {
			return err
		}
		return nil
	case SettingKeyRechargeAmountTiers:
		if _, err := ParseAmountTiers(s.Value); err != nil {
			return err
		}
		return nil
	case SettingKeyScorePriceWeight, SettingKeyScoreLatencyWeight, SettingKeyScoreSuccessWeight,
		SettingKeyScoreReliabilityFloor:
		weight, err := strconv.Atoi(s.Value)
		if err != nil || weight < 0 || weight > 100 {
			return fmt.Errorf("score weight must be an integer between 0 and 100")
		}
		return nil
	case SettingKeySiteAnnouncementEnabled, SettingKeyMaintenanceMode:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("%s must be a boolean", s.Key)
		}
		return nil
	case SettingKeyLogRetentionDays, SettingKeyAutoBackupKeep, SettingKeyAutoBackupInterval, SettingKeyHealthCheckMinutes:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 1 || n > 3650 {
			return fmt.Errorf("%s must be a positive integer", s.Key)
		}
		return nil
	case SettingKeyUpdateCheckMinutes:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 1 || n > 1440 {
			return fmt.Errorf("update check interval must be an integer between 1 and 1440 minutes")
		}
		return nil
	case SettingKeyHealthLatencyMS:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 || n > 600000 {
			return fmt.Errorf("%s must be a non-negative integer (max 600000)", s.Key)
		}
		return nil
	case SettingKeyLogArchiveEnabled, SettingKeyLogStoreBody, SettingKeyAutoBackupEnabled, SettingKeyHealthCheckEnabled, SettingKeyBackupEncrypt, SettingKeyBackupRemote:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("%s must be a boolean", s.Key)
		}
		return nil
	case SettingKeyAlertDedupMinutes:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 || n > 1440 {
			return fmt.Errorf("alert dedup minutes must be an integer between 0 and 1440")
		}
		return nil
	case SettingKeyAlertFailStreak:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 1 || n > 100 {
			return fmt.Errorf("alert fail streak must be an integer between 1 and 100")
		}
	case SettingKeyAlertChannels:
		// 逗号分隔的渠道列表, 允许为空
		return nil
	case SettingKeyAlertTelegramBotToken, SettingKeyAlertTelegramChatID,
		SettingKeyAlertEmailSMTPHost, SettingKeyAlertEmailUsername,
		SettingKeyAlertEmailPassword, SettingKeyAlertEmailFrom, SettingKeyAlertEmailTo:
		// 字符串配置, 允许为空
		return nil
	case SettingKeyAlertEmailSMTPPort:
		port, err := strconv.Atoi(s.Value)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid SMTP port: must be 1-65535")
		}
		return nil
	case SettingKeyBreakerKeyThreshold, SettingKeyBreakerModelThreshold:
		// 0 关闭计数熔断(只剩立即熔断与冷却), 上限 100 次以免阈值形同虚设。
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 || n > 100 {
			return fmt.Errorf("breaker error threshold must be an integer between 0 and 100")
		}
		return nil
	case SettingKeyBreakerKeyMinutes, SettingKeyBreakerModelMinutes:
		// 至少 1 分钟, 上限一天; 熔断时长为 0 等于不熔断, 应改用阈值 0。
		if n, err := strconv.Atoi(s.Value); err != nil || n < 1 || n > 1440 {
			return fmt.Errorf("breaker duration must be an integer between 1 and 1440 minutes")
		}
		return nil
	case SettingKeyBreakerKeyHardMinutes:
		// 鉴权与额度的立即熔断至少 1 分钟, 上限一天。
		if n, err := strconv.Atoi(s.Value); err != nil || n < 1 || n > 1440 {
			return fmt.Errorf("breaker hard duration must be an integer between 1 and 1440 minutes")
		}
		return nil
	case SettingKeyBreakerCoolAuthSeconds, SettingKeyBreakerCoolSoftSeconds:
		// 冷却只防本轮重复选中, 上限 10 分钟。
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 || n > 600 {
			return fmt.Errorf("breaker cooldown must be an integer between 0 and 600 seconds")
		}
		return nil
	case SettingKeyMetricsAuth:
		if s.Value != "off" && s.Value != "bearer" {
			return fmt.Errorf("metrics auth must be off or bearer")
		}
		return nil
	case SettingKeyMetricsToken:
		return nil
	case SettingKeyAlertWebhookURL:
		if s.Value == "" {
			return nil
		}
		// 只允许 http(s), 避免把种子协议交给容器内发起请求。
		if !strings.HasPrefix(s.Value, "http://") && !strings.HasPrefix(s.Value, "https://") {
			return fmt.Errorf("alert webhook url must start with http:// or https://")
		}
		if len(s.Value) > 500 {
			return fmt.Errorf("alert webhook url is too long")
		}
		return nil
	case SettingKeyLogMaskFields:
		if len(s.Value) > 2000 {
			return fmt.Errorf("%s must not exceed 2000 characters", s.Key)
		}
		return nil
	case SettingKeySiteName, SettingKeySiteDescription, SettingKeySiteContact, SettingKeySiteAnnouncement, SettingKeyMaintenanceNotice:
		if len([]rune(s.Value)) > 2000 {
			return fmt.Errorf("%s must not exceed 2000 characters", s.Key)
		}
		return nil
	case SettingKeyModelInfoUpdateInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyModelFilter:
		if s.Value == "" {
			return nil
		}
		// 与渠道侧一致用 ECMAScript 方言校验, 避免设置能存但探测时编译失败。
		if _, err := regexp2.Compile(s.Value, regexp2.ECMAScript); err != nil {
			return fmt.Errorf("model filter regex is invalid: %w", err)
		}
		return nil
	case SettingKeyLoginMaxAttempts:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 || n > 100 {
			return fmt.Errorf("login max attempts must be an integer between 0 and 100")
		}
		return nil
	case SettingKeyLoginLockoutSeconds:
		if n, err := strconv.Atoi(s.Value); err != nil || n < 0 || n > 86400 {
			return fmt.Errorf("login lockout seconds must be an integer between 0 and 86400")
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":    true,
			"https":   true,
			"socks5":  true,
			"socks5h": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	}

	return nil
}

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
	// 多用户与计费。
	SettingKeyMarkupRatio             SettingKey = "markup_ratio"               // 上浮比例: 用户价 = 供货价 × (1 + 上浮比例)
	SettingKeyRegisterUserEnabled     SettingKey = "register_user_enabled"      // 用户注册开关, 默认关
	SettingKeyRegisterResellerEnabled SettingKey = "register_reseller_enabled"  // 渠道商注册开关, 默认关
	SettingKeyRegisterApproval        SettingKey = "register_approval_required" // 注册后是否需要管理员审批
	SettingKeyBalanceReserveCap       SettingKey = "balance_reserve_output_cap" // 余额预扣估算的默认输出 token 上限。
	SettingKeyBalanceReserveImages    SettingKey = "balance_reserve_images"     // 生图预扣保守张数(无 N 时使用), 默认 4。
	SettingKeyMinBalance              SettingKey = "min_balance"                // 允许的最低余额, 0 表示不足即拒
	// 路由策略系统默认: 分组内未自定义(权重为 0)时按这里的配比执行。
	SettingKeyScorePriceWeight   SettingKey = "score_price_weight"   // 综合评分默认价格权重(0-100)
	SettingKeyScoreLatencyWeight SettingKey = "score_latency_weight" // 综合评分默认延迟权重(0-100)
	SettingKeyScoreSuccessWeight SettingKey = "score_success_weight" // 综合评分默认成功率权重(0-100)
	// 系统信息配置: 管理后台展示的站点身份信息与公告, 仅管理员可改。
	SettingKeySiteName                SettingKey = "site_name"                 // 站点名称, 留空则前端回退到默认标题
	SettingKeySiteDescription         SettingKey = "site_description"          // 站点描述, 展示在登录页与关于处
	SettingKeySiteContact             SettingKey = "site_contact"              // 联系方式, 供用户寻求支持
	SettingKeySiteAnnouncement        SettingKey = "site_announcement"         // 全局公告, 展示给全部登录用户, 留空不展示
	SettingKeySiteAnnouncementEnabled SettingKey = "site_announcement_enabled" // 公告开关
	SettingKeyMaintenanceMode         SettingKey = "maintenance_mode"          // 维护模式, 开启后非管理员登录将被拒绝
	SettingKeyMaintenanceNotice       SettingKey = "maintenance_notice"        // 维护提示文案
	// 日志生命周期与备份: 留存、脱敏与自动备份。
	SettingKeyLogRetentionDays   SettingKey = "log_retention_days"    // 调用日志留存天数, 默认 7。
	SettingKeyLogArchiveEnabled  SettingKey = "log_archive_enabled"   // 是否在清理前归档, 默认 true。
	SettingKeyLogStoreBody       SettingKey = "log_store_body"        // 是否保存请求/响应正文, 默认 true。
	SettingKeyLogMaskFields      SettingKey = "log_mask_fields"       // 正文脱敏字段(逗号分隔的 JSON key)。
	SettingKeyAutoBackupEnabled  SettingKey = "auto_backup_enabled"   // 自动备份开关, 默认 true。
	SettingKeyAutoBackupKeep     SettingKey = "auto_backup_keep"      // 自动备份保留份数, 默认 7。
	SettingKeyAutoBackupInterval SettingKey = "auto_backup_interval"  // 自动备份间隔(小时), 默认 24。
	SettingKeyBackupEncrypt      SettingKey = "backup_encrypt"        // 备份加密开关, 默认 false; 口令在 config.json backup.passphrase。
	SettingKeyBackupRemote       SettingKey = "backup_remote"         // 备份异地推送开关, 默认 false; 地址与凭据在 config.json backup.remote_*。
	SettingKeyHealthCheckEnabled SettingKey = "health_check_enabled"  // 渠道健康探测开关, 默认 false(可能产生上游费用)。
	SettingKeyHealthCheckMinutes SettingKey = "health_check_interval" // 渠道健康探测间隔(分钟), 默认 30。
	SettingKeyHealthLatencyMS    SettingKey = "health_latency_ms"     // 延迟告警阈值(毫秒), 0 表示关闭, 默认 1000。
	SettingKeyAlertWebhookURL    SettingKey = "alert_webhook_url"     // 告警 Webhook 地址, 留空仅站内通知。
	SettingKeyAlertDedupMinutes  SettingKey = "alert_dedup_minutes"   // 告警去重窗口(分钟), 同渠道同类型同原因在窗口内只告警一次, 默认 10。
	SettingKeyAlertFailStreak    SettingKey = "alert_fail_streak"     // 连续失败多少次才判定渠道宕机, 默认 3(抖动抑制)。
	// 探针鉴权: /metrics 默认开放(与 /healthz 一致, 便于本地抓取), 需要收敛时切成 bearer 并要求令牌。
	SettingKeyMetricsAuth  SettingKey = "metrics_auth"  // off | bearer; /healthz 恒不鉴权(存活探针)。
	SettingKeyMetricsToken SettingKey = "metrics_token" // bearer 模式下的抓取令牌, 内部键不外露。
	// 内部键: 只存库不出任何读取接口, 也不进备份。
	SettingKeyAuthSecret SettingKey = "auth_secret"
)

// IsInternalSetting 内部键不出读取接口与备份。
// metrics_token 是抓取凭据: 一旦随 /setting/list 外露, 鉴权即形同虚设, 故与 auth_secret 同等对待。
func IsInternalSetting(key SettingKey) bool {
	return key == SettingKeyAuthSecret || key == SettingKeyMetricsToken
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
		{Key: SettingKeyMarkupRatio, Value: "0.2"},               // 默认上浮 20%
		{Key: SettingKeyRegisterUserEnabled, Value: "false"},     // 用户注册默认关闭
		{Key: SettingKeyRegisterResellerEnabled, Value: "false"}, // 渠道商注册默认关闭
		{Key: SettingKeyRegisterApproval, Value: "true"},         // 注册默认需要审批
		{Key: SettingKeyBalanceReserveCap, Value: "4096"},        // 预扣估算默认输出上限
		{Key: SettingKeyBalanceReserveImages, Value: "4"},        // 生图预扣保守张数, 4 张覆盖 dall-e-3 / gpt-image-1 n=4 上限
		{Key: SettingKeyMinBalance, Value: "0"},                  // 不允许透支
		{Key: SettingKeyScorePriceWeight, Value: "40"},           // 综合评分默认配比: 价格 40
		{Key: SettingKeyScoreLatencyWeight, Value: "30"},         // 综合评分默认配比: 延迟 30
		{Key: SettingKeyScoreSuccessWeight, Value: "30"},         // 综合评分默认配比: 成功率 30
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
		{Key: SettingKeyMetricsAuth, Value: "off"},               // 探针默认开放
		{Key: SettingKeyMetricsToken, Value: ""},                 // 令牌留空, 开启鉴权时自动生成
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
	case SettingKeyScorePriceWeight, SettingKeyScoreLatencyWeight, SettingKeyScoreSuccessWeight:
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

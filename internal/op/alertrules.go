package op

import (
	"crypto/rand"
	"encoding/hex"
	"sort"

	"github.com/bestruirui/octopus/internal/model"
)

// AlertRule 一条生效中的告警规则: 可配置项都同时给出当前值与默认值, 使"界面上看到的数字"
// 与"引擎真正使用的数字"永不脱节, 也让运维无需读代码即可判断哪些规则被改过。
type AlertRule struct {
	Key         string `json:"key"`
	Value       int    `json:"value"`
	Default     int    `json:"default"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
}

// AlertRules 返回全部生效中的告警规则(去重窗口、抖动抑制、延迟阈值), 供运维面板展示与核对。
func AlertRules() []AlertRule {
	rules := []AlertRule{
		{
			Key:         string(model.SettingKeyAlertDedupMinutes),
			Value:       SettingGetIntDefault(model.SettingKeyAlertDedupMinutes, alertDedupDefaultMinutes),
			Default:     alertDedupDefaultMinutes,
			Unit:        "minutes",
			Description: "同渠道同类型同原因在该窗口内只告警一次; 0 表示不去重",
		},
		{
			Key:         string(model.SettingKeyAlertFailStreak),
			Value:       SettingGetIntDefault(model.SettingKeyAlertFailStreak, alertFailStreakDefault),
			Default:     alertFailStreakDefault,
			Unit:        "probes",
			Description: "连续失败多少次才判定渠道宕机(抖动抑制)",
		},
		{
			Key:         string(model.SettingKeyHealthLatencyMS),
			Value:       SettingGetIntDefault(model.SettingKeyHealthLatencyMS, 1000),
			Default:     1000,
			Unit:        "milliseconds",
			Description: "探测延迟超过该值发 degraded; 0 表示关闭延迟告警",
		},
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Key < rules[j].Key })
	return rules
}

// MetricsAuthMode 返回 /metrics 当前生效的鉴权模式: off(默认开放) 或 bearer(需令牌)。
func MetricsAuthMode() string {
	mode, err := SettingGetString(model.SettingKeyMetricsAuth)
	if err != nil || mode != "bearer" {
		return "off"
	}
	return "bearer"
}

// MetricsTokenEnsure 返回当前抓取令牌, 为空时生成一个并落库。
// 令牌是内部键: 不进设置列表、不进备份, 丢失时由管理员在运维面板重新生成即可, 恢复后立即生效。
func MetricsTokenEnsure() (string, error) {
	if token, err := SettingGetString(model.SettingKeyMetricsToken); err == nil && token != "" {
		return token, nil
	}
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buffer)
	if err := SettingSetString(model.SettingKeyMetricsToken, token); err != nil {
		return "", err
	}
	return token, nil
}

// MetricsTokenRotate 重新生成抓取令牌, 用于凭据轮换; 旧令牌立即失效。
func MetricsTokenRotate() (string, error) {
	if err := SettingSetString(model.SettingKeyMetricsToken, ""); err != nil {
		return "", err
	}
	return MetricsTokenEnsure()
}

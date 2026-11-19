package handlers

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestAuditSettingValueMasksSecrets 守护审计脱敏: 密钥类/内部设置值绝不写原文进审计日志。
// 审计长期留存且随备份外传, 写入令牌等于把凭据铺开到每一份备份副本。
// 这条一旦被改坏不会有任何报错, 只会在审计表里静静地出现明文令牌。
func TestAuditSettingValueMasksSecrets(t *testing.T) {
	secret := "test_token_never_logged"
	for _, key := range []model.SettingKey{
		model.SettingKeyGithubToken,
		model.SettingKeyAlertTelegramBotToken,
		model.SettingKeyAlertEmailPassword,
		model.SettingKeyAuthSecret,
		model.SettingKeyMetricsToken,
	} {
		got := auditSettingValue(model.Setting{Key: key, Value: secret})
		if got == secret {
			t.Errorf("密钥设置项 %q 的审计值是原文: 明文落库", key)
		}
		if got != model.TokenConfiguredMask {
			t.Errorf("密钥设置项 %q 的审计值应为占位符, 得到 %q", key, got)
		}
	}

	// 普通设置项不受影响, 否则审计就没有可读性可言。
	plain := auditSettingValue(model.Setting{Key: model.SettingKeyProxyURL, Value: "http://127.0.0.1:7890"})
	if plain != "http://127.0.0.1:7890" {
		t.Errorf("普通设置项的审计值被改动: %q", plain)
	}
}

// TestResponseSettingValueMasksSecrets 守护接口回显脱敏: 写接口不能把刚提交的真值原样送回
// 浏览器与沿途代理日志。/list 的掩码是既有约定(占位符提交表示"没改"), 写接口回显同口径。
func TestResponseSettingValueMasksSecrets(t *testing.T) {
	secret := "test_token_never_echoed"
	for _, key := range []model.SettingKey{
		model.SettingKeyEpayKey,
		model.SettingKeyStripeSecretKey,
		model.SettingKeyGithubToken,
		model.SettingKeyAuthSecret,
		model.SettingKeyMetricsToken,
	} {
		got := responseSettingValue(model.Setting{Key: key, Value: secret})
		if got == secret {
			t.Errorf("密钥设置项 %q 的写接口回显是原文", key)
		}
		if got != model.TokenConfiguredMask {
			t.Errorf("密钥设置项 %q 的写接口回显应为占位符, 得到 %q", key, got)
		}
	}

	// 清空(空值)与普通设置项回原样: 界面需要知道"现在是空的/改成了什么"。
	if got := responseSettingValue(model.Setting{Key: model.SettingKeyEpayKey, Value: ""}); got != "" {
		t.Errorf("清空密钥的回显应为空串, 得到 %q", got)
	}
	plain := responseSettingValue(model.Setting{Key: model.SettingKeySiteName, Value: "my-site"})
	if plain != "my-site" {
		t.Errorf("普通设置项的回显被改动: %q", plain)
	}
}

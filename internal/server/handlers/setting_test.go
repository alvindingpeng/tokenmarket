package handlers

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestAuditSettingValueMasksSecrets 守护审计脱敏: 密钥类设置值绝不写原文进审计日志。
// 审计长期留存且随备份外传, 写入令牌等于把凭据铺开到每一份备份副本。
// 这条一旦被改坏不会有任何报错, 只会在审计表里静静地出现明文令牌。
func TestAuditSettingValueMasksSecrets(t *testing.T) {
	secret := "ghp_this_must_never_be_logged"
	for _, key := range []model.SettingKey{
		model.SettingKeyGithubToken,
		model.SettingKeyAlertTelegramBotToken,
		model.SettingKeyAlertEmailPassword,
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

package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestMetricsAuthDefaultIsBearer(t *testing.T) {
	for _, setting := range DefaultSettings() {
		if setting.Key == SettingKeyMetricsAuth && setting.Value != "bearer" {
			t.Fatalf("metrics_auth default = %q, want bearer", setting.Value)
		}
	}
}

func TestSettingValidateFinite(t *testing.T) {
	cases := []struct {
		key     SettingKey
		value   string
		wantErr bool
	}{
		{SettingKeyMarkupRatio, "NaN", true},
		{SettingKeyMarkupRatio, "100", false},
		{SettingKeyMinBalance, "Inf", true},
		{SettingKeyMinBalance, "0", false},
	}
	for _, tc := range cases {
		setting := Setting{Key: tc.key, Value: tc.value}
		err := setting.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s=%s error=%v wantErr=%v", tc.key, tc.value, err, tc.wantErr)
		}
	}

	// Gemini client_secret 验证: 必须非空且长度足够
	secretCases := []struct {
		value   string
		wantErr bool
	}{
		{"", true},
		{"short", true},
		{"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", false},
	}
	for _, tc := range secretCases {
		setting := Setting{Key: SettingKeyGeminiOAuthClientSecret, Value: tc.value}
		err := setting.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("GeminiOAuthClientSecret=%s error=%v wantErr=%v", tc.value, err, tc.wantErr)
		}
	}
}

// TestDefaultSettingsCoversEveryKey 守护设置键的播种完整性。
// 只有出现在 DefaultSettings() 里的键才会在启动时落库; 漏播种的键不存在于 settingCache,
// SettingGetString 与 SettingSetString 会一律返回 "setting not found" ——
// 表现为前端能填能点, 保存却必然失败。这里直接解析源码常量, 新增键时无需手工维护清单。
func TestDefaultSettingsCoversEveryKey(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "setting.go", nil, 0)
	if err != nil {
		t.Fatalf("parse setting.go: %v", err)
	}

	seeded := make(map[SettingKey]bool)
	for _, setting := range DefaultSettings() {
		seeded[setting.Key] = true
	}

	declared := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// 常量块允许省略类型, 沿用上一条的类型。
		var lastType string
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := value.Type.(*ast.Ident); ok {
				lastType = ident.Name
			}
			if lastType != "SettingKey" {
				continue
			}
			for _, expr := range value.Values {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				raw, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				declared++
				key := SettingKey(raw)
				// 内部键(如 auth_secret)由启动流程自行生成, 不进默认值。
				if IsInternalSetting(key) {
					continue
				}
				if !seeded[key] {
					t.Errorf("设置键 %q 未在 DefaultSettings() 中播种: 该键不会落库, 读写都会以 \"setting not found\" 失败", raw)
				}
			}
		}
	}

	if declared == 0 {
		t.Fatal("未从 setting.go 解析到任何 SettingKey 常量, 该守护测试已失效")
	}
}

// TestSecretSettingsAreMaskedNotHidden 守护密钥类设置项的两条约束。
// 1) 必须落库(不能同时被当成内部键隐藏), 否则界面上的输入框永远保存失败;
// 2) 必须带占位回显, 不把真值下发到浏览器。
// 这两条一旦被改坏, 表现分别是「填了令牌没反应」和「令牌被任何管理员会话读走」, 都不会报错。
func TestSecretSettingsAreMaskedNotHidden(t *testing.T) {
	seeded := make(map[SettingKey]bool)
	for _, setting := range DefaultSettings() {
		seeded[setting.Key] = true
	}

	secretKeys := []SettingKey{
		SettingKeyGithubToken,
		SettingKeyAlertTelegramBotToken,
		SettingKeyAlertEmailPassword,
	}
	for _, key := range secretKeys {
		if !seeded[key] {
			t.Errorf("密钥设置项 %q 未播种: 保存会以 setting not found 失败", key)
		}
		if IsInternalSetting(key) {
			t.Errorf("密钥设置项 %q 被当成内部键隐藏: 界面上将无从配置", key)
		}
		if !IsSecretSetting(key) {
			t.Errorf("密钥设置项 %q 未标记为需要占位回显: 真值会被下发到浏览器", key)
		}
	}

	if IsSecretSetting(SettingKeyProxyURL) {
		t.Error("普通设置项被标记为密钥类: 界面将永远看不到代理地址, 无从修改")
	}
	if TokenConfiguredMask == "" {
		t.Error("占位符不能为空: 会把「已配置」误判成「未配置」")
	}

	// Gemini OAuth: client_secret 必须是 secret 类, client_id 必须不是
	if !IsSecretSetting(SettingKeyGeminiOAuthClientSecret) {
		t.Error("Gemini client_secret 必须标记为密钥类(加密落库/掩码回显)")
	}
	if IsSecretSetting(SettingKeyGeminiOAuthClientID) {
		t.Error("Gemini client_id 不应是密钥类: 它是公开标识符")
	}
	if SettingNeedsAtRestEncryption(SettingKeyGeminiOAuthClientSecret) {
		// 正确: client_secret 进了 IsSecretSetting -> SettingNeedsAtRestEncryption
	} else {
		t.Error("Gemini client_secret 必须进 SettingNeedsAtRestEncryption(信封加密落库)")
	}
	if SettingNeedsAtRestEncryption(SettingKeyGeminiOAuthClientID) {
		t.Error("Gemini client_id 不应进 SettingNeedsAtRestEncryption(非秘密)")
	}
}

// TestGeminiOAuthDefaultSettingsExact 钉住内置默认值:
// 1) 从 DefaultSettings() 取出的值必须等于已知的正确值(来自 Gemini CLI npm 包)
// 2) client_secret 必须以 GOCSPX- 开头且长度为 35(当前已知格式)
// 这不能替代真实端点测试, 但能挡住"值被改成别的样子"的回归。
func TestGeminiOAuthDefaultSettingsExact(t *testing.T) {
	var clientID, clientSecret string
	for _, s := range DefaultSettings() {
		if s.Key == SettingKeyGeminiOAuthClientID {
			clientID = s.Value
		}
		if s.Key == SettingKeyGeminiOAuthClientSecret {
			clientSecret = s.Value
		}
	}
	if clientID != "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com" {
		t.Errorf("Gemini client_id 默认值错误: got %q", clientID)
	}
	const expectedSecret = "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"
	if clientSecret != expectedSecret {
		t.Errorf("Gemini client_secret 默认值错误: got %q want %q", clientSecret, expectedSecret)
	}
	if !strings.HasPrefix(clientSecret, "GOCSPX-") {
		t.Errorf("client_secret 必须以 GOCSPX- 开头: got %q", clientSecret)
	}
	// 当前已知的 GOCSPX- secret 为 35 字符(含前缀)
	if len(clientSecret) != 35 {
		t.Errorf("client_secret 长度应为 35: got %d (%q)", len(clientSecret), clientSecret)
	}
}

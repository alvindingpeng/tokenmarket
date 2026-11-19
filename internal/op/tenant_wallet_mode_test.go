package op

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestTenantBillingModeDefaultsToOff 直接查种子默认值而不是靠测试里手动设置:
// 这是"升级不影响存量部署"的核心保证 —— 默认值一旦变成 enforce,
// 所有老部署的默认租户余额都是 0, 升级即全站停摆。
func TestTenantBillingModeDefaultsToOff(t *testing.T) {
	// 走真实启动路径: InitDB 只建表, 默认设置由 InitCache 播种。
	// 不调 InitCache 就查默认值, 测的是"键不存在"而不是"默认值是多少"。
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "default-mode.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}

	mode, err := SettingGetString(model.SettingKeyTenantBillingMode)
	if err != nil {
		t.Fatalf("read default billing mode: %v", err)
	}
	if mode != model.TenantBillingModeOff {
		t.Fatalf("default billing mode=%q, want %q (must not break existing deployments)", mode, model.TenantBillingModeOff)
	}
	if TenantBillingEnforced() {
		t.Fatal("billing must not be enforced by default")
	}
}

// TestTenantBillingModeRejectsUnknownValue 拼错的模式名必须在保存时被拒,
// 因为读不到/不认识的值会退化成"不拦截", 让欠费账号继续调用。
func TestTenantBillingModeRejectsUnknownValue(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "mode-validate.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, value := range []string{"enforced", "ON", "", "strict"} {
		setting := model.Setting{Key: model.SettingKeyTenantBillingMode, Value: value}
		if err := setting.Validate(); err == nil {
			t.Fatalf("value %q should be rejected", value)
		}
	}
	for _, value := range []string{model.TenantBillingModeOff, model.TenantBillingModeEnforce} {
		setting := model.Setting{Key: model.SettingKeyTenantBillingMode, Value: value}
		if err := setting.Validate(); err != nil {
			t.Fatalf("value %q should be accepted: %v", value, err)
		}
	}
}

package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// emailCodeTestSetup 初始化隔离库并播种邮件配置(不真正发信: 发送失败不影响码的落库语义测试,
// 这里直接调底层写码路径前先绕开发信——MailSend 未配置时报错, 故只测校验侧)。
func emailCodeTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "emailcode.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
}

// emailCodeSeed 直接落一条验证码(绕过发信), 返回明文码。
func emailCodeSeed(t *testing.T, email, purpose, code string, expired bool) {
	t.Helper()
	record := model.EmailCode{
		Email:    email,
		Purpose:  purpose,
		CodeHash: hashCode(code),
	}
	if expired {
		record.ExpiresAt = time.Now().Add(-emailCodeTTL)
	} else {
		record.ExpiresAt = time.Now().Add(emailCodeTTL)
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed code: %v", err)
	}
}

// TestEmailCodeVerifyConsumesOnce 验证码一次性: 校验通过即作废, 复用一律拒绝。
func TestEmailCodeVerifyConsumesOnce(t *testing.T) {
	emailCodeTestSetup(t)
	ctx := context.Background()
	emailCodeSeed(t, "a@example.com", model.EmailCodePurposeRegister, "123456", false)

	if err := EmailCodeVerify(ctx, "a@example.com", model.EmailCodePurposeRegister, "123456"); err != nil {
		t.Fatalf("first verify should pass: %v", err)
	}
	if err := EmailCodeVerify(ctx, "a@example.com", model.EmailCodePurposeRegister, "123456"); err == nil {
		t.Fatal("second verify should fail: code is single-use")
	}
}

// TestEmailCodePurposeIsolation 注册码不能拿来改密, 反之亦然。
func TestEmailCodePurposeIsolation(t *testing.T) {
	emailCodeTestSetup(t)
	ctx := context.Background()
	emailCodeSeed(t, "b@example.com", model.EmailCodePurposeRegister, "654321", false)

	if err := EmailCodeVerify(ctx, "b@example.com", model.EmailCodePurposeReset, "654321"); err == nil {
		t.Fatal("register code must not verify for reset purpose")
	}
	if err := EmailCodeVerify(ctx, "b@example.com", model.EmailCodePurposeRegister, "654321"); err != nil {
		t.Fatalf("same-purpose verify should pass: %v", err)
	}
}

// TestEmailCodeExpiryAndAttempts 过期码拒绝; 错码累计次数, 超限后真码也拒。
func TestEmailCodeExpiryAndAttempts(t *testing.T) {
	emailCodeTestSetup(t)
	ctx := context.Background()
	emailCodeSeed(t, "c@example.com", model.EmailCodePurposeReset, "111111", true)
	if err := EmailCodeVerify(ctx, "c@example.com", model.EmailCodePurposeReset, "111111"); err == nil {
		t.Fatal("expired code must be rejected")
	}

	emailCodeSeed(t, "d@example.com", model.EmailCodePurposeReset, "222222", false)
	for i := 0; i < emailCodeMaxVerify; i++ {
		if err := EmailCodeVerify(ctx, "d@example.com", model.EmailCodePurposeReset, "999999"); err == nil {
			t.Fatal("wrong code must be rejected")
		}
	}
	if err := EmailCodeVerify(ctx, "d@example.com", model.EmailCodePurposeReset, "222222"); err == nil {
		t.Fatal("real code must be rejected after too many attempts")
	}
}

// TestUserForgotPasswordResetsAndRevokes 找回密码: 重设成功且旧会话吊销(令牌版本递增)。
func TestUserForgotPasswordResetsAndRevokes(t *testing.T) {
	emailCodeTestSetup(t)
	user := model.User{Username: "forgot", Email: "f@example.com", Password: "oldpassword1", Role: model.RoleUser, Status: model.StatusActive}
	if err := user.HashPassword(); err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	// 直插库不走 UserCreate, 手动刷新缓存让邮箱索引可见。
	if err := userRefreshCache(context.Background()); err != nil {
		t.Fatalf("refresh cache: %v", err)
	}
	if err := UserForgotPassword("f@example.com", "newpassword1"); err != nil {
		t.Fatalf("forgot password: %v", err)
	}
	ver, err := UserVerify("forgot", "newpassword1")
	if err != nil {
		t.Fatalf("verify new password: %v", err)
	}
	if ver.TokenVersion == 0 {
		t.Fatal("token version should bump to revoke old sessions")
	}
	if _, err := UserVerify("forgot", "oldpassword1"); err == nil {
		t.Fatal("old password must stop working")
	}
}

package op

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/totp"
)

// 2FA 信任设备: 签发/校验/过期/吊销/越权隔离。
//
// 这一组用例盯的是"安全功能的失败方式": 过期了必须失效、吊销了必须立即失效、别人的设备
// 一定不能认、令牌明文一定不能落库。任何一条放松都是真缺口, 而不是"体验问题"。

func TestTrustedDeviceIssueAndVerify(t *testing.T) {
	u := totpEnv(t)
	token, device, err := TrustedDeviceIssue(u.ID, "Mozilla/5.0 test-ua", "192.0.2.9")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if token == "" || len(token) != 64 {
		t.Fatalf("token shape: %q", token)
	}
	// 落库的必须是哈希, 不是明文 —— 库被拖走时明文令牌等于一张免码通行证。
	if device.TokenHash == token || len(device.TokenHash) != 64 {
		t.Fatalf("token stored unhashed: %q", device.TokenHash)
	}
	var stored model.TrustedDevice
	if err := db.GetDB().Where("id = ?", device.ID).First(&stored).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if strings.Contains(stored.TokenHash, token) {
		t.Fatal("plaintext token leaked into row")
	}
	if got := stored.UserAgent; got != "Mozilla/5.0 test-ua" {
		t.Fatalf("user agent not recorded: %q", got)
	}
	got, ok := TrustedDeviceVerify(u.ID, token)
	if !ok || got.ID != device.ID {
		t.Fatalf("verify: ok=%v id=%d", ok, got.ID)
	}
	// 校验成功会刷新 LastUsedAt(用于展示"最近使用"), 但不得延长到期时间 ——
	// 滑动过期会让一台十年不换的机器永远免码, 30 天的承诺就名存实亡。
	var after model.TrustedDevice
	if err := db.GetDB().Where("id = ?", device.ID).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !after.ExpiresAt.Equal(device.ExpiresAt) {
		t.Fatalf("expiry slid: %v -> %v", device.ExpiresAt, after.ExpiresAt)
	}
	if after.LastUsedAt.Before(device.LastUsedAt) {
		t.Fatalf("last used went backwards: %v", after.LastUsedAt)
	}
}

func TestTrustedDeviceRejectsWrongOwnerAndShape(t *testing.T) {
	u := totpEnv(t)
	other := userDeleteTestCreateUser(t, "totp-other")
	token, _, err := TrustedDeviceIssue(u.ID, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	// 别人的令牌: 哈希能查到行, 但归属不匹配。这是最容易写漏的一条越权判据。
	if _, ok := TrustedDeviceVerify(other.ID, token); ok {
		t.Fatal("accepted another user's device")
	}
	for _, bad := range []string{"", "   ", "not-a-token", strings.Repeat("a", 63)} {
		if _, ok := TrustedDeviceVerify(u.ID, bad); ok {
			t.Fatalf("accepted bogus token %q", bad)
		}
	}
	if _, ok := TrustedDeviceVerify(0, token); ok {
		t.Fatal("accepted device for user 0")
	}
}

func TestTrustedDeviceExpires(t *testing.T) {
	u := totpEnv(t)
	token, device, err := TrustedDeviceIssue(u.ID, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := TrustedDeviceVerify(u.ID, token); !ok {
		t.Fatal("fresh device rejected")
	}
	// 把到期时间推到过去, 模拟 30 天之后。
	if err := db.GetDB().Model(&model.TrustedDevice{}).Where("id = ?", device.ID).
		Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if _, ok := TrustedDeviceVerify(u.ID, token); ok {
		t.Fatal("expired device still accepted")
	}
	// 过期行不进列表(否则用户以为还有旁路可用)。
	list, err := TrustedDeviceList(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expired device listed: %d", len(list))
	}
}

func TestTrustedDeviceRevokeIsScopedAndImmediate(t *testing.T) {
	u := totpEnv(t)
	other := userDeleteTestCreateUser(t, "totp-victim")
	token, device, err := TrustedDeviceIssue(u.ID, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	// 拿别人的 id 去吊销: 必须 0 行, 且目标设备仍然有效。
	revoked, err := TrustedDeviceRevoke(other.ID, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 0 {
		t.Fatalf("cross-user revoke affected %d rows", revoked)
	}
	if _, ok := TrustedDeviceVerify(u.ID, token); !ok {
		t.Fatal("device revoked by another user")
	}
	revoked, err = TrustedDeviceRevoke(u.ID, device.ID)
	if err != nil || revoked != 1 {
		t.Fatalf("revoke: rows=%d err=%v", revoked, err)
	}
	if _, ok := TrustedDeviceVerify(u.ID, token); ok {
		t.Fatal("revoked device still accepted")
	}
}

func TestTrustedDeviceRevokeAll(t *testing.T) {
	u := totpEnv(t)
	first, _, _ := TrustedDeviceIssue(u.ID, "ua1", "ip1")
	second, _, _ := TrustedDeviceIssue(u.ID, "ua2", "ip2")
	// 另一用户的设备不能被顺带清掉。
	other := userDeleteTestCreateUser(t, "totp-bystander")
	keep, _, _ := TrustedDeviceIssue(other.ID, "ua3", "ip3")
	revoked, err := TrustedDeviceRevokeAll(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 2 {
		t.Fatalf("revoke all affected %d rows", revoked)
	}
	for _, token := range []string{first, second} {
		if _, ok := TrustedDeviceVerify(u.ID, token); ok {
			t.Fatal("device survived revoke-all")
		}
	}
	if _, ok := TrustedDeviceVerify(other.ID, keep); !ok {
		t.Fatal("revoke-all leaked to another user")
	}
}

func TestTrustedLoginSkipsCodeOnlyWithValidDevice(t *testing.T) {
	u := totpEnv(t)
	secret, _, err := UserTOTPSetup(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	if _, err := UserTOTPEnable(u.ID, code); err != nil {
		t.Fatal(err)
	}
	enabled, _ := userTOTPFresh(u.ID)
	// 空码 + 无信任凭据 -> 仍然要码。
	if _, _, err := UserTOTPLoginWithTrust(enabled, "", ""); err == nil || err.Error() != "two_factor_required" {
		t.Fatalf("no device but no code: %v", err)
	}
	// 空码 + 伪造凭据 -> 仍然要码。
	if _, _, err := UserTOTPLoginWithTrust(enabled, "", "bogus-token"); err == nil {
		t.Fatal("bogus token bypassed 2FA")
	}
	token, _, err := TrustedDeviceIssue(u.ID, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := userTOTPFresh(u.ID)
	fresh.Password = enabled.Password
	_, used, err := UserTOTPLoginWithTrust(fresh, "", token)
	if err != nil || !used {
		t.Fatalf("trusted login: used=%v err=%v", used, err)
	}
	// 靠信任免码登录**不**改变账号状态(不消耗 last_step, 不推进 totp_version)。
	after, _ := userTOTPFresh(u.ID)
	if after.TOTPVersion != fresh.TOTPVersion || after.TOTPLastStep != fresh.TOTPLastStep {
		t.Fatalf("trusted login mutated 2FA state: version %d->%d step %d->%d",
			fresh.TOTPVersion, after.TOTPVersion, fresh.TOTPLastStep, after.TOTPLastStep)
	}
	// 显式动态码优先: 输码登录不走信任凭据, used 必须为 false(否则调用方会误续期 Cookie)。
	next, _ := userTOTPFresh(u.ID)
	next.Password = enabled.Password
	code2, _ := totp.Generate(secret, time.Now().Unix()+30)
	if _, used, err := UserTOTPLoginWithTrust(next, code2, token); err != nil || used {
		t.Fatalf("explicit code: used=%v err=%v", used, err)
	}
}

func TestTOTPEnableAndDisableRevokeTrustedDevices(t *testing.T) {
	u := totpEnv(t)
	secret, _, err := UserTOTPSetup(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	if _, err := UserTOTPEnable(u.ID, code); err != nil {
		t.Fatal(err)
	}
	token, _, err := TrustedDeviceIssue(u.ID, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := TrustedDeviceVerify(u.ID, token); !ok {
		t.Fatal("device should be valid before disable")
	}
	// 解绑 2FA: 免第二因子的凭证随之作废。
	code2, _ := totp.Generate(secret, time.Now().Unix()+30)
	if err := UserTOTPDisable(u.ID, code2); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok := TrustedDeviceVerify(u.ID, token); ok {
		t.Fatal("device survived 2FA disable")
	}
}

func TestRegenerateRecoveryReplacesNotAppends(t *testing.T) {
	u := totpEnv(t)
	secret, _, err := UserTOTPSetup(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	first, err := UserTOTPEnable(u.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 10 {
		t.Fatalf("initial codes: %d", len(first))
	}
	// 用一张旧恢复码作为再生成的门槛(丢了设备的人只有这样自救)。
	if _, err := UserTOTPRegenerateRecovery(u.ID, first[0]); err != nil {
		t.Fatalf("regenerate with recovery code: %v", err)
	}
	// first[1] 没被用掉, 但整批已经被替换 —— 它现在必须是无效码。
	if _, err := UserTOTPRegenerateRecovery(u.ID, first[1]); err == nil {
		t.Fatal("stale recovery code accepted after regeneration")
	}
	// 新码可用。
	enabled, _ := userTOTPFresh(u.ID)
	var hashes []string
	_ = jsonUnmarshalHashes(t, enabled.TOTPRecoveryHashes, &hashes)
	if len(hashes) != 10 {
		t.Fatalf("hash count: %d (regeneration must replace, not append)", len(hashes))
	}
}

func TestRegenerateRecoveryRequiresEnabled2FA(t *testing.T) {
	u := totpEnv(t)
	if _, err := UserTOTPRegenerateRecovery(u.ID, "123456"); err == nil {
		t.Fatal("regenerated without 2FA enabled")
	}
}

// jsonUnmarshalHashes 把落库的恢复码哈希数组解出来; 解析失败直接判死(而不是当成空数组),
// 否则"哈希表被写坏"会伪装成"没有恢复码"。
func jsonUnmarshalHashes(t *testing.T, raw string, out *[]string) error {
	t.Helper()
	if raw == "" {
		*out = nil
		return nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("recovery hashes unparsable: %v", err)
	}
	return nil
}

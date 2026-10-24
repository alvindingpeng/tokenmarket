package auth

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/golang-jwt/jwt/v5"
)

// stepupTestUser 构造一个普通账号; 凭证校验只看 ID 与 TokenVersion, 不碰库。
func stepupTestUser() model.User {
	return model.User{ID: 7, Username: "alice", Role: model.RoleAdmin, TokenVersion: 3}
}

func TestStepUpTokenBindsUserVersionAndSession(t *testing.T) {
	secret = []byte("test-secret")
	user := stepupTestUser()
	token, ttl, err := IssueStepUpToken(user, "session-jwt-a")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ttl <= 0 || time.Duration(ttl)*time.Second > StepUpTTL {
		t.Fatalf("ttl out of range: %d", ttl)
	}
	if err := VerifyStepUpToken(token, "session-jwt-a", user); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}

	// 换一个用户: 不能拿别人的凭证过闸。
	other := user
	other.ID = 8
	if err := VerifyStepUpToken(token, "session-jwt-a", other); err == nil {
		t.Fatal("credential must not transfer between users")
	}

	// 换一个登录会话: 凭证跟签发时的会话绑定。
	if err := VerifyStepUpToken(token, "session-jwt-b", user); err == nil {
		t.Fatal("credential must not transfer between login sessions")
	}

	// 改密/禁用/登出都会递增 token_version, 旧凭证当场作废。
	bumped := user
	bumped.TokenVersion = user.TokenVersion + 1
	if err := VerifyStepUpToken(token, "session-jwt-a", bumped); err == nil {
		t.Fatal("credential must die with token_version bump")
	}
}

func TestStepUpTokenRejectsExpiredAndForged(t *testing.T) {
	secret = []byte("test-secret")
	user := stepupTestUser()
	token, _, err := IssueStepUpToken(user, "session-jwt-a")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// 过期: 手工签一枚 exp 已过的凭证。
	past := time.Now().Add(-time.Minute)
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &StepUpClaims{
		UserID:       user.ID,
		TokenVersion: user.TokenVersion,
		Session:      SessionFingerprint("session-jwt-a"),
		Purpose:      StepUpPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(past.Add(-StepUpTTL)),
			ExpiresAt: jwt.NewNumericDate(past),
		},
	}).SignedString(secret)
	if err != nil {
		t.Fatalf("sign expired: %v", err)
	}
	if err := VerifyStepUpToken(expired, "session-jwt-a", user); err == nil {
		t.Fatal("expired credential must be rejected")
	}

	// 篡改: 动签名段的首个字符。注意不能改末位 —— base64url 末字符只有高 2 bit 有效,
	// 只动低 4 bit 的改法解码出的签名字节一模一样, 会被当成"合法"放过(测试假阴性)。
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected credential shape: %s", token)
	}
	flipped := "A"
	if parts[2][0] == 'A' {
		flipped = "B"
	}
	parts[2] = flipped + parts[2][1:]
	tampered := strings.Join(parts, ".")
	if err := VerifyStepUpToken(tampered, "session-jwt-a", user); err == nil {
		t.Fatal("tampered credential must be rejected")
	}

	// 伪造: 换一把签名密钥签的凭证。
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &StepUpClaims{
		UserID:       user.ID,
		TokenVersion: user.TokenVersion,
		Session:      SessionFingerprint("session-jwt-a"),
		Purpose:      StepUpPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(StepUpTTL)),
		},
	}).SignedString([]byte("attacker-secret"))
	if err != nil {
		t.Fatalf("sign forged: %v", err)
	}
	if err := VerifyStepUpToken(forged, "session-jwt-a", user); err == nil {
		t.Fatal("credential signed with another key must be rejected")
	}

	// 空凭证/空会话一律拒绝。
	if err := VerifyStepUpToken("", "session-jwt-a", user); err == nil {
		t.Fatal("empty credential must be rejected")
	}
	if err := VerifyStepUpToken(token, "", user); err == nil {
		t.Fatal("credential without a login session must be rejected")
	}
}

func TestStepUpAndLoginTokensAreNotInterchangeable(t *testing.T) {
	secret = []byte("test-secret")
	user := stepupTestUser()

	// 登录 JWT 不能当 step-up 凭证(缺 step_up 用途标记)。
	loginToken, _, err := GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatalf("login token: %v", err)
	}
	if err := VerifyStepUpToken(loginToken, "session-jwt-a", user); err == nil {
		t.Fatal("login JWT must not pass as a step-up credential")
	}

	// step-up 凭证也不能当登录 JWT(载荷字段错开, 解析出的用户是零值)。
	stepToken, _, err := IssueStepUpToken(user, loginToken)
	if err != nil {
		t.Fatalf("step-up token: %v", err)
	}
	if claims, err := VerifyJWTToken(stepToken); err == nil && claims.UserID != 0 {
		t.Fatalf("step-up credential must not authenticate a session, claims=%+v", claims)
	}
	if err := VerifyStepUpToken(stepToken, loginToken, user); err != nil {
		t.Fatalf("step-up credential rejected for its own session: %v", err)
	}
}

func TestIssueStepUpTokenRequiresSession(t *testing.T) {
	secret = []byte("test-secret")
	if _, _, err := IssueStepUpToken(stepupTestUser(), ""); err == nil {
		t.Fatal("must refuse to issue a credential that cannot be session-bound")
	}
}

// TestStepUpThrottleBlocksIPRotationBruteforce 换 IP 绕不过爆破防线: 失败计数同时落在
// 用户维度(跨 IP 累计)与 IP 维度(跨账号累计), 任一维度超限即锁。
// 攻击者每试一次换一个 IP, 总尝试次数仍被用户维度封顶; 一台主机轮换扫多个账号,
// 则被 IP 维度封顶。
func TestStepUpThrottleBlocksIPRotationBruteforce(t *testing.T) {
	const uid = 5555
	const max = 5 // loginMaxAttempts 的默认阈值

	// 攻击面 A: 同一账号, 每次换一个全新 IP。
	// 阈值内(max 次)每次都放行, 第 max 次失败触发锁定。
	for i := 0; i < max; i++ {
		ip := fmt.Sprintf("10.55.%d.%d", i/256, i%256)
		if _, ok := StepUpAllowed(ip, uid); !ok {
			t.Fatalf("fresh IP must be allowed until the user budget is spent (attempt %d)", i)
		}
		StepUpFailure(ip, uid)
	}
	// 换到又一个全新 IP: 用户维度已超限, 照样拒 —— IP 轮换不增加总尝试预算。
	if _, ok := StepUpAllowed("10.55.255.255", uid); ok {
		t.Fatal("IP rotation must not extend the attempt budget for one account")
	}
	// 恢复现场: 成功清零用户维度。
	StepUpSuccess("10.55.255.255", uid)

	// 攻击面 B: 同一台主机, 每次打一个全新账号。
	const ip = "10.66.0.1"
	for i := 0; i < max; i++ {
		uidOther := uint(6000 + i)
		if _, ok := StepUpAllowed(ip, uidOther); !ok {
			t.Fatalf("fresh account must be allowed until the IP budget is spent (attempt %d)", i)
		}
		StepUpFailure(ip, uidOther)
	}
	// 换到又一个全新账号: IP 维度已超限, 照样拒。
	if _, ok := StepUpAllowed(ip, 6999); ok {
		t.Fatal("account rotation must not extend the attempt budget from one client")
	}
	// 不相干的 IP + 不相干的账号照常可用。
	if _, ok := StepUpAllowed("10.66.0.2", 7000); !ok {
		t.Fatal("throttle must not lock unrelated clients")
	}
	StepUpSuccess(ip, 6999)
}

func TestStepUpThrottleIsIndependentFromLogin(t *testing.T) {
	const ip = "10.99.0.1"
	const uid = 4242

	// 登录失败不影响 step-up 的额度(计数主体不同)。
	LoginFailure("ip:" + ip)
	defer LoginSuccess("ip:" + ip)
	if _, ok := StepUpAllowed(ip, uid); !ok {
		t.Fatal("login failures must not consume the step-up budget")
	}

	for i := 0; i < 10; i++ {
		StepUpFailure(ip, uid)
	}
	if retry, ok := StepUpAllowed(ip, uid); ok {
		t.Fatal("step-up password verification must be throttled after repeated failures")
	} else if retry <= 0 {
		t.Fatalf("throttle must report a wait time, got %s", retry)
	}

	// 换 IP 也躲不开: 主体按 IP 与用户双维度计数、任一超限即拒(与登录同一策略)。
	if _, ok := StepUpAllowed("10.99.0.2", uid); ok {
		t.Fatal("throttle must follow the account across client IPs")
	}
	if _, ok := StepUpAllowed(ip, uid+1); ok {
		t.Fatal("throttle must follow the client across accounts")
	}
	// 不相干的 IP + 不相干的账号不受影响。
	if _, ok := StepUpAllowed("10.99.0.2", uid+1); !ok {
		t.Fatal("throttle must not lock unrelated clients")
	}

	// 成功后清零, 恢复可试。
	StepUpSuccess(ip, uid)
	if _, ok := StepUpAllowed(ip, uid); !ok {
		t.Fatal("successful verification must reset the failure budget")
	}
}

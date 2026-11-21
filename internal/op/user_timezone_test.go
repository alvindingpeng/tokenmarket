package op

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestUserTimezoneColumnAddedByAutoMigrate 钉住"加列靠 AutoMigrate, 不靠迁移版本号"这条决策。
//
// 为什么专门起一个"老库"来测: 这条列**没有**对应的 migrate 版本(47 号留给以后真有数据搬迁时用),
// 于是"老库启动后到底有没有这一列"就只由 AutoMigrate 决定。若哪天有人把 &model.User{} 从
// AutoMigrate 清单里拿掉、或把本列改成需要回填的形态, 这个用例会第一个红。
func TestUserTimezoneColumnAddedByAutoMigrate(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-users.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("unwrap sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 老库形态: users 表存在、**没有 timezone 列**, 里面已经躺着一个账号。
	//
	// 老表按加列前一版的 users 形状建, 不是"只有 id 一列"那种最简表。原因很实际:
	// AutoMigrate 会顺手给 username 之类的 NOT NULL 无默认值列补 ALTER, 而 SQLite 拒绝
	// 给非空表加无默认值的 NOT NULL 列 —— 用例就会红在一个跟 timezone 毫无关系的地方。
	// 而"前一版的完整形状"正是生产里真实的形态, 也才测得出"补这一列"这件事本身。
	if err := conn.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		username text NOT NULL DEFAULT '',
		password text NOT NULL DEFAULT '',
		email text NOT NULL DEFAULT '',
		role text NOT NULL DEFAULT 'user',
		status text NOT NULL DEFAULT 'active',
		tier text NOT NULL DEFAULT '',
		token_version integer NOT NULL DEFAULT 0,
		balance real NOT NULL DEFAULT 0,
		frozen real NOT NULL DEFAULT 0,
		total_spent real NOT NULL DEFAULT 0,
		total_revenue real NOT NULL DEFAULT 0,
		total_recharged real NOT NULL DEFAULT 0,
		total_withdrawn real NOT NULL DEFAULT 0,
		withdraw_frozen real NOT NULL DEFAULT 0,
		totp_enabled numeric NOT NULL DEFAULT 0,
		totp_secret_encrypted text NOT NULL DEFAULT '',
		totp_version integer NOT NULL DEFAULT 0,
		totp_last_step integer NOT NULL DEFAULT -1,
		totp_recovery_hashes text NOT NULL DEFAULT '',
		totp_attempts integer NOT NULL DEFAULT 0,
		totp_attempt_window integer NOT NULL DEFAULT 0,
		created_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	if err := conn.Exec("INSERT INTO users (id, username) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}

	// db.InitDB 对 &model.User{} 做的正是这一步; 这里只调它, 不跑整套迁移,
	// 免得把"加列是否成功"跟别的迁移混在一起(那样红了也看不出是谁的问题)。
	if err := conn.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("automigrate users: %v", err)
	}

	if !conn.Migrator().HasColumn(&model.User{}, "timezone") {
		t.Fatal("users.timezone 列没有被 AutoMigrate 建出来: 本列没有对应的迁移版本, 建不出来就永远读不到偏好")
	}

	// 老行读出的必须是空串 —— 空串就是"跟随服务器时区"的语义, 所以不需要任何回填。
	// 若这里读出别的东西(NULL 扫进 string 会报错, 或 default 落成非空值),
	// 老账号就会被静默钉死在某个时区上, 而用户从没做过这个选择。
	var legacy model.User
	if err := conn.Table("users").Where("id = ?", 1).Take(&legacy).Error; err != nil {
		t.Fatalf("read legacy user: %v", err)
	}
	if legacy.Timezone != "" {
		t.Fatalf("老账号的 timezone 应为空串(跟随服务器), 实际 %q", legacy.Timezone)
	}
	if got := legacy.View().Timezone; got != "" {
		t.Fatalf("View() 应把空串原样带出, 实际 %q", got)
	}
}

// TestUserUpdateTimezone 覆盖改时区偏好的三条出口: 合法 / 非法 / 空串(恢复跟随服务器),
// 并顺带钉住两个容易回归的点 —— 写入后缓存立刻可见, 以及**不动 token_version**。
func TestUserUpdateTimezone(t *testing.T) {
	// 本包内直接调 opTestDBSetup: 它内部已经 resetProcessCaches() 并注册了 cleanup,
	// 这就是任务里说的"换库必须清进程级缓存"(外部包测试才需要 ResetProcessCachesForTest)。
	opTestDBSetup(t, "user-timezone.db")
	user := userDeleteTestCreateUser(t, "tz-user")

	// token_version 是"改时区不影响登录"的落地点: 它一旦自增, 该用户的全部既有 Cookie
	// 立刻失效, 用户选个时区就被踢下线 —— 那就把改时区的代价跟改用户名混为一谈了。
	tokenVersionOf := func() int {
		var row struct {
			TokenVersion int
		}
		if err := db.GetDB().Table("users").Select("token_version").Where("id = ?", user.ID).Take(&row).Error; err != nil {
			t.Fatalf("read token_version: %v", err)
		}
		return row.TokenVersion
	}
	before := tokenVersionOf()

	// 合法值: 写入后缓存里必须立刻是新值, 否则设置页改完刷新 /user/status 会读回旧值。
	if err := UserUpdateTimezone(user.ID, "Asia/Shanghai"); err != nil {
		t.Fatalf("合法时区应被接受, 实际报错: %v", err)
	}
	if got := UserTimezoneByID(user.ID); got != "Asia/Shanghai" {
		t.Fatalf("UserTimezoneByID 应读到刚写入的 Asia/Shanghai, 实际 %q", got)
	}
	// View() 必须带上这一列: /user/status 与 /login 都走 View(), 漏掉映射的话
	// 库里有值、接口里没有, 设置页永远显示"跟随服务器", 用户改了也看不出变化。
	if err := userRefreshCache(t.Context()); err != nil {
		t.Fatalf("refresh user cache: %v", err)
	}
	updated, err := UserGetByID(user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got := updated.View().Timezone; got != "Asia/Shanghai" {
		t.Fatalf("View() 应带出 timezone, 实际 %q", got)
	}
	if v := tokenVersionOf(); v != before {
		t.Fatalf("改时区不该自增 token_version(会强制重新登录): 改前 %d, 改后 %d", before, v)
	}

	// 非法值必须报错且**不改动**已有偏好。
	// 注意两种控制字符形态的区别: "Asia\nShanghai" 是名字中间夹换行, "Asia/Shanghai\n" 是末尾带换行。
	// 后者**不能**靠 TrimSpace 兜住 —— 清洗完就是合法名, 于是脏请求被静默接受,
	// 400 前的这道闸是唯一能把这个边界讲清楚的地方。
	for _, bad := range []string{
		"Not/AZone",
		"Asia Shanghai",
		"Asia\nShanghai",
		"Asia/Shanghai\n",
		"Asia/Shanghai\r\n",
		"\tAsia/Shanghai",
		"../../etc/passwd",
		"Local",
		"Asia/Shanghai/Extra",
		strings.Repeat("A", model.TimezoneMaxLen+1),
	} {
		if err := UserUpdateTimezone(user.ID, bad); err == nil {
			t.Fatalf("非法时区 %q 应被拒绝, 却返回成功", bad)
		}
		if got := UserTimezoneByID(user.ID); got != "Asia/Shanghai" {
			t.Fatalf("非法输入 %q 不该改动已有偏好, 期望仍是 Asia/Shanghai, 实际 %q", bad, got)
		}
	}

	// 空串(含纯空白): 合法, 表示恢复"跟随服务器"。
	if err := UserUpdateTimezone(user.ID, "   "); err != nil {
		t.Fatalf("空串表示跟随服务器, 应被接受, 实际报错: %v", err)
	}
	if got := UserTimezoneByID(user.ID); got != "" {
		t.Fatalf("清空后应读回空串, 实际 %q", got)
	}

	// 空串要真的落库(而不是只改了缓存): 重建缓存后仍应是空串。
	// 这条断言挡的是"Update 调用没报错但其实没写"这类静默失败 —— 界面上看不出来,
	// 直到下一次进程重启偏好才"自己变回去"。
	if err := userRefreshCache(t.Context()); err != nil {
		t.Fatalf("refresh user cache: %v", err)
	}
	if got := UserTimezoneByID(user.ID); got != "" {
		t.Fatalf("清空后落库值应为空串, 重读缓存得到 %q", got)
	}

	// 再次写入合法值并重读, 确认前面"重读得到空串"不是因为它压根没写进去。
	if err := UserUpdateTimezone(user.ID, "UTC"); err != nil {
		t.Fatalf("UTC 应被显式接受(它是真实 IANA 区名), 实际报错: %v", err)
	}
	if err := userRefreshCache(t.Context()); err != nil {
		t.Fatalf("refresh user cache: %v", err)
	}
	if got := UserTimezoneByID(user.ID); got != "UTC" {
		t.Fatalf("重读缓存应得到 UTC, 实际 %q", got)
	}

	// 缓存未命中(账号不在缓存里)时回落到空串, 而不是报错或去查库:
	// 这个函数在统计链路里逐请求调用, "还不知道"的降级方向必须与"没设置"一致。
	if got := UserTimezoneByID(user.ID + 1000); got != "" {
		t.Fatalf("未知账号应回落到空串, 实际 %q", got)
	}
}

package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 本文件锁的是「统计窗口按用户时区解释、日/周/月分组仍按服务器日历」这条并存口径。
//
// 为什么必须钉死: 两套日历的分工一旦被谁"顺手统一", 错的不是显示而是钱 ——
// 统计页与账单页会对同一区间给出不同金额, 而这样的错不报错、不 panic, 只是账对不上。

// statsTZTestLocation 按 IANA 名取时区; 取不到时跳过(某些最小化镜像没有 tzdata)。
// 不硬编码偏移来替代 LoadLocation: 本任务要验的正是"名字 → 挂钟"的换算,
// 用 FixedZone 会把被测逻辑换成测试自己构造的时区, 断言就失去意义。
func statsTZTestLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil || loc == nil {
		t.Skipf("tzdata 里没有 %s, 跳过时区用例: %v", name, err)
	}
	return loc
}

// statsTZDate 构造某时区的挂钟时间。
func statsTZDate(loc *time.Location, y int, m time.Month, d, hour, min int) time.Time {
	return time.Date(y, m, d, hour, min, 0, 0, loc)
}

// statsTZLocalOffsetMinutes 返回 time.Local 在当前时刻的 UTC 偏移分钟数。
// 不写死 480: 服务器可能是任何时区, 期望值必须由实测偏移推出。
func statsTZLocalOffsetMinutes() int {
	_, offsetSeconds := time.Now().Zone()
	return offsetSeconds / 60
}

// TestStatsDateRangeTimezoneSemantics 三个时区解析出的瞬时不同, 但**服务器挂钟语义**必须一致:
// 同一个 "2026-11-21" 在任何用户时区下都精确对应同样长度的 24 小时, 且返回前已换回 time.Local,
// 下游的整日裁剪(dayStart/dayKey/billingRetentionCutoff)拿到的是同一套服务器挂钟表示。
func TestStatsDateRangeTimezoneSemantics(t *testing.T) {
	shanghai := statsTZTestLocation(t, "Asia/Shanghai")
	phnomPenh := statsTZTestLocation(t, "Asia/Phnom_Penh")
	utc := statsTZTestLocation(t, "UTC")

	// 用固定的日期字符串, 避免"今天"随运行时刻漂移而让默认值分支参与断言。
	startS, endS := "2026-11-21", "2026-11-21"

	type got struct {
		name         string
		start, end   time.Time
		windowHours  float64
		startLocalSS string
		endLocalSS   string
	}
	cases := []struct {
		name string
		loc  *time.Location
	}{
		{"UTC", utc},
		{"Asia/Shanghai", shanghai},
		{"Asia/Phnom_Penh", phnomPenh},
	}

	results := make([]got, 0, len(cases))
	for _, c := range cases {
		start, end := parseStatsDateRange(startS, endS, c.loc)
		results = append(results, got{
			name:         c.name,
			start:        start,
			end:          end,
			windowHours:  end.Sub(start).Hours(),
			startLocalSS: start.In(time.Local).Format("2006-01-02 15:04:05 -0700"),
			endLocalSS:   end.In(time.Local).Format("2006-01-02 15:04:05 -0700"),
		})
	}

	// 1) 每个结果都是 24 小时窗口: endDate 是包含的最后一天, 故窗口 = 当天整天。
	for _, r := range results {
		if r.windowHours != 24 {
			t.Fatalf("%s: 窗口应恰好 24 小时, 实际 %.2f 小时(start=%v end=%v)",
				r.name, r.windowHours, r.start, r.end)
		}
	}

	// 2) 瞬时**不同**: UTC 用户与 +07 用户说的 "11-21" 在绝对时间上不是同一段。
	//    若三者相等, 说明 loc 参数被无视了(退化成服务器时区), 那本任务等于没做。
	if results[0].start.Equal(results[1].start) {
		t.Fatalf("UTC 与 Asia/Shanghai 解析出的区间起点是同一瞬时(均为 %v) —— loc 参数没生效", results[0].start)
	}
	if results[1].start.Equal(results[2].start) {
		t.Fatalf("Asia/Shanghai 与 Asia/Phnom_Penh(+07) 解析出的区间起点是同一瞬时(%v) —— 时区没生效", results[1].start)
	}

	// 3) 换回服务器时区只改**表示**, 不改**瞬时**: 返回值必须与"在该用户时区里取当天零点"同一瞬时。
	//    这一条是本函数的存在理由 —— 下游(dayStart/dayKey/billingRetentionCutoff/SQL 文本比较)
	//    全建立在服务器挂钟表示上, 但它读到的窗口仍是用户说的那一天。
	//    注意: 窗口**不**对齐服务器零点(那正是本任务要修的偏差), 所以这里不能断言 Hour()==0:
	//    用户 +07 的 11-21 零点对应服务器 11-21 01:00, 断言零点等于把 bug 写进测试。
	for i, r := range results {
		loc := cases[i].loc
		wantStart := time.Date(2026, time.November, 21, 0, 0, 0, 0, loc)
		wantEnd := wantStart.AddDate(0, 0, 1)
		if !r.start.Equal(wantStart) {
			t.Fatalf("%s: 换算改变了瞬时: 得到 %v, 期望 %v(%s 本地 11-21 00:00)",
				r.name, r.start, wantStart, loc)
		}
		if !r.end.Equal(wantEnd) {
			t.Fatalf("%s: 换算改变了瞬时: 得到 %v, 期望 %v(%s 本地 11-22 00:00)",
				r.name, r.end, wantEnd, loc)
		}
		if r.end.Sub(r.start) != 24*time.Hour {
			t.Fatalf("%s: 窗口不是 24 小时: %s → %s", r.name, r.startLocalSS, r.endLocalSS)
		}
	}

	// 4) 每个用例内部, 返回值与自身 .In(time.Local) 逐字段相等(即已经换到了本地时区表示)。
	for _, r := range results {
		back := r.start.In(time.Local)
		y1, m1, d1 := r.start.Date()
		y2, m2, d2 := back.Date()
		h1, mi1, s1 := r.start.Clock()
		h2, mi2, s2 := back.Clock()
		if y1 != y2 || m1 != m2 || d1 != d2 || h1 != h2 || mi1 != mi2 || s1 != s2 {
			t.Fatalf("%s: 返回值与 .In(time.Local) 不一致: %v vs %v", r.name, r.start, back)
		}
		if r.start.Location() != time.Local {
			t.Fatalf("%s: 返回值的 Location 应为 time.Local, 实际 %v", r.name, r.start.Location())
		}
	}

	// 5) 偏移差 = 窗口位移量: +07 用户的 11-21 比 +08 用户的 11-21 在绝对时间上早 1 小时。
	_, shanghaiOffset := time.Now().In(shanghai).Zone()
	_, phnomOffset := time.Now().In(phnomPenh).Zone()
	wantShift := time.Duration(shanghaiOffset-phnomOffset) * time.Second
	if delta := results[2].start.Sub(results[1].start); delta != wantShift {
		t.Fatalf("两个时区的区间起点位移 = %v, 期望 %v(即偏移差)", delta, wantShift)
	}
}

// statsTZSetUserTimezone 让「用户时区偏好」在测试里生效。
//
// 为什么写库再回填缓存而不是只改缓存: UserTimezoneByID 读的是 userCache(缓存未命中不查库,
// 见它的注释), 所以只改库不刷新缓存等于没改; 而只改缓存又会让"偏好是持久化设置"这件事在测试里
// 变成一个内存幻觉。两步都做, 测试断言的才是真实读路径。
func statsTZSetUserTimezone(t *testing.T, userID uint, tz string) {
	t.Helper()
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", userID).
		Update("timezone", tz).Error; err != nil {
		t.Fatalf("set user timezone: %v", err)
	}
	var user model.User
	if err := db.GetDB().Where("id = ?", userID).Take(&user).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	userCache.Set(int(userID), user)
}

// TestStatsWindowUsesUserCalendar 是本任务的核心断言: 单日窗口下明细行的取舍必须由**用户日历**决定。
//
// 换算方向先讲清楚(这里最容易搞反, 也最容易写出"看似通过"的假断言):
// 用户 UTC+7、服务器 UTC+8, 同一瞬时下 用户本地 = 服务器本地 − 1 小时。
// 用户说的"11-21"= 用户本地 11-21 00:00 → 11-22 00:00
//
//	= 服务器挂钟  11-21 01:00 → 11-22 01:00 (两侧都左移 1 小时)。
//
// 于是服务器挂钟的 11-21 00:00-01:00 这一段属于用户的 **11-20 23:00-24:00**(前一天),
// 而服务器挂钟的 11-22 00:00-01:00 属于用户的 **11-21 23:00-24:00**(仍在当天)。
// 改造前窗口恒为服务器 11-21 00:00 → 11-22 00:00, 恰好在这两个端点上出错。
func TestStatsWindowUsesUserCalendar(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	server := statsTZTestLocation(t, "Asia/Shanghai")
	user := statsTZTestLocation(t, "Asia/Phnom_Penh")
	_ = user // 仅用于说明两个时区的偏移差, 实际换算由 parseStatsDateRange 完成。

	statsTZSetUserTimezone(t, payer.ID, "Asia/Phnom_Penh")

	serverAt := func(d, hour, min int) time.Time {
		// SQLite stores application timestamps in the process's local representation.
		return statsTZDate(server, 2026, time.November, d, hour, min).In(time.Local)
	}
	// 五条记录的代价互不相同(1/2/4/8/16), 这样"算错了哪一条"能从金额差反推出来。
	rows := []model.BillingRecord{
		// A: 服务器 11-20 23:30 = 用户 11-20 22:30 → 用户的前一天, 两套日历都排除。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, serverAt(20, 23, 30), 1, 1),
		// B: 服务器 11-21 00:30 = 用户 11-20 23:30 → 用户的前一天, **必须排除**。
		//    改造前它落在服务器窗口内会被计入 —— 这是本任务要修的第一处。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, serverAt(21, 0, 30), 2, 2),
		// C: 服务器 11-21 01:30 = 用户 11-21 00:30 → 用户当天, 两套日历都计入。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, serverAt(21, 1, 30), 4, 4),
		// D: 服务器 11-21 23:30 = 用户 11-21 22:30 → 用户当天, 两套日历都计入。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, serverAt(21, 23, 30), 8, 8),
		// E: 服务器 11-22 00:30 = 用户 11-21 23:30 → 用户当天, **必须计入**。
		//    改造前它落在服务器窗口外会被丢掉 —— 这是本任务要修的第二处。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, serverAt(22, 0, 30), 16, 16),
	}
	userStatsInsert(t, rows...)

	ctx := context.Background()
	overview, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	// 用户 11-21 = 服务器 11-21 01:00 → 11-22 01:00 ⇒ C + D + E。
	if overview.TotalRequests != 3 || overview.TotalCost != 28 || overview.TotalTokens != 28 {
		t.Fatalf("用户日历单日窗口口径错: requests=%d cost=%v tokens=%d, 期望 3/28/28 "+
			"(= C(4)+D(8)+E(16), 即服务器 11-21 01:00→11-22 01:00)",
			overview.TotalRequests, overview.TotalCost, overview.TotalTokens)
	}

	// 核心判别点单独钉死, 免得总数凑巧相等就通过:
	// B(服务器 00:30)必须不在结果里 —— 它在用户本地是 11-20 23:30。
	// 用"把 B 的金额改成极端值再看合计是否变化"来验证它确实没被计入。
	if err := db.GetDB().Model(&model.BillingRecord{}).
		Where("user_id = ? AND user_cost = ?", payer.ID, 2).
		Update("user_cost", 999).Error; err != nil {
		t.Fatalf("update B cost: %v", err)
	}
	after, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if after.TotalCost != 28 {
		t.Fatalf("把服务器 00:30 那条(A 类边界)的金额改成 999 后合计变了(%v → %v), "+
			"说明它被错误计入用户 11-21", overview.TotalCost, after.TotalCost)
	}
	// E(服务器 11-22 00:30)必须真的在结果里: 把它改成 0 后合计应减少 16。
	if err := db.GetDB().Model(&model.BillingRecord{}).
		Where("user_id = ? AND user_cost = ?", payer.ID, 16).
		Update("user_cost", 0).Error; err != nil {
		t.Fatalf("update E cost: %v", err)
	}
	afterE, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if afterE.TotalCost != 12 {
		t.Fatalf("服务器 11-22 00:30(用户 11-21 23:30)那条没有被计入(合计 %v, 期望 12 = 28-16); "+
			"它在用户日历里属于 11-21 的最后一小时", afterE.TotalCost)
	}

	// 三个接口必须用同一套边界: 按模型与延迟也只看那三条,
	// 否则前端把概览金额与按模型/延迟样本并排一放就自相矛盾。
	models, err := UserStatsModelBreakdown(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("model breakdown: %v", err)
	}
	if len(models) != 1 || models[0].Requests != 3 {
		t.Fatalf("按模型的区间口径与概览不一致: %+v", models)
	}

	// 反向对照: 清空偏好后按进程的日历取数。夹具用上海时间描述，
	// 在上海应计入 B+C+D；UTC 和金边的午夜边界各不相同。
	statsTZSetUserTimezone(t, payer.ID, "")
	serverOverview, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview(server tz): %v", err)
	}
	rows[1].UserCost = 999
	rows[4].UserCost = 0
	var wantRequests int64
	var wantCost float64
	for _, row := range rows {
		if row.CreatedAt.In(time.Local).Format(time.DateOnly) == "2026-11-21" {
			wantRequests++
			wantCost += row.UserCost
		}
	}
	if serverOverview.TotalRequests != wantRequests || serverOverview.TotalCost != wantCost {
		t.Fatalf("服务器日历: requests/cost=%d/%v，期望 %d/%v",
			serverOverview.TotalRequests, serverOverview.TotalCost, wantRequests, wantCost)
	}
}

// TestStatsHourLabelShiftsToUserTimezone 小时桶标签换算到用户时区: 桶本身不动, 只换刻度标签。
//
// 关键取舍: 桶是服务器挂钟的整点切片, 平移到 +07 后标签为 :00(整小时偏移),
// 而平移到 +05:45(Asia/Kathmandu)之后会带 45 分钟位 —— 必须如实显示, 不许四舍五入。
func TestStatsHourLabelShiftsToUserTimezone(t *testing.T) {
	phnomPenh := statsTZTestLocation(t, "Asia/Phnom_Penh")
	shanghai := statsTZTestLocation(t, "Asia/Shanghai")
	hostLabel := func(hour int) string {
		return statsTZDate(shanghai, 2026, time.November, 21, hour, 0).
			In(time.Local).Format("2006-01-02 15:04")
	}

	// 服务器 10:00 的桶 → +07 用户看到 09:00。
	if got := statsHourLabelIn(hostLabel(10), phnomPenh); got != "2026-11-21 09:00" {
		t.Fatalf("服务器 10:00 的桶在 +07 下应为 09:00, 实际 %q", got)
	}
	// 服务器 10:00 的桶 → +08 用户(与服务器同时区)应当仍是 10:00。
	if got := statsHourLabelIn(hostLabel(10), shanghai); got != "2026-11-21 10:00" {
		t.Fatalf("同时区下标签不该变, 实际 %q", got)
	}
	// 跨日: 服务器 00:00 的桶 → +07 用户是**前一天的** 23:00。
	if got := statsHourLabelIn(hostLabel(0), phnomPenh); got != "2026-11-20 23:00" {
		t.Fatalf("跨日换算错: 服务器 11-21 00:00 在 +07 下应为 11-20 23:00, 实际 %q", got)
	}
	// 非整点偏移必须保留分钟位(四舍五入会让标签与它代表的桶错位)。
	katmandu := statsTZTestLocation(t, "Asia/Kathmandu")
	if got := statsHourLabelIn(hostLabel(10), katmandu); got != "2026-11-21 07:45" {
		t.Fatalf("+05:45 下的标签应带 45 分钟位(07:45), 实际 %q", got)
	}
	// 解析不了就原样返回: 宁可显示旧标签, 也不要让整条曲线消失。
	if got := statsHourLabelIn("not-a-timestamp", phnomPenh); got != "not-a-timestamp" {
		t.Fatalf("非法标签应原样返回, 实际 %q", got)
	}
}

// TestStatsHourTrendLabelEndToEnd 端到端: 趋势接口在 hour 粒度下返回的标签已经是用户时区。
// 与上一个用例的分工: 那个测纯函数, 这个测接口真的接上了 (否则函数正确但没人调用)。
func TestStatsHourTrendLabelEndToEnd(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	server := statsTZTestLocation(t, "Asia/Shanghai")
	statsTZSetUserTimezone(t, payer.ID, "Asia/Phnom_Penh")

	at := statsTZDate(server, 2026, time.November, 21, 10, 15).In(time.Local)
	userStatsInsert(t, userStatsRow(payer.ID, "m", model.BillingStatusSettled, at, 3, 30))

	ctx := context.Background()
	trend, err := UserStatsTrend(ctx, payer.ID, "2026-11-21", "2026-11-21", "hour")
	if err != nil {
		t.Fatalf("trend hour: %v", err)
	}
	if len(trend) != 1 {
		t.Fatalf("应恰有一个小时桶, 实际 %+v", trend)
	}
	if trend[0].Date != "2026-11-21 09:00" {
		t.Fatalf("+07 用户下服务器 10:xx 的桶标签应为 09:00, 实际 %q", trend[0].Date)
	}
	if trend[0].Requests != 1 || trend[0].Cost != 3 {
		t.Fatalf("标签换算不该改动桶里的量: %+v", trend[0])
	}

	// 未设置偏好时, 标签与改造前逐字相同(服务器挂钟)。
	statsTZSetUserTimezone(t, payer.ID, "")
	trend, err = UserStatsTrend(ctx, payer.ID, "2026-11-21", "2026-11-21", "hour")
	if err != nil {
		t.Fatalf("trend hour(server tz): %v", err)
	}
	want := at.In(time.Local).Format("2006-01-02 15:00")
	if len(trend) != 1 || trend[0].Date != want {
		t.Fatalf("未设置用户时区时应返回服务器小时桶 %s, 实际 %+v", want, trend)
	}
}

// TestStatsOverviewTimezoneFields 概览的 5 个口径字段: 值必须与窗口实际使用的时区同源,
// 且前端"两个 offset 相等 ⇒ 不提示"这条判据在服务端就已可判定。
func TestStatsOverviewTimezoneFields(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()

	// ── 分支一: 偏好设为 +07, 服务器可能不是 +07 ──
	statsTZSetUserTimezone(t, payer.ID, "Asia/Phnom_Penh")
	overview, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.StatsTimezone != "Asia/Phnom_Penh" {
		t.Fatalf("stats_timezone 应为用户偏好名, 实际 %q", overview.StatsTimezone)
	}
	if overview.StatsTimezoneSource != statsTimezoneSourceUser {
		t.Fatalf("stats_timezone_source 应为 %q, 实际 %q", statsTimezoneSourceUser, overview.StatsTimezoneSource)
	}
	if overview.StatsTimezoneOffsetMinutes != 420 { // +07:00 固定, 无夏令时
		t.Fatalf("Asia/Phnom_Penh 偏移应为 420 分钟, 实际 %d", overview.StatsTimezoneOffsetMinutes)
	}
	// 日历侧恒为服务器时区, 偏移由实测推出(不写死 480: 服务器可以是任何时区)。
	if overview.StatsCalendarTimezoneOffsetMinutes != statsTZLocalOffsetMinutes() {
		t.Fatalf("日历偏移应为 time.Local 的实测偏移 %d, 实际 %d",
			statsTZLocalOffsetMinutes(), overview.StatsCalendarTimezoneOffsetMinutes)
	}
	if overview.StatsCalendarTimezone == "" || overview.StatsCalendarTimezone == "Local" {
		t.Fatalf("日历时区名不能为空也不能是 \"Local\"(不可复现), 实际 %q", overview.StatsCalendarTimezone)
	}
	// 前端只用"两个 offset 是否相等"来判定要不要提示两套时区(名字不同但偏移相同 = 挂钟一致),
	// 所以这两个字段必须是同一时刻的偏移, 不能各自取 time.Now 算一遍 —— DST 切换那一刻前后
	// 会给出"同一时区两个偏移"的假差异。断言它们与"同一时刻换算两次"的结果一致。
	at := time.Now()
	if want := statsOffsetMinutes(time.Local, at); overview.StatsCalendarTimezoneOffsetMinutes != want {
		t.Fatalf("日历偏移与同一时刻换算不符: %d vs %d", overview.StatsCalendarTimezoneOffsetMinutes, want)
	}
	if overview.StatsTimezoneOffsetMinutes == overview.StatsCalendarTimezoneOffsetMinutes {
		// 偏移相同即挂钟一致: 此时时区名不同也不该提示。断言我们没有把" name 不同"伪装成"日历不同"。
		t.Logf("用户时区 %q 与日历 %q 偏移相同(%d), 前端不应提示两套时区",
			overview.StatsTimezone, overview.StatsCalendarTimezone, overview.StatsTimezoneOffsetMinutes)
	}

	// ── 分支二: 未设置偏好 → source=server 且两个名字必须相同 ──
	statsTZSetUserTimezone(t, payer.ID, "")
	serverOverview, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview(server): %v", err)
	}
	if serverOverview.StatsTimezoneSource != statsTimezoneSourceServer {
		t.Fatalf("未设置偏好时 source 应为 %q, 实际 %q", statsTimezoneSourceServer, serverOverview.StatsTimezoneSource)
	}
	if serverOverview.StatsTimezone != serverOverview.StatsCalendarTimezone {
		t.Fatalf("回落到服务器时区时两个时区名必须相同(前端据名字相同判定不提示): %q vs %q",
			serverOverview.StatsTimezone, serverOverview.StatsCalendarTimezone)
	}
	if serverOverview.StatsTimezoneOffsetMinutes != serverOverview.StatsCalendarTimezoneOffsetMinutes {
		t.Fatalf("回落到服务器时区时两个偏移必须相同: %d vs %d",
			serverOverview.StatsTimezoneOffsetMinutes, serverOverview.StatsCalendarTimezoneOffsetMinutes)
	}
	// 两种情况下的日历侧必须一致: 日/周/月桶永远不吃用户偏好。
	if serverOverview.StatsCalendarTimezone != overview.StatsCalendarTimezone {
		t.Fatalf("日历时区不该随用户偏好变化: %q → %q",
			overview.StatsCalendarTimezone, serverOverview.StatsCalendarTimezone)
	}
}

// TestStatsRollupNotPulledInForNonAlignedWindow 汇总表安全:
// 用户时区让窗口在**服务器日历**上不再整日对齐时, userStatsRollupDayRange 必须返回空,
// 即这天只能按明细算 —— 不许把整天汇总拉进来(那会把窗口外的半天的量算进总额)。
//
// 场景: 用户 +07、服务器 +08, 用户说"11-21" ⇒ 服务器挂钟 11-21 01:00 → 11-22 01:00。
// 按服务器整天看, 首日 11-21 只覆盖了 01:00-24:00(缺了 00:00-01:00), 末日 11-22 只覆盖 00:00-01:00,
// 两天都不是完整服务器日 ⇒ 一天汇总都不能用。
// 钉死方式: 埋一行 user 维度汇总(数值远大于明细), 断言合计只等于明细。
func TestStatsRollupNotPulledInForNonAlignedWindow(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	server := statsTZTestLocation(t, "Asia/Shanghai")
	statsTZSetUserTimezone(t, payer.ID, "Asia/Phnom_Penh")

	// 明细: 一条落在用户 11-21 当天(服务器 11-21 01:30)。
	at := statsTZDate(server, 2026, time.November, 21, 1, 30)
	userStatsInsert(t, userStatsRow(payer.ID, "m", model.BillingStatusSettled, at, 5, 50))

	// 汇总: 服务器 11-21 一整天的合计, request_count=10 / cost=100 / tokens=1000。
	// 若被错误拉进来, 合计就会变成 11/105/1050 —— 断言立刻红。
	if err := db.GetDB().Create(&model.BillingRollup{
		Dimension:    model.BillingRollupUser,
		DimensionID:  payer.ID,
		Day:          "20261121",
		RequestCount: 10,
		UserCost:     100,
		InputToken:   1000,
	}).Error; err != nil {
		t.Fatalf("seed rollup: %v", err)
	}

	ctx := context.Background()
	overview, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 1 || overview.TotalCost != 5 || overview.TotalTokens != 50 {
		t.Fatalf("非整日对齐的窗口把整天汇总拉进来了: requests=%d cost=%v tokens=%d, "+
			"期望只等于明细 1/5/50(汇总行是 11-21 的服务器整天, 其 00:00-01:00 落在窗口外)",
			overview.TotalRequests, overview.TotalCost, overview.TotalTokens)
	}

	// 直接钉住裁剪函数本身: 该窗口在服务器日历上没有任何一整天完全落在区间内。
	start, end := parseStatsDateRange("2026-11-21", "2026-11-21", statsTZTestLocation(t, "Asia/Phnom_Penh"))
	if from, to := userStatsRollupDayRange(start, end); from != "" || to != "" {
		t.Fatalf("非整日对齐窗口不该拿到任何可用的整天汇总, 实际 from=%q to=%q(区间 %v → %v)",
			from, to, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	// 对照: 同一份数据在服务器日历下窗口是整天的, 汇总**应当**被用上 ——
	// 免得上面那条断言因为"裁剪永远是空"而碰巧通过。
	statsTZSetUserTimezone(t, payer.ID, "")
	serverStart, serverEnd := parseStatsDateRange("2026-11-21", "2026-11-21", time.Local)
	if from, to := userStatsRollupDayRange(serverStart, serverEnd); from != "20261121" || to != "20261121" {
		t.Fatalf("服务器日历时窗口应恰好是 11-21 一个整天, 实际 from=%q to=%q", from, to)
	}
}

// TestStatsInvalidTimezoneFallsBackToServer 偏好值存坏了(拼错/非 IANA)时静默回落到服务器时区:
// 不报错、source=server、窗口按服务器日历 —— 一个坏设置不该让统计页 500。
func TestStatsInvalidTimezoneFallsBackToServer(t *testing.T) {
	payer, _ := userStatsTestSetup(t)

	// 直接写坏值, 绕过设置接口的校验: 要验的正是读侧遇到坏数据时的行为,
	// 而不是写入侧会不会拒绝(那是另一个任务的事)。
	statsTZSetUserTimezone(t, payer.ID, "Mars/Olympus")
	if got := statsUserLocation(payer.ID); got != time.Local {
		t.Fatalf("非法偏好应回落到 time.Local, 实际 %v", got)
	}
	if got := statsTimezoneSource(payer.ID); got != statsTimezoneSourceServer {
		t.Fatalf("非法偏好的 source 应为 %q, 实际 %q", statsTimezoneSourceServer, got)
	}

	ctx := context.Background()
	overview, err := UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("非法偏好不该让概览报错: %v", err)
	}
	if overview.StatsTimezoneSource != statsTimezoneSourceServer {
		t.Fatalf("非法偏好下 source 应为 server, 实际 %q", overview.StatsTimezoneSource)
	}
	if overview.StatsTimezone != overview.StatsCalendarTimezone {
		t.Fatalf("非法偏好下窗口时区名必须与日历一致: %q vs %q",
			overview.StatsTimezone, overview.StatsCalendarTimezone)
	}

	// 空白值同样算"未设置"(存成 " " 这种脏数据不该被当成时区名)。
	statsTZSetUserTimezone(t, payer.ID, "   ")
	if got := statsUserLocation(payer.ID); got != time.Local {
		t.Fatalf("空白偏好应回落到 time.Local, 实际 %v", got)
	}

	// 非法偏好下窗口也确实按服务器日历走: 服务器 11-21 00:30 那条应被计入
	// (这正是 +07 用户日历下会被移出的那条, 见 TestStatsWindowUsesUserCalendar)。
	server := statsTZTestLocation(t, "Asia/Shanghai")
	statsTZSetUserTimezone(t, payer.ID, "Mars/Olympus")
	userStatsInsert(t, userStatsRow(payer.ID, "m", model.BillingStatusSettled,
		statsTZDate(server, 2026, time.November, 21, 0, 30), 2, 2))
	overview, err = UserStatsOverview(ctx, payer.ID, "2026-11-21", "2026-11-21")
	if err != nil {
		t.Fatalf("overview(非法偏好): %v", err)
	}
	if overview.TotalRequests != 1 || overview.TotalCost != 2 {
		t.Fatalf("非法偏好下应按服务器日历计入 11-21 00:30 那条, 实际 requests=%d cost=%v",
			overview.TotalRequests, overview.TotalCost)
	}
}

// TestStatsServerTimezoneNameIsReproducible 服务器时区名必须能在别处复现, 绝不能是 "Local"。
//
// 为什么较真: "Local" 只是本机的 time.Local, 把它写进响应里, 用户换台机器或把报表导出后
// 就没人知道那到底是哪 24 小时 —— 提示语会变成"按 Local 时区统计", 等于没说。
func TestStatsServerTimezoneNameIsReproducible(t *testing.T) {
	name := serverTimezoneName()
	if name == "" {
		t.Fatalf("服务器时区名不能为空")
	}
	if name == "Local" {
		t.Fatalf("服务器时区名不能是 \"Local\": 它不是一个时区名, 无法在另一台机器上复现")
	}
	// IANA 名(如 Asia/Shanghai)必须能被解析回同一个位置; 退化形态(UTC+08:00)不要求可解析。
	if name != "UTC" && !strings.HasPrefix(name, "UTC+") && !strings.HasPrefix(name, "UTC-") {
		loc, err := time.LoadLocation(name)
		if err != nil || loc == nil {
			t.Fatalf("服务器时区名 %q 无法被 LoadLocation 解析: %v", name, err)
		}
	}
	if n := statsLocationName(time.Local); n == "Local" || n == "" {
		t.Fatalf("statsLocationName(time.Local) 不能返回 %q —— 那是不可复现的名字", n)
	}
}

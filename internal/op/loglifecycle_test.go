package op

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestMaskJSONBody(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantMask bool
	}{
		{"simple api_key", "{\"api_key\":\"sk-123\",\"model\":\"gpt\"}", true},
		{"nested authorization", "{\"headers\":{\"Authorization\":\"Bearer x\"},\"model\":\"m\"}", true},
		{"array of tokens", "{\"items\":[{\"token\":\"abc\"},{\"token\":\"def\"}]}", true},
		{"password field", "{\"password\":\"pw123456\",\"model\":\"m\"}", true},
		{"non json passthrough", "plain text", false},
		{"case insensitive", "{\"API_KEY\":\"sk-1\",\"MODEL\":\"m\"}", true},
		{"hyphen normalized", "{\"access-token\":\"sk-2\"}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := MaskJSONBody(tc.input)
			if tc.wantMask {
				if !strings.Contains(result, "***") {
					t.Errorf("expected mask marker in %s", result)
				}
				if strings.Contains(result, "sk-123") || strings.Contains(result, "Bearer x") || strings.Contains(result, "sk-1") || strings.Contains(result, "sk-2") || strings.Contains(result, "pw123456") {
					t.Errorf("masked body still contains sensitive value: %s", result)
				}
			} else {
				if result != tc.input {
					t.Errorf("passthrough expected: got %s", result)
				}
			}
		})
	}
}

// logSettingsDefaultEnv 为"断言默认值"的用例准备一个刚初始化的库。
//
// 这两个用例断言的是**出厂默认**, 而设置读取走进程级 settingCache: 同一个包里的日志
// 生命周期用例会把 log_retention_days 写成 1。默认顺序下它们排在前面所以一直绿,
// -shuffle 下谁先跑谁定, 于是"默认值"变成"上一个用例留下的值"(实测跑 6 个种子红 1 个)。
// 换库前必须连同缓存一起重置, 光换库没用 —— 缓存不跟着库走。
func logSettingsDefaultEnv(t *testing.T) {
	t.Helper()
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "defaults.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func TestShouldStoreBody(t *testing.T) {
	logSettingsDefaultEnv(t)
	if !ShouldStoreBody() {
		t.Error("default should store body")
	}
}

func TestLogRetentionDaysDefault(t *testing.T) {
	logSettingsDefaultEnv(t)
	days := LogRetentionDays()
	if days != 7 {
		t.Errorf("default retention should be 7, got %d", days)
	}
}

// 以下用例钉死日志生命周期"先归档再清理"的顺序契约:
//   - 归档必须拿到清空前的完整正文, 否则归档里只剩空壳;
//   - 归档失败时正文与行都必须原封不动, 不能"先丢证据再报错"。

// logLifecycleTestEnv 初始化隔离库, 并把工作目录切到临时目录(归档目录是相对路径 data/archives)。
func logLifecycleTestEnv(t *testing.T, archiveEnabled bool) {
	t.Helper()
	// 换库前清空进程级缓存, 否则 -shuffle 下会读到上一个用例的旧库数据。
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "logs.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	value := "false"
	if archiveEnabled {
		value = "true"
	}
	if err := SettingUpsert(model.SettingKeyLogArchiveEnabled, value); err != nil {
		t.Fatalf("set archive enabled: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyLogRetentionDays, "1"); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	t.Chdir(t.TempDir())
}

// logLifecycleSeedExpired 落一条超过留存期、带正文的调用日志。
func logLifecycleSeedExpired(t *testing.T, id uint64) {
	t.Helper()
	row := model.RelayRequest{
		ID:           id,
		UserID:       1,
		Model:        "gpt-4o",
		Status:       "success",
		StartedAt:    time.Now().AddDate(0, 0, -10),
		RequestBody:  "{\"prompt\":\"keep-me\"}",
		ResponseBody: "{\"answer\":\"archived-me\"}",
	}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("create relay request: %v", err)
	}
}

// TestLogLifecycleArchivesBodiesBeforeClear 归档文件里必须是清空前的正文。
func TestLogLifecycleArchivesBodiesBeforeClear(t *testing.T) {
	logLifecycleTestEnv(t, true)
	logLifecycleSeedExpired(t, 1001)

	result, err := LogLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("LogLifecycleRun: %v", err)
	}
	if !result.Enabled || result.Archived != 1 || result.RequestsDel != 1 || result.BodyCleared != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	raw, err := os.ReadFile(result.File)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	archived := string(raw)
	// 顺序反了(先清正文再归档)时这两段会变成空串, 归档就失去回溯价值。
	if !strings.Contains(archived, "keep-me") || !strings.Contains(archived, "archived-me") {
		t.Fatalf("archive lost request/response body: %s", archived)
	}
	var left int64
	if err := db.GetDB().Model(&model.RelayRequest{}).Where("id = ?", 1001).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("expired request should be deleted after archive, still %d row(s)", left)
	}
}

// TestLogLifecycleArchiveFailureKeepsData 归档失败时正文与行都不能动。
func TestLogLifecycleArchiveFailureKeepsData(t *testing.T) {
	logLifecycleTestEnv(t, true)
	logLifecycleSeedExpired(t, 1002)
	// 让归档目录无法创建: data 被一个普通文件占住, MkdirAll("data/archives") 必然失败。
	if err := os.WriteFile("data", []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}

	result, err := LogLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("LogLifecycleRun: %v", err)
	}
	if result.ArchiveError == "" {
		t.Fatalf("expected archive error to be reported: %+v", result)
	}
	if result.BodyCleared != 0 || result.RequestsDel != 0 || result.AttemptsDel != 0 {
		t.Fatalf("failed archive must not clear or delete anything: %+v", result)
	}
	var row model.RelayRequest
	if err := db.GetDB().First(&row, 1002).Error; err != nil {
		t.Fatalf("expired request should survive a failed archive: %v", err)
	}
	if row.RequestBody == "" || row.ResponseBody == "" {
		t.Fatalf("bodies must survive a failed archive, got request=%q response=%q", row.RequestBody, row.ResponseBody)
	}
}

// TestLogLifecycleWithoutArchiveStillCleans 归档关闭时按登记的清理口径直接处理。
func TestLogLifecycleWithoutArchiveStillCleans(t *testing.T) {
	logLifecycleTestEnv(t, false)
	logLifecycleSeedExpired(t, 1003)

	result, err := LogLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("LogLifecycleRun: %v", err)
	}
	if result.Enabled || result.Archived != 0 || result.File != "" {
		t.Fatalf("archive is disabled, nothing should be written: %+v", result)
	}
	if result.BodyCleared != 1 || result.RequestsDel != 1 {
		t.Fatalf("expected cleanup without archive: %+v", result)
	}
}

// TestLogLifecycleCutoffAlignedToDay 清理截止必须对齐自然日 00:00。
//
// 用 time.Now().AddDate(0, 0, -retention) 时, 留存末日只剩"本轮运行时刻之后"的半天样本:
// 成功率、延迟这类按日汇总的指标会随运行时刻漂移, 而前端覆盖率提示只到日粒度,
// 用户会把那半天当成一整天。对齐到 00:00 后每轮都以整天为单位, 结果与运行时刻无关。
func TestLogLifecycleCutoffAlignedToDay(t *testing.T) {
	logLifecycleTestEnv(t, false)

	result, err := LogLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("LogLifecycleRun: %v", err)
	}
	want := dayStart(time.Now()).AddDate(0, 0, -LogRetentionDays())
	if !result.Cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s (自然日 00:00, 与 billingRetentionCutoff 同规则)",
			result.Cutoff.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	// 时分秒毫秒都必须为 0 —— 只比较日期会放过"带时分秒但日期正确"的实现。
	year, month, day := result.Cutoff.Date()
	hour, minute, second := result.Cutoff.Clock()
	if hour != 0 || minute != 0 || second != 0 || result.Cutoff.Nanosecond() != 0 {
		t.Fatalf("cutoff 时分秒非零: %04d-%02d-%02d %02d:%02d:%02d.%d",
			year, month, day, hour, minute, second, result.Cutoff.Nanosecond())
	}
	if result.Cutoff.Location() != time.Local {
		t.Fatalf("cutoff 时区 = %s, want 本地挂钟", result.Cutoff.Location())
	}
}

// TestLogLifecycleCutoffBoundaryDeletesStrictlyBefore 对齐天后, 删除条件仍是 created_at < cutoff:
// 截止日前一天的日志删, 截止日当天 00:00 的日志留(边界那一秒属于留存期)。
func TestLogLifecycleCutoffBoundaryDeletesStrictlyBefore(t *testing.T) {
	logLifecycleTestEnv(t, false)
	cutoff := dayStart(time.Now()).AddDate(0, 0, -LogRetentionDays())
	seed := func(id uint64, at time.Time, status string) {
		row := model.RelayRequest{ID: id, UserID: 1, Model: "gpt-4o", Status: status, StartedAt: at}
		if err := db.GetDB().Create(&row).Error; err != nil {
			t.Fatalf("seed row %d: %v", id, err)
		}
	}
	seed(2001, cutoff.Add(-time.Second), "success") // 截止前一秒: 删
	seed(2002, cutoff, "success")                   // 截止那一刻: 留(条件严格小于)
	seed(2003, cutoff.Add(24*time.Hour), "success") // 留存期内: 留

	result, err := LogLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("LogLifecycleRun: %v", err)
	}
	if result.RequestsDel != 1 {
		t.Fatalf("requests_deleted = %d, want 1 (只有严格早于 cutoff 的那条)", result.RequestsDel)
	}
	var left int64
	if err := db.GetDB().Model(&model.RelayRequest{}).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("剩余 %d 行, want 2 (cutoff 当刻与留存期内的两条都必须留下)", left)
	}
}

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

func TestShouldStoreBody(t *testing.T) {
	if !ShouldStoreBody() {
		t.Error("default should store body")
	}
}

func TestLogRetentionDaysDefault(t *testing.T) {
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

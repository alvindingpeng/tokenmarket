package op

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// withBillingSettings 在测试内临时设定资金表留存配置。
// 用 SettingUpsert 而不是 SettingSetString: 测试库不播种默认设置, 后者会因键不存在失败。
func withBillingSettings(t *testing.T, days int, archive bool) {
	t.Helper()
	if err := SettingUpsert(model.SettingKeyBillingRetentionDays, fmt.Sprintf("%d", days)); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	archiveValue := "false"
	if archive {
		archiveValue = "true"
	}
	if err := SettingUpsert(model.SettingKeyBillingArchiveEnabled, archiveValue); err != nil {
		t.Fatalf("set archive flag: %v", err)
	}
}

// seedBillingRecord 造一条指定账龄的计费明细(金额三方对平)。
func seedBillingRecord(t *testing.T, userID, ownerID uint, channelID int, age time.Duration, userCost, ownerRevenue float64) model.BillingRecord {
	t.Helper()
	record := model.BillingRecord{
		UserID:          userID,
		OwnerID:         ownerID,
		ChannelID:       channelID,
		ModelName:       "gpt-test",
		InputToken:      1000,
		OutputToken:     500,
		UserCost:        userCost,
		OwnerRevenue:    ownerRevenue,
		PlatformRevenue: userCost - ownerRevenue,
		Status:          model.BillingStatusSettled,
		CreatedAt:       time.Now().Add(-age),
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed billing record: %v", err)
	}
	return record
}

// TestBillingRetentionSealsBeforeDelete 留存清理必须"先固化汇总再删明细":
// 明细被删掉之后, 终身口径(累计消费/全期毛利/可提现额度)必须一分不差。
func TestBillingRetentionSealsBeforeDelete(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, true)

	// 一年前的旧账 + 一条近期账; 只有旧的该被清掉。
	seedBillingRecord(t, 7, 8, 3, 400*24*time.Hour, 10, 6)
	seedBillingRecord(t, 7, 8, 3, 10*24*time.Hour, 5, 3)

	before, err := BillingTotal(context.Background(), "user", 7)
	if err != nil {
		t.Fatalf("billing total before: %v", err)
	}
	if before.RequestCount != 2 {
		t.Fatalf("records before = %d, want 2", before.RequestCount)
	}

	result, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedRecords != 1 {
		t.Fatalf("deleted records = %d, want 1", result.DeletedRecords)
	}
	if result.SealedDays != 1 {
		t.Fatalf("sealed days = %d, want 1", result.SealedDays)
	}
	if result.Archived != 1 {
		t.Fatalf("archived = %d, want 1 (归档必须覆盖被删的明细)", result.Archived)
	}

	// 归档文件必须真的落盘。
	data, err := os.ReadFile(result.ArchiveFile)
	if err != nil {
		t.Fatalf("read archive %s: %v", result.ArchiveFile, err)
	}
	if len(data) == 0 {
		t.Fatal("billing archive file is empty")
	}

	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Count(&live).Error; err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if live != 1 {
		t.Fatalf("live records = %d, want 1", live)
	}

	// 关键断言: 明细少了一条, 终身口径必须一条不少。
	after, err := BillingTotal(context.Background(), "user", 7)
	if err != nil {
		t.Fatalf("billing total after: %v", err)
	}
	if after.RequestCount != before.RequestCount {
		t.Fatalf("request count shrank: before=%d after=%d (汇总没有补上被删的明细)", before.RequestCount, after.RequestCount)
	}
	if after.UserCost < before.UserCost-1e-9 || after.UserCost > before.UserCost+1e-9 {
		t.Fatalf("user cost changed: before=%v after=%v", before.UserCost, after.UserCost)
	}
	if after.OwnerRevenue != before.OwnerRevenue || after.PlatformRevenue != before.PlatformRevenue {
		t.Fatalf("revenue changed: before=(%v,%v) after=(%v,%v)",
			before.OwnerRevenue, before.PlatformRevenue, after.OwnerRevenue, after.PlatformRevenue)
	}
}

// TestBillingRetentionIsIdempotent 重复执行不该重复固化: 第二次必须什么都不做,
// 否则汇总表会把同一批明细累加两遍, 终身口径随执行次数虚高。
func TestBillingRetentionIsIdempotent(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, true)
	seedBillingRecord(t, 5, 6, 2, 400*24*time.Hour, 10, 6)

	first, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.DeletedRecords != 1 || first.SealedDays != 1 {
		t.Fatalf("first run deleted=%d sealed=%d, want 1/1", first.DeletedRecords, first.SealedDays)
	}
	total1, err := BillingTotal(context.Background(), "user", 5)
	if err != nil {
		t.Fatalf("total after first: %v", err)
	}

	second, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.DeletedRecords != 0 || second.SealedDays != 0 {
		t.Fatalf("second run must be a no-op, got deleted=%d sealed=%d", second.DeletedRecords, second.SealedDays)
	}
	total2, err := BillingTotal(context.Background(), "user", 5)
	if err != nil {
		t.Fatalf("total after second: %v", err)
	}
	if total2.RequestCount != total1.RequestCount || total2.UserCost != total1.UserCost {
		t.Fatalf("second run changed lifetime totals: first=%+v second=%+v", total1, total2)
	}
}

// TestBillingRetentionKeepsDisputedRecords 被未驳回申诉引用的明细不能删:
// 批准退款那条路径要按 dispute.RecordID 回查并改它的 status, 行没了就退不了款。
func TestBillingRetentionKeepsDisputedRecords(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)

	// 载体: 让申诉指向一条一年前的旧账。
	old := seedBillingRecord(t, 9, 10, 4, 400*24*time.Hour, 20, 12)
	dispute := model.Dispute{RecordID: old.ID, UserID: 9, Status: model.DisputeStatusPending}
	if err := db.GetDB().Create(&dispute).Error; err != nil {
		t.Fatalf("seed dispute: %v", err)
	}

	result, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedRecords != 0 {
		t.Fatalf("deleted records = %d, want 0 (被申诉引用的行必须保留)", result.DeletedRecords)
	}
	if result.RetainedRecords != 1 {
		t.Fatalf("retained records = %d, want 1", result.RetainedRecords)
	}
	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Count(&live).Error; err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if live != 1 {
		t.Fatalf("live records = %d, want 1", live)
	}

	// 申诉被驳回后同一个窗口就变回可删; 这一轮才算真的清掉。
	if err := db.GetDB().Model(&model.Dispute{}).Where("id = ?", dispute.ID).
		Update("status", model.DisputeStatusRejected).Error; err != nil {
		t.Fatalf("reject dispute: %v", err)
	}
	after, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention after rejection: %v", err)
	}
	if after.DeletedRecords != 1 {
		t.Fatalf("deleted records after rejection = %d, want 1", after.DeletedRecords)
	}
}

// TestBillingRetentionKeepsReservationsWithSettlementIntent 有结算任务的预扣行不能删:
// BillingReleaseStale 用 NOT EXISTS(结算任务) 判断"有没有结算意图", 删掉任务行会让一笔
// 仍在等结算的预扣被当成泄漏释放 —— 冻结额退了, 随后结算再落账, 同一笔钱既退又扣。
func TestBillingRetentionKeepsReservationsWithSettlementIntent(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)

	oldReserve := model.BalanceReservation{
		UserID: 3, RequestID: 9001, Amount: 1.5, Settled: true,
		CreatedAt: time.Now().Add(-400 * 24 * time.Hour),
	}
	if err := db.GetDB().Create(&oldReserve).Error; err != nil {
		t.Fatalf("seed reservation: %v", err)
	}
	// created_at/updated_at 必须显式回填: gorm 会把零值时间自动写成"现在",
	// 那样这条任务就永远"很新", 留存判定依据 updated_at 会正确地不删它 —— 用例就测了个空。
	task := model.BillingSettlementTask{
		ReservationID: oldReserve.ID, RequestID: 9001, UserID: 3,
		Status: model.BillingSettlementStatusCompleted, Attempts: 1,
		Snapshot:  "{}",
		CreatedAt: time.Now().Add(-400 * 24 * time.Hour),
		UpdatedAt: time.Now().Add(-400 * 24 * time.Hour),
	}
	if err := db.GetDB().Create(&task).Error; err != nil {
		t.Fatalf("seed settlement task: %v", err)
	}
	// 另一条纯老账: 已结算 + 无结算任务, 该被清掉。
	plainReserve := model.BalanceReservation{
		UserID: 3, RequestID: 9002, Amount: 0.5, Settled: true,
		CreatedAt: time.Now().Add(-400 * 24 * time.Hour),
	}
	if err := db.GetDB().Create(&plainReserve).Error; err != nil {
		t.Fatalf("seed plain reservation: %v", err)
	}

	result, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedReserves != 1 {
		t.Fatalf("deleted reservations = %d, want 1 (只有无结算意图的那条可删)", result.DeletedReserves)
	}
	// 有结算意图的那条预扣必须还在, 否则 BillingReleaseStale 会误释放它。
	var kept int64
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", oldReserve.ID).Count(&kept).Error; err != nil {
		t.Fatalf("count kept reservation: %v", err)
	}
	if kept != 1 {
		t.Fatal("reservation with settlement intent was deleted; BillingReleaseStale would release it as a leak")
	}
	// 结算任务行也随同清理: 对应预扣已结算且无未结算依赖, 任务行可删。
	if result.DeletedTasks != 1 {
		t.Fatalf("deleted settlement tasks = %d, want 1", result.DeletedTasks)
	}
}

// TestBillingRetentionKeepsPendingSettlementTasks pending/retry/manual 状态的结算任务不能删:
// manual 是人工对账的唯一证据, 自动路径已经放弃判断, 人要看的就是那张快照。
func TestBillingRetentionKeepsPendingSettlementTasks(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)

	for _, status := range []string{
		model.BillingSettlementStatusPending,
		model.BillingSettlementStatusRetry,
		model.BillingSettlementStatusManual,
	} {
		reserve := model.BalanceReservation{
			UserID: 4, RequestID: 9100 + uint64(len(status)), Amount: 1, Settled: true,
			CreatedAt: time.Now().Add(-400 * 24 * time.Hour),
		}
		if err := db.GetDB().Create(&reserve).Error; err != nil {
			t.Fatalf("seed reservation: %v", err)
		}
		task := model.BillingSettlementTask{
			ReservationID: reserve.ID, RequestID: reserve.RequestID, UserID: 4,
			Status: status, Attempts: 1, Snapshot: "{}",
			CreatedAt: time.Now().Add(-400 * 24 * time.Hour),
			UpdatedAt: time.Now().Add(-400 * 24 * time.Hour),
		}
		if err := db.GetDB().Create(&task).Error; err != nil {
			t.Fatalf("seed settlement task %s: %v", status, err)
		}
	}

	result, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedTasks != 0 {
		t.Fatalf("deleted settlement tasks = %d, want 0 (非 completed 的任务行是未完成的证据)", result.DeletedTasks)
	}
	var live int64
	if err := db.GetDB().Model(&model.BillingSettlementTask{}).Count(&live).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if live != 3 {
		t.Fatalf("live settlement tasks = %d, want 3", live)
	}
}

// TestBillingRetentionArchiveFailureDeletesNothing 归档失败必须一条都不删:
// 先删后归档, 或归档没落盘就返回成功, 都会让"记录没了、副本也没了"同时发生。
func TestBillingRetentionArchiveFailureDeletesNothing(t *testing.T) {
	breakerTestSetup(t)
	dir := chdirTemp(t)
	withBillingSettings(t, 180, true)

	seedBillingRecord(t, 11, 12, 5, 400*24*time.Hour, 10, 6)

	// 把归档目录占成一个普通文件: MkdirAll 会失败, 归档必然失败。
	if err := os.MkdirAll(filepath.Join(dir, "data", "archives"), 0o750); err != nil {
		t.Fatalf("mkdir archives: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "archives", "billing"), []byte("not a dir"), 0o640); err != nil {
		t.Fatalf("occupy archive path: %v", err)
	}

	result, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention should report archive failure in result, not as an error: %v", err)
	}
	if result.ArchiveError == "" {
		t.Fatal("archive failure was not reported")
	}
	if result.DeletedRecords != 0 || result.DeletedLedgers != 0 || result.DeletedReserves != 0 {
		t.Fatalf("archive failure must delete nothing, got records=%d ledgers=%d reserves=%d",
			result.DeletedRecords, result.DeletedLedgers, result.DeletedReserves)
	}
	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Count(&live).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if live != 1 {
		t.Fatalf("live records = %d, want 1 (归档失败时记录必须完好)", live)
	}
}

// TestBillingRetentionKeepsRecentRecords 留存期内的记录一条都不能动。
func TestBillingRetentionKeepsRecentRecords(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, true)

	seedBillingRecord(t, 13, 14, 6, 10*24*time.Hour, 10, 6)
	seedBillingRecord(t, 13, 14, 6, 179*24*time.Hour, 4, 2)

	result, err := BillingRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedRecords != 0 || result.SealedDays != 0 {
		t.Fatalf("nothing should be deleted inside the window, got records=%d sealed=%d", result.DeletedRecords, result.SealedDays)
	}
	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Count(&live).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if live != 2 {
		t.Fatalf("live records = %d, want 2", live)
	}
}

// TestBillingRetentionSettingsBounds 非法配置必须回落到安全值, 不能变成"永不清理"或"全删"。
func TestBillingRetentionSettingsBounds(t *testing.T) {
	breakerTestSetup(t)
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 180},
		{"0", 180},
		{"-5", 180},
		{"30", 30},
		{"99999", 3650},
	} {
		if tc.value == "" {
			if err := db.GetDB().Where("key = ?", model.SettingKeyBillingRetentionDays).Delete(&model.Setting{}).Error; err != nil {
				t.Fatalf("clear setting: %v", err)
			}
			resetProcessCaches()
		} else if err := SettingUpsert(model.SettingKeyBillingRetentionDays, tc.value); err != nil {
			t.Fatalf("set %s: %v", tc.value, err)
		}
		if got := BillingRetentionDays(); got != tc.want {
			t.Fatalf("BillingRetentionDays(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

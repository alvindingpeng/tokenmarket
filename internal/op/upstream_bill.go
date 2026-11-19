package op

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// UpstreamBillRow CSV 导入的一行原始输入。
type UpstreamBillRow struct {
	ModelName string
	Currency  string
	Cost      float64
}

// UpstreamBillImport 导入渠道某账期的上游账单并对单。
//
// CSV 形状(表头必需, 顺序固定): model,currency,cost
// 允许大小写混写与多余空白; 币种必须在白名单内。同一 (渠道, 账期, 模型) 重复导入按"覆盖"处理 ——
// 供应商补发修订版账单是常态, 若做成追加就会把同一笔成本记两次, 差异凭空翻倍。
// 对单基准是同期 billing_records 的 owner_revenue 合计(已含折算), 因此导入侧也必须把
// 原币成本按同一口径折成 USD: 两边都折, 差异才是真实差异而不是汇率差。
func UpstreamBillImport(ctx context.Context, channelID int, period string, r io.Reader) (int, error) {
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return 0, fmt.Errorf("channel not found")
	}
	period = model.NormalizeUpstreamBillPeriod(period)
	if len(period) != 6 {
		return 0, fmt.Errorf("period must be YYYY-MM or YYYYMM")
	}
	rows, err := parseUpstreamBillCSV(r)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, row := range rows {
		currency := model.NormalizeCurrency(row.Currency)
		if !model.ValidPriceCurrency(currency) {
			return imported, fmt.Errorf("model %s: unsupported currency %q", row.ModelName, row.Currency)
		}
		if err := model.ValidateUpstreamBillAmount(row.Cost); err != nil {
			return imported, fmt.Errorf("model %s: %w", row.ModelName, err)
		}
		costUSD, rate, _, err := FxRateConvert(ctx, currency, row.Cost)
		if err != nil {
			// 无牌价时按原价落库并标记 fx_rate=0, 让对单页能看出"这行没折算" ——
			// 拒绝整份导入会让渠道商在牌价服务恢复前完全无法对账, 那更糟。
			costUSD, rate = row.Cost, 0
		}
		ownerRev, err := upstreamBillOwnerRevenue(ctx, channelID, period, row.ModelName)
		if err != nil {
			return imported, err
		}
		record := model.UpstreamBill{
			ChannelID: channelID,
			Period:    period,
			ModelName: strings.TrimSpace(row.ModelName),
			Currency:  currency,
			Cost:      row.Cost,
			CostUSD:   costUSD,
			FxRate:    rate,
			OwnerRev:  ownerRev,
			Status:    upstreamBillStatus(costUSD, ownerRev),
		}
		record.DiffRatio = upstreamBillDiffRatio(costUSD, ownerRev)
		// 覆盖式 upsert: 唯一键 (channel_id, period, model_name)。
		// 不用 Where+Assign 的 FirstOrCreate 组合: 它的回填语义对新增行只会落 Where 条件字段,
		// Assign 列并不可靠地写进新行 —— 显式先查后写, 两分支都写全量字段, 行为可读。
		var existing model.UpstreamBill
		lookupErr := db.GetDB().WithContext(ctx).
			First(&existing, "channel_id = ? AND period = ? AND model_name = ?", channelID, period, record.ModelName).Error
		if lookupErr == nil {
			if err := db.GetDB().WithContext(ctx).Model(&existing).Updates(map[string]any{
				"currency": record.Currency, "cost": record.Cost, "cost_usd": record.CostUSD,
				"fx_rate": record.FxRate, "owner_rev": record.OwnerRev,
				"diff_ratio": record.DiffRatio, "status": record.Status, "updated_at": time.Now(),
			}).Error; err != nil {
				return imported, fmt.Errorf("update upstream bill %s: %w", record.ModelName, err)
			}
		} else {
			if err := db.GetDB().WithContext(ctx).Create(&record).Error; err != nil {
				return imported, fmt.Errorf("create upstream bill %s: %w", record.ModelName, err)
			}
		}
		imported++
	}
	log.Infof("upstream bill import: channel#%d period=%s rows=%d", channel.ID, period, imported)
	return imported, nil
}

// upstreamBillStatus 按容差定结论; 账单为 0 而平台有收入(或反之)一律 mismatch ——
// 这是最典型的"币种配错/价格没同步", 不是零头。
func upstreamBillStatus(costUSD, ownerRev float64) string {
	if costUSD == 0 && ownerRev == 0 {
		return model.UpstreamBillStatusMatched
	}
	ratio := upstreamBillDiffRatio(costUSD, ownerRev)
	if math.Abs(ratio) <= model.UpstreamBillDiffTolerance {
		return model.UpstreamBillStatusMatched
	}
	return model.UpstreamBillStatusMismatch
}

// upstreamBillDiffRatio 相对差异; 账单为 0 时用绝对差值表示(避免除零), 便于排序看出量级。
func upstreamBillDiffRatio(costUSD, ownerRev float64) float64 {
	if costUSD == 0 {
		if ownerRev == 0 {
			return 0
		}
		return ownerRev
	}
	return (ownerRev - costUSD) / costUSD
}

// upstreamBillOwnerRevenue 同期同模型的平台侧供货成本合计(USD 口径)。
// 口径说明: 与上游账单比的是"我们付给上游的钱", 即 owner_revenue(供货价 × 用量),
// 不是用户支出(含上浮)。period 形如 YYYYMM, 转成当月首末日做范围查询。
func upstreamBillOwnerRevenue(ctx context.Context, channelID int, period, modelName string) (float64, error) {
	start, err := time.ParseInLocation("200601", period, time.Local)
	if err != nil {
		return 0, fmt.Errorf("invalid period %q: %w", period, err)
	}
	end := start.AddDate(0, 1, 0)
	var total *float64
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select("COALESCE(SUM(owner_revenue), 0)").
		Where("channel_id = ? AND model_name = ? AND created_at >= ? AND created_at < ?", channelID, modelName, start, end).
		Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum owner revenue: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// parseUpstreamBillCSV 解析账单 CSV: 表头必须是 model,currency,cost(大小写与空白不敏感)。
// 表头校验是有意的: 供应商账单列序各不相同, 静默按位置读会把"token 数"当成本导入。
func parseUpstreamBillCSV(r io.Reader) ([]UpstreamBillRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = 3
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read csv header: %w", err)
	}
	for i := range header {
		header[i] = strings.ToLower(strings.TrimSpace(header[i]))
	}
	want := []string{"model", "currency", "cost"}
	for i, name := range want {
		if i >= len(header) || header[i] != name {
			return nil, fmt.Errorf("csv header must be model,currency,cost")
		}
	}
	rows := make([]UpstreamBillRow, 0, 64)
	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv line %d: %w", line, err)
		}
		name := strings.TrimSpace(record[0])
		if name == "" {
			return nil, fmt.Errorf("csv line %d: model name is empty", line)
		}
		cost, err := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("csv line %d: cost %q is not a number", line, record[2])
		}
		rows = append(rows, UpstreamBillRow{ModelName: name, Currency: record[1], Cost: cost})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("csv has no data rows")
	}
	return rows, nil
}

// UpstreamBillList 返回某渠道某账期的对单行, 差异大的排前面(管理员先看最值钱的差异)。
func UpstreamBillList(ctx context.Context, channelID int, period string) ([]model.UpstreamBill, error) {
	rows := make([]model.UpstreamBill, 0, 32)
	query := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID)
	if period != "" {
		query = query.Where("period = ?", model.NormalizeUpstreamBillPeriod(period))
	}
	if err := query.Order("CASE status WHEN 'mismatch' THEN 0 ELSE 1 END, ABS(diff_ratio) DESC, model_name ASC").
		Limit(500).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list upstream bills: %w", err)
	}
	return rows, nil
}

// UpstreamBillExplain 人工定性一条差异(折扣/税费/供应商口径), 只改说明与状态, 不动金额 ——
// 账单金额是供应商的原始凭据, 平台侧改它等于改证据。
// 只有 mismatch 行允许定性: matched 行无可解释的差异, explained 已定性, 重复写说明会覆盖前一次判断。
func UpstreamBillExplain(ctx context.Context, id uint64, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return fmt.Errorf("note is required to explain a difference")
	}
	result := db.GetDB().WithContext(ctx).Model(&model.UpstreamBill{}).
		Where("id = ? AND status = ?", id, model.UpstreamBillStatusMismatch).
		Updates(map[string]any{"status": model.UpstreamBillStatusExplained, "note": note, "updated_at": time.Now()})
	if result.Error != nil {
		return fmt.Errorf("explain upstream bill: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("upstream bill %d is not an open mismatch", id)
	}
	return nil
}

// UpstreamBillRecompute 重新对单(牌价或计费数据补齐后重跑): 重算 owner_revenue 与差额。
// 不改已 explained 的结论 —— 人已经下过判断的行不该被自动流程翻掉。
func UpstreamBillRecompute(ctx context.Context, channelID int, period string) (int, error) {
	rows, err := UpstreamBillList(ctx, channelID, period)
	if err != nil {
		return 0, err
	}
	recomputed := 0
	for _, row := range rows {
		if row.Status == model.UpstreamBillStatusExplained {
			continue
		}
		ownerRev, err := upstreamBillOwnerRevenue(ctx, channelID, row.Period, row.ModelName)
		if err != nil {
			return recomputed, err
		}
		costUSD := row.CostUSD
		// 首轮导入时若牌价缺失会落 fx_rate=0, 此时补折一次。
		if row.FxRate == 0 {
			if converted, rate, _, err := FxRateConvert(ctx, row.Currency, row.Cost); err == nil {
				costUSD, _ = converted, rate
			}
		}
		status := upstreamBillStatus(costUSD, ownerRev)
		if err := db.GetDB().WithContext(ctx).Model(&model.UpstreamBill{}).Where("id = ?", row.ID).
			Updates(map[string]any{
				"cost_usd": costUSD, "owner_rev": ownerRev,
				"diff_ratio": upstreamBillDiffRatio(costUSD, ownerRev),
				"status":     status, "updated_at": time.Now(),
			}).Error; err != nil {
			return recomputed, fmt.Errorf("recompute upstream bill %d: %w", row.ID, err)
		}
		recomputed++
	}
	return recomputed, nil
}

// UpstreamBillGet 按 ID 读单行, 供 handler 做归属校验后再定性。
func UpstreamBillGet(ctx context.Context, id uint64) (model.UpstreamBill, error) {
	var bill model.UpstreamBill
	if err := db.GetDB().WithContext(ctx).First(&bill, "id = ?", id).Error; err != nil {
		return model.UpstreamBill{}, fmt.Errorf("upstream bill %d not found", id)
	}
	return bill, nil
}

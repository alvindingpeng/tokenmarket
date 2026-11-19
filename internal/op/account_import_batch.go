package op

import (
	"context"
	"time"
)

// AccountBatchItem 批量导入里的一个账号。
type AccountBatchItem struct {
	Name string // 凭据名(渠道内唯一)
	Raw  string // 账号 JSON 原文
}

// AccountBatchResult 批量导入里一条的结局。
//
// 为什么批量接口要返回逐条结果而不是只在全部失败时报错:
// 一批里有的账号字段齐全、有的缺 refresh_token, 是常态。若整批只给一个
// "部分失败"的笼统信息, 使用者只能一条条猜哪条出了问题 —— 那比不批量还糟。
type AccountBatchResult struct {
	Name         string
	OK           bool   // 该条是否成功落库。
	ChannelKeyID int    // 成功时是凭据主键, 供界面"立即刷新"。
	Platform     string // 成功时的平台(后端按请求平台解析, 但解析可能纠正)。
	Kind         string // oauth | cookie, 界面据此决定显示与刷新能力。
	Error        string // 失败原因(解析/落库), 成功时为空。
}

// AccountImportBatch 批量导入多个账号到同一渠道。
//
// 关键决策: 批量**不做首刷**(spec.SkipRefresh)。
//   - 逐条首刷 = N 次上游令牌调用, 又慢(串行, 每条最多 20 秒)又可能触发上游风控;
//   - 批量的语义是"快速录入一批账号", 当场验证每个是另一件事 —— 留给账号页统一刷新;
//   - 单条导入的语义不变: 那里"导入即验证"是合理的, 因为使用者只导入一个, 等得起。
//
// 逐条独立: 一条失败(如缺字段、撞名)不影响其他条, 每条独立事务(AccountImport 内部)。
func AccountImportBatch(ctx context.Context, channelID int, platform string, items []AccountBatchItem, enabled bool, now time.Time) []AccountBatchResult {
	results := make([]AccountBatchResult, 0, len(items))
	for _, item := range items {
		result, err := AccountImport(ctx, channelID, AccountImportSpec{
			Platform:     platform,
			Name:         item.Name,
			Raw:          item.Raw,
			Enabled:      enabled,
			SkipRefresh:  true,
		}, now)
		if err != nil {
			// 失败原因要原样带回: 缺 access_token 和缺 refresh_token 对使用者是两件完全不同的事,
			// 前者是贴错文件, 后者是这个账号无法自动续期。笼统报"导入失败"会让人误判问题。
			results = append(results, AccountBatchResult{
				Name:  item.Name,
				OK:    false,
				Error: err.Error(),
			})
			continue
		}
		results = append(results, AccountBatchResult{
			Name:         item.Name,
			OK:           true,
			ChannelKeyID: result.ChannelKeyID,
			Platform:     result.Platform,
			Kind:         result.Kind,
		})
	}
	return results
}

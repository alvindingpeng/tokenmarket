package op

import (
	"context"
	"fmt"

	"github.com/bestruirui/octopus/internal/alert"
)

// 后台任务故障告警。
//
// 改造前任务失败只有 log.Warnf: 备份不再产出、归档停在原地都没人知道,
// 直到磁盘被日志写满才暴露。现在连续失败达到阈值(见 task 包)后经这里进同一条告警管道。

// taskLabels 任务标识到中文名: 告警给管理员看, 不能让他去猜 auto_backup 是什么。
// 名称与 internal/task 里的常量对应; task 依赖 op, 无法反向引用其常量, 故此处以字符串登记,
// 新增任务时两处都要加(未登记也不会漏告警 —— 兜底用原始标识)。
var taskLabels = map[string]string{
	"model_info_update": "模型价格与能力更新",
	"price_update":      "模型价格与能力更新",
	"stats_save":        "统计落库",
	"billing_rollback":  "余额预扣回滚",
	"billing_reconcile": "计费对账",
	"log_archive":       "日志归档清理",
	"auto_backup":       "自动备份",
	"channel_health":    "渠道健康探测",
	"update_check":      "版本更新检查",
	"video_job_cleanup": "生视频任务清理",
}

// taskHints 任务持续失败的实际影响与处置建议: 管理员看完告警就知道该做什么, 而不是只会重启。
var taskHints = map[string]string{
	"model_info_update": "模型价格库会过期, 新计费可能沿用旧价",
	"price_update":      "模型价格库会过期, 新计费可能沿用旧价",
	"stats_save":        "统计面板数据停止更新, 分时报表会缺段落",
	"billing_rollback":  "超时未结算的预扣额不会释放, 用户可用余额被长期冻结",
	"billing_reconcile": "计费差异无人核对, 漏账错账发现不了",
	"log_archive":       "日志表持续膨胀, 磁盘写满会拖垮整个网关",
	"auto_backup":       "数据库没有新备份, 此刻故障将无恢复点",
	"channel_health":    "渠道健康探测停摆, 故障渠道不会再被自动发现",
	"update_check":      "无法获知新版本发布",
	"video_job_cleanup": "过期生视频任务记录堆积",
}

// RaiseTaskFailureAlert 报告后台任务连续失败。
func RaiseTaskFailureAlert(name string, consecutive int, reason string) {
	label := taskLabel(name)
	fields := []alert.Field{
		{Label: "任务", Value: label},
		{Label: "任务标识", Value: name},
		{Label: "连续失败", Value: fmt.Sprintf("%d 次", consecutive)},
		{Label: "最近错误", Value: reason},
	}
	if hint := taskHints[name]; hint != "" {
		fields = append(fields, alert.Field{Label: "影响", Value: hint})
	}
	fields = append(fields, alert.Field{Label: "建议", Value: "查看服务日志定位原因; 与磁盘/上游/网络相关的故障恢复后本任务会自动回到正常节奏"})
	RaiseAlertFacts(context.Background(), AlertFacts{
		Kind: "task_failed",
		// reason 是去重键, 因此既不含最近错误原文也不含失败次数: 达到阈值后每轮都会回调,
		// 任何易变值都会让每轮生成一条新记录, 十分钟内把告警变成骚扰。
		// 具体次数与错误原文放在明细行里, 每次外发都能看到最新值。
		Reason: fmt.Sprintf("task %s failed repeatedly", label),
		Fields: fields,
	})
}

// RaiseTaskRecoveredAlert 报告此前持续失败的任务恢复正常。
func RaiseTaskRecoveredAlert(name string, previousFailures int) {
	label := taskLabel(name)
	RaiseAlertFacts(context.Background(), AlertFacts{
		Kind:   "task_recovered",
		Reason: fmt.Sprintf("task %s recovered", label),
		Fields: []alert.Field{
			{Label: "任务", Value: label},
			{Label: "任务标识", Value: name},
			{Label: "此前连续失败", Value: fmt.Sprintf("%d 次", previousFailures)},
		},
	})
}

// taskLabel 取任务中文名, 未登记时退回原始标识。
func taskLabel(name string) string {
	if label, ok := taskLabels[name]; ok {
		return label
	}
	return name
}

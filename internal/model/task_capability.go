package model

import "time"

// ModelTaskCapability 记录管理员对某模型在处理某类请求时的能力判断。
//
// 它独立于 models.dev 同步出来的 ModelCapability: 后者描述模态与窗口，前者是
// 面向自动路由的能力分和验证证据。models.dev 没有工具调用或编程质量的统一字段，
// 不能从模型名称、价格或上下文推断这些能力；没有登记时路由只能按未知处理。
type ModelTaskCapability struct {
	ModelName  string    `json:"model_name" gorm:"primaryKey;size:255;not null"` // 统一小写的模型名。
	Task       string    `json:"task" gorm:"primaryKey;size:32;not null"`        // general, summary, coding, tool, extraction。
	Score      int       `json:"score" gorm:"not null;default:0"`                // 0-100 质量分；0 表示未验证或明确不达标。
	Verified   bool      `json:"verified" gorm:"not null;default:false"`         // 管理员是否已实际验证过该模型完成此类任务。
	Source     string    `json:"source" gorm:"size:32;not null;default:manual"`  // 当前固定为 manual，为后续接入评测来源保留。
	Notes      string    `json:"notes" gorm:"size:1024;default:''"`              // 验证方法、限制或样本说明。
	VerifiedAt time.Time `json:"verified_at"`                                    // 最近一次人工验证时间。
	UpdatedAt  time.Time `json:"updated_at"`                                     // 最近一次保存时间。
}

// TableName 固定表名，避免 GORM 生成与含义不一致的默认名称。
func (ModelTaskCapability) TableName() string { return "model_task_capabilities" }

// AutoRoutingTasks 是自动路由支持的质量能力任务集合。
var AutoRoutingTasks = []string{
	AutoRoutingTaskGeneral,
	AutoRoutingTaskSummary,
	AutoRoutingTaskCoding,
	AutoRoutingTaskTool,
	AutoRoutingTaskExtraction,
}

// IsValidAutoRoutingTask 校验任务标识。
func IsValidAutoRoutingTask(task string) bool {
	for _, supported := range AutoRoutingTasks {
		if task == supported {
			return true
		}
	}
	return false
}

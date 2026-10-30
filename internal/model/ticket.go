package model

import "time"

// TicketStatus 工单状态。
const (
	TicketStatusOpen       = "open"        // 新建
	TicketStatusInProgress = "in_progress" // 处理中
	TicketStatusResolved   = "resolved"    // 已解决
	TicketStatusClosed     = "closed"      // 已关闭
)

// TicketPriority 工单优先级。
const (
	TicketPriorityLow    = "low"
	TicketPriorityNormal = "normal"
	TicketPriorityHigh   = "high"
	TicketPriorityUrgent = "urgent"
)

// 工单输入长度上限: title 与库列宽一致(255), 正文按 runes 计数, 不让多字节文本被按字节截断。
const (
	TicketTitleMaxLen   = 255
	TicketContentMaxLen = 10000
)

// TicketCategory 工单分类。
const (
	TicketCategoryBilling = "billing" // 计费问题
	TicketCategoryChannel = "channel" // 渠道问题
	TicketCategoryAccount = "account" // 账号问题
	TicketCategoryOther   = "other"   // 其他
)

// Ticket 工单主表。
type Ticket struct {
	ID         uint64    `json:"id" gorm:"primaryKey"`
	UserID     uint      `json:"user_id" gorm:"index;not null"`
	Title      string    `json:"title" gorm:"size:255;not null"`
	Content    string    `json:"content" gorm:"type:text;not null"`
	Status     string    `json:"status" gorm:"size:32;not null;default:open;index"`
	Priority   string    `json:"priority" gorm:"size:32;not null;default:normal"`
	Category   string    `json:"category" gorm:"size:64"`
	AssignedTo *uint     `json:"assigned_to,omitempty" gorm:"index"` // 分配给哪个管理员
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// ResolvedAt 进入 resolved 的时刻(离开 resolved 置空)。自动关闭按它计时:
	// updated_at 会被回复顶起, 用它会让"标记解决后 3 天自动关闭"被对话错误重置。
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
}

// TicketMessage 工单消息表（对话记录）。
type TicketMessage struct {
	ID          uint64    `json:"id" gorm:"primaryKey"`
	TicketID    uint64    `json:"ticket_id" gorm:"index;not null"`
	UserID      uint      `json:"user_id" gorm:"not null"`
	Content     string    `json:"content" gorm:"type:text;not null"`
	Attachments string    `json:"attachments,omitempty" gorm:"type:text"` // JSON 数组
	IsAdmin     bool      `json:"is_admin" gorm:"not null;default:false"`
	CreatedAt   time.Time `json:"created_at"`
}

// TicketCreateRequest 创建工单请求。
type TicketCreateRequest struct {
	Title    string `json:"title" binding:"required,min=1,max=255"`
	Content  string `json:"content" binding:"required,min=1,max=10000"`
	Priority string `json:"priority" binding:"omitempty,oneof=low normal high urgent"`
	Category string `json:"category" binding:"omitempty,oneof=billing channel account other"`
}

// TicketReplyRequest 回复工单请求。
type TicketReplyRequest struct {
	Content string `json:"content" binding:"required,min=1,max=10000"`
}

// TicketListRequest 工单列表查询。
type TicketListRequest struct {
	Status     string `form:"status" binding:"omitempty,oneof=open in_progress resolved closed"`
	Priority   string `form:"priority" binding:"omitempty,oneof=low normal high urgent"`
	Category   string `form:"category" binding:"omitempty,oneof=billing channel account other"`
	AssignedTo *uint  `form:"assigned_to"`
	Page       int    `form:"page" binding:"omitempty,min=1"`
	PageSize   int    `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// TicketView 工单视图（含消息数）。
type TicketView struct {
	ID           uint64     `json:"id"`
	UserID       uint       `json:"user_id"`
	Username     string     `json:"username"`
	Title        string     `json:"title"`
	Content      string     `json:"content"`
	Status       string     `json:"status"`
	Priority     string     `json:"priority"`
	Category     string     `json:"category"`
	AssignedTo   *uint      `json:"assigned_to,omitempty"`
	AssignedName string     `json:"assigned_name,omitempty"`
	MessageCount int64      `json:"message_count"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

// TicketDetailView 工单详情（含消息列表）。
type TicketDetailView struct {
	TicketView
	Messages []TicketMessageView `json:"messages"`
}

// TicketMessageView 工单消息视图。
type TicketMessageView struct {
	ID        uint64    `json:"id"`
	TicketID  uint64    `json:"ticket_id"`
	UserID    uint      `json:"user_id"`
	Username  string    `json:"username"`
	Content   string    `json:"content"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

// TicketAssignRequest 分配工单请求。
type TicketAssignRequest struct {
	AssignedTo uint `json:"assigned_to" binding:"required"`
}

// TicketStatusUpdateRequest 变更工单状态请求。
type TicketStatusUpdateRequest struct {
	Status string `json:"status" binding:"required,oneof=open in_progress resolved closed"`
}

// TicketStatusTransitions 合法状态流转表(不含自环):
//
//	open        → in_progress, closed            新建可直接关, 也可开始处理
//	in_progress → open, resolved, closed         退回待处理 / 标记已解决 / 关闭
//	resolved    → in_progress, closed            未真正解决则继续处理; 用户确认或标记解决满 3 天自动关闭(按 resolved_at 计时)
//	closed      → open                           用户(或管理员)重新打开
//
// 不在表里的组合一律拒绝, 避免出现 resolved→open 这类绕过"重新打开"语义的野路径;
// 同状态→同状态不算流转, 调用方按幂等空操作处理。
var TicketStatusTransitions = map[string][]string{
	TicketStatusOpen:       {TicketStatusInProgress, TicketStatusClosed},
	TicketStatusInProgress: {TicketStatusOpen, TicketStatusResolved, TicketStatusClosed},
	TicketStatusResolved:   {TicketStatusInProgress, TicketStatusClosed},
	TicketStatusClosed:     {TicketStatusOpen},
}

// TicketCanTransition 判断状态流转是否合法; from == to 返回 false(幂等空操作不算流转)。
func TicketCanTransition(from, to string) bool {
	if from == to {
		return false
	}
	for _, target := range TicketStatusTransitions[from] {
		if target == to {
			return true
		}
	}
	return false
}

// Normalize 分页缺省与上限: 页码从 1 起, 每页最多 100 条。
// 绑定层已挡非法值, 这里兜底 op 直调, 两处共用同一份口径。
func (r *TicketListRequest) Normalize() {
	if r.Page < 1 {
		r.Page = 1
	}
	if r.PageSize < 1 {
		r.PageSize = 20
	}
	if r.PageSize > 100 {
		r.PageSize = 100
	}
}

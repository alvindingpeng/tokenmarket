package op

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// ticketNotifyWG 追踪在途的通知投递 goroutine。测试可据此排空(避免异步通知
// 落进下一个用例的库), 也为将来的优雅关闭留一个统一的等待点。
var ticketNotifyWG sync.WaitGroup

// 工单业务哨兵错误: handler 按 errors.Is 映射规范 HTTP 状态码
// (404 未找到 / 403 越权 / 400 输入非法 / 409 状态冲突), 调用方不必解析文案。
var (
	// ErrTicketNotFound 工单不存在。
	ErrTicketNotFound = errors.New("ticket not found")
	// ErrTicketForbidden 无权操作他人工单(普通用户只能操作自己的工单)。
	ErrTicketForbidden = errors.New("permission denied")
	// ErrTicketClosed 工单已关闭, 回复前需先重新打开。
	ErrTicketClosed = errors.New("ticket is closed, reopen it before replying")
	// ErrTicketInvalidTransition 状态流转不合法(见 model.TicketStatusTransitions)。
	ErrTicketInvalidTransition = errors.New("invalid ticket status transition")
	// ErrTicketInvalidInput 输入非法(空标题/空白正文/超长等)。
	ErrTicketInvalidInput = errors.New("invalid ticket input")
	// ErrTicketAssigneeInvalid 分配目标不是在职管理员。
	ErrTicketAssigneeInvalid = errors.New("assignee must be an active admin")
	// ErrTicketConflict 并发写冲突: 读到旧状态后被别的请求改掉(close/reopen 交错), 需刷新重试。
	ErrTicketConflict = errors.New("ticket status changed concurrently, retry")
)

// TicketAutoCloseAfter resolved 工单自动关闭的保留期: 状态机里"用户确认 / 3 天自动关闭"的 3 天。
// 以 resolved_at(进入 resolved 的时刻)起算: 后续回复会顶起 updated_at, 但不重置这个窗口,
// 否则一个用户每隔几天补一句话就能把工单永久挂在 resolved 上。
const TicketAutoCloseAfter = 72 * time.Hour

// TicketCreate 创建工单。
//
// 新工单通知全部在职管理员(排除创建者本人): 工单的第一读者是管理员而不是提交者自己。
func TicketCreate(ctx context.Context, userID uint, req model.TicketCreateRequest) (model.TicketView, error) {
	title, err := ticketTitle(req.Title)
	if err != nil {
		return model.TicketView{}, err
	}
	content, err := ticketContent(req.Content)
	if err != nil {
		return model.TicketView{}, err
	}
	priority, err := ticketPriority(req.Priority)
	if err != nil {
		return model.TicketView{}, err
	}
	category, err := ticketCategory(req.Category)
	if err != nil {
		return model.TicketView{}, err
	}

	ticket := model.Ticket{
		UserID:   userID,
		Title:    title,
		Content:  content,
		Status:   model.TicketStatusOpen,
		Priority: priority,
		Category: category,
	}
	if err := db.GetDB().WithContext(ctx).Create(&ticket).Error; err != nil {
		return model.TicketView{}, fmt.Errorf("failed to create ticket: %w", err)
	}

	notifyTicketAdmins(userID, ticket.ID,
		fmt.Sprintf("新工单 #%d：%s", ticket.ID, title), content)

	return ticketToView(ctx, &ticket)
}

// TicketList 查询工单列表。isAdmin 由调用方从登录态角色得出(不可信请求参数):
// true 看全部, 否则(含 reseller 等非 admin 角色)只看自己的工单。
func TicketList(ctx context.Context, userID uint, isAdmin bool, req model.TicketListRequest) ([]model.TicketView, int64, error) {
	if err := ticketListFilters(&req); err != nil {
		return nil, 0, err
	}
	req.Normalize()

	query := db.GetDB().WithContext(ctx).Model(&model.Ticket{})
	if !isAdmin {
		// 角色以外再收一次范围: 非管理员无论传什么过滤条件都只可能看到自己的工单。
		query = query.Where("user_id = ?", userID)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.Priority != "" {
		query = query.Where("priority = ?", req.Priority)
	}
	if req.Category != "" {
		query = query.Where("category = ?", req.Category)
	}
	if req.AssignedTo != nil {
		query = query.Where("assigned_to = ?", *req.AssignedTo)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count tickets: %w", err)
	}

	var tickets []model.Ticket
	err := query.Order("created_at DESC").
		Offset((req.Page - 1) * req.PageSize).
		Limit(req.PageSize).
		Find(&tickets).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list tickets: %w", err)
	}

	views := make([]model.TicketView, len(tickets))
	for i := range tickets {
		view, err := ticketToView(ctx, &tickets[i])
		if err != nil {
			return nil, 0, err
		}
		views[i] = view
	}

	return views, total, nil
}

// TicketGet 查询工单详情(含消息列表)。
func TicketGet(ctx context.Context, userID uint, isAdmin bool, ticketID uint64) (model.TicketDetailView, error) {
	ticket, err := ticketOwned(ctx, userID, isAdmin, ticketID)
	if err != nil {
		return model.TicketDetailView{}, err
	}

	view, err := ticketToView(ctx, ticket)
	if err != nil {
		return model.TicketDetailView{}, err
	}

	var messages []model.TicketMessage
	if err := db.GetDB().WithContext(ctx).
		Where("ticket_id = ?", ticketID).
		Order("created_at ASC").
		Find(&messages).Error; err != nil {
		return model.TicketDetailView{}, fmt.Errorf("failed to get ticket messages: %w", err)
	}

	msgViews := make([]model.TicketMessageView, len(messages))
	for i, msg := range messages {
		username, _ := getUsernameByID(msg.UserID)
		msgViews[i] = model.TicketMessageView{
			ID:        msg.ID,
			TicketID:  msg.TicketID,
			UserID:    msg.UserID,
			Username:  username,
			Content:   msg.Content,
			IsAdmin:   msg.IsAdmin,
			CreatedAt: msg.CreatedAt,
		}
	}

	return model.TicketDetailView{
		TicketView: view,
		Messages:   msgViews,
	}, nil
}

// TicketReply 回复工单。返回落库后的消息视图。
//
// 消息写入与工单 updated_at 在同一个事务里: 对话记录与"最后活动时间"要么都在要么都不在,
// 否则自动关闭任务可能拿着旧的 updated_at 把刚有人说话的工单关掉。
func TicketReply(ctx context.Context, userID uint, isAdmin bool, ticketID uint64, req model.TicketReplyRequest) (model.TicketMessageView, error) {
	content, err := ticketContent(req.Content)
	if err != nil {
		return model.TicketMessageView{}, err
	}
	ticket, err := ticketOwned(ctx, userID, isAdmin, ticketID)
	if err != nil {
		return model.TicketMessageView{}, err
	}
	if ticket.Status == model.TicketStatusClosed {
		return model.TicketMessageView{}, ErrTicketClosed
	}

	message := model.TicketMessage{
		TicketID: ticketID,
		UserID:   userID,
		Content:  content,
		IsAdmin:  isAdmin,
	}
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先按"未关闭"条件顶起 updated_at 再写消息: 上面那次读之后管理员可能刚 close,
		// 没有这道守卫, 消息会落进已关闭工单。RowsAffected=0 说明状态已被改, 回滚不落消息。
		res := tx.Model(&model.Ticket{}).
			Where("id = ? AND status <> ?", ticketID, model.TicketStatusClosed).
			Update("updated_at", time.Now())
		if res.Error != nil {
			return fmt.Errorf("failed to touch ticket: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrTicketClosed
		}
		if err := tx.Create(&message).Error; err != nil {
			return fmt.Errorf("failed to create message: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.TicketMessageView{}, err
	}

	// 通知"对方"而不是自己: 管理员回复通知工单主人; 用户回复通知全体在职管理员(排除本人)。
	if isAdmin {
		notifyTicketUser(userID, ticket.UserID, ticketID,
			fmt.Sprintf("工单 #%d 有新回复", ticketID), content)
	} else {
		notifyTicketAdmins(userID, ticketID,
			fmt.Sprintf("工单 #%d 有用户回复", ticketID), content)
	}

	username, _ := getUsernameByID(userID)
	return model.TicketMessageView{
		ID:        message.ID,
		TicketID:  message.TicketID,
		UserID:    message.UserID,
		Username:  username,
		Content:   message.Content,
		IsAdmin:   message.IsAdmin,
		CreatedAt: message.CreatedAt,
	}, nil
}

// TicketClose 关闭工单(用户关自己的, 管理员关任意)。
func TicketClose(ctx context.Context, userID uint, isAdmin bool, ticketID uint64) error {
	ticket, err := ticketOwned(ctx, userID, isAdmin, ticketID)
	if err != nil {
		return err
	}
	if !model.TicketCanTransition(ticket.Status, model.TicketStatusClosed) {
		return fmt.Errorf("%w: %s -> closed", ErrTicketInvalidTransition, ticket.Status)
	}
	if err := setTicketStatus(ctx, ticket, model.TicketStatusClosed); err != nil {
		return err
	}
	notifyTicketUser(userID, ticket.UserID, ticketID,
		fmt.Sprintf("工单 #%d 已关闭", ticketID), ticket.Title)
	return nil
}

// TicketReopen 重新打开已关闭工单(用户可重新打开; 管理员同权)。
// closed → open 是唯一合法的重开路径, 落库时清空 closed_at。
func TicketReopen(ctx context.Context, userID uint, isAdmin bool, ticketID uint64) error {
	ticket, err := ticketOwned(ctx, userID, isAdmin, ticketID)
	if err != nil {
		return err
	}
	if !model.TicketCanTransition(ticket.Status, model.TicketStatusOpen) {
		return fmt.Errorf("%w: %s -> open", ErrTicketInvalidTransition, ticket.Status)
	}
	if err := setTicketStatus(ctx, ticket, model.TicketStatusOpen); err != nil {
		return err
	}
	notifyTicketUser(userID, ticket.UserID, ticketID,
		fmt.Sprintf("工单 #%d 已重新打开", ticketID), ticket.Title)
	return nil
}

// TicketAssign 分配工单(管理员)。分配目标必须是在职管理员。
func TicketAssign(ctx context.Context, actorID uint, ticketID uint64, req model.TicketAssignRequest) error {
	ticket, err := ticketLoad(ctx, ticketID)
	if err != nil {
		return err
	}
	// 分配给非管理员/离职管理员等于把工单丢进黑洞, 先挡在写库之前。
	var assignee model.User
	if err := db.GetDB().WithContext(ctx).Take(&assignee, "id = ?", req.AssignedTo).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTicketAssigneeInvalid
		}
		return fmt.Errorf("failed to load assignee: %w", err)
	}
	if assignee.Role != model.RoleAdmin || assignee.Status != model.StatusActive {
		return ErrTicketAssigneeInvalid
	}

	// 只写 assigned_to, 不动 updated_at: 分派是管理动作, 不算工单活动,
	// 否则每次分派都会把自动关闭的静默期往后推。
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Model(&model.Ticket{}).Where("id = ?", ticketID).
			Update("assigned_to", req.AssignedTo).Error
	}); err != nil {
		return fmt.Errorf("failed to assign ticket: %w", err)
	}

	notifyTicketUser(actorID, req.AssignedTo, ticketID,
		fmt.Sprintf("工单 #%d 已分配给您", ticketID), ticket.Title)
	return nil
}

// TicketUpdateStatus 变更工单状态(管理员)。同状态视为幂等空操作, 不落库也不通知。
func TicketUpdateStatus(ctx context.Context, actorID uint, ticketID uint64, req model.TicketStatusUpdateRequest) error {
	ticket, err := ticketLoad(ctx, ticketID)
	if err != nil {
		return err
	}
	if req.Status == ticket.Status {
		return nil
	}
	if !model.TicketCanTransition(ticket.Status, req.Status) {
		return fmt.Errorf("%w: %s -> %s", ErrTicketInvalidTransition, ticket.Status, req.Status)
	}
	if err := setTicketStatus(ctx, ticket, req.Status); err != nil {
		return err
	}
	notifyTicketUser(actorID, ticket.UserID, ticketID,
		fmt.Sprintf("工单 #%d 状态变更为 %s", ticketID, req.Status), ticket.Title)
	return nil
}

// TicketAutoCloseResolved 自动关闭 resolved 停留超过 olderThan 的工单, 返回实际关闭数。
//
// 计时起点是 resolved_at(进入 resolved 的时刻), 不是 updated_at: 回复会顶起 updated_at,
// 拿它计时等于让用户随便补一句话就把自动关闭窗口重置。判定与更新都在同一条带
// resolved_at 守卫的 UPDATE 里(status 仍是 resolved 且到期才动手), 与人工 close/reopen
// 并发时不会把已重开的工单又关回去。
func TicketAutoCloseResolved(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	var tickets []model.Ticket
	if err := db.GetDB().WithContext(ctx).
		Where("status = ? AND resolved_at IS NOT NULL AND resolved_at <= ?", model.TicketStatusResolved, cutoff).
		Find(&tickets).Error; err != nil {
		return 0, fmt.Errorf("failed to load resolved tickets: %w", err)
	}

	var closed int64
	for i := range tickets {
		ticket := tickets[i]
		now := time.Now()
		var affected int64
		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&model.Ticket{}).
				Where("id = ? AND status = ? AND resolved_at IS NOT NULL AND resolved_at <= ?", ticket.ID, model.TicketStatusResolved, cutoff).
				Updates(map[string]interface{}{
					"status":      model.TicketStatusClosed,
					"closed_at":   now,
					"resolved_at": nil, // 离开 resolved 一并清空, 与 setTicketStatus 同一不变式
					"updated_at":  now,
				})
			if result.Error != nil {
				return result.Error
			}
			affected = result.RowsAffected
			return nil
		})
		if err != nil {
			return closed, fmt.Errorf("failed to auto-close ticket #%d: %w", ticket.ID, err)
		}
		if affected == 0 {
			continue // 期间被人工 close/reopen 抢先, 不算本轮关闭, 也不发自动关闭通知。
		}
		closed += affected
		notifyTicketUser(0, ticket.UserID, ticket.ID,
			fmt.Sprintf("工单 #%d 已自动关闭", ticket.ID),
			fmt.Sprintf("工单「%s」标记解决后已满 %d 天, 系统自动关闭; 如需继续沟通可重新打开。", ticket.Title, int(olderThan.Hours()/24)))
	}
	return closed, nil
}

// setTicketStatus 在事务里落状态与时间戳:
// 进入 closed 记 closed_at(已有则保留), 离开 closed(重新打开)清空;
// 进入 resolved 记 resolved_at(自动关闭计时起点), 离开 resolved 清空。
func setTicketStatus(ctx context.Context, ticket *model.Ticket, newStatus string) error {
	updates := map[string]interface{}{
		"status":     newStatus,
		"updated_at": time.Now(),
	}
	if newStatus == model.TicketStatusClosed {
		if ticket.ClosedAt == nil {
			updates["closed_at"] = time.Now()
		}
	} else {
		updates["closed_at"] = nil
	}
	if newStatus == model.TicketStatusResolved {
		updates["resolved_at"] = time.Now()
	} else {
		updates["resolved_at"] = nil
	}
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 带上读到的旧状态做条件更新: 两个并发的 close/reopen 都基于同一份旧状态判断时,
		// 没有这道守卫会双双"成功", 后写的覆盖先写的, 调用方还以为自己赢了。
		res := tx.Model(&model.Ticket{}).
			Where("id = ? AND status = ?", ticket.ID, ticket.Status).
			Updates(updates)
		if res.Error != nil {
			return fmt.Errorf("failed to update ticket status: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrTicketConflict
		}
		return nil
	})
}

// ticketLoad 读工单本体; 不存在时返回 ErrTicketNotFound。
func ticketLoad(ctx context.Context, ticketID uint64) (*model.Ticket, error) {
	var ticket model.Ticket
	err := db.GetDB().WithContext(ctx).Where("id = ?", ticketID).Take(&ticket).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("failed to get ticket: %w", err)
	}
	return &ticket, nil
}

// ticketOwned 读工单并做所有权校验: 非管理员只能操作自己的工单。
// 注意 isAdmin 只能来自登录态中间件(库里读出的角色), 不采信请求体里的任何角色字段。
func ticketOwned(ctx context.Context, userID uint, isAdmin bool, ticketID uint64) (*model.Ticket, error) {
	ticket, err := ticketLoad(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !isAdmin && ticket.UserID != userID {
		return nil, ErrTicketForbidden
	}
	return ticket, nil
}

// ticketTitle 标题校验: 去空白后必填, 最长 TicketTitleMaxLen 个字符。
func ticketTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("%w: title is required", ErrTicketInvalidInput)
	}
	if utf8.RuneCountInString(title) > model.TicketTitleMaxLen {
		return "", fmt.Errorf("%w: title must be at most %d characters", ErrTicketInvalidInput, model.TicketTitleMaxLen)
	}
	return title, nil
}

// ticketContent 正文校验: 去空白后必填, 最长 TicketContentMaxLen 个字符。
func ticketContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("%w: content is required", ErrTicketInvalidInput)
	}
	if utf8.RuneCountInString(content) > model.TicketContentMaxLen {
		return "", fmt.Errorf("%w: content must be at most %d characters", ErrTicketInvalidInput, model.TicketContentMaxLen)
	}
	return content, nil
}

// ticketPriority 优先级缺省 normal, 只接受枚举值。
func ticketPriority(priority string) (string, error) {
	switch priority {
	case "":
		return model.TicketPriorityNormal, nil
	case model.TicketPriorityLow, model.TicketPriorityNormal, model.TicketPriorityHigh, model.TicketPriorityUrgent:
		return priority, nil
	default:
		return "", fmt.Errorf("%w: invalid priority %q", ErrTicketInvalidInput, priority)
	}
}

// ticketCategory 分类缺省 other, 只接受枚举值。
func ticketCategory(category string) (string, error) {
	switch category {
	case "":
		return model.TicketCategoryOther, nil
	case model.TicketCategoryBilling, model.TicketCategoryChannel, model.TicketCategoryAccount, model.TicketCategoryOther:
		return category, nil
	default:
		return "", fmt.Errorf("%w: invalid category %q", ErrTicketInvalidInput, category)
	}
}

// ticketListFilters 列表过滤值校验: 枚举字段只接受已知值, 防直调 op 时绕过绑定层。
func ticketListFilters(req *model.TicketListRequest) error {
	if req.Status != "" && !ticketStatusKnown(req.Status) {
		return fmt.Errorf("%w: invalid status %q", ErrTicketInvalidInput, req.Status)
	}
	if req.Priority != "" {
		if _, err := ticketPriority(req.Priority); err != nil {
			return err
		}
	}
	if req.Category != "" {
		if _, err := ticketCategory(req.Category); err != nil {
			return err
		}
	}
	return nil
}

// ticketStatusKnown 判断是否为已知工单状态。
func ticketStatusKnown(status string) bool {
	switch status {
	case model.TicketStatusOpen, model.TicketStatusInProgress, model.TicketStatusResolved, model.TicketStatusClosed:
		return true
	default:
		return false
	}
}

// ticketNotifyAsync 非阻塞投递一条工单通知: 独立 goroutine + 独立超时。
// 请求路径不该等通知落库; 调用方的 ctx 随请求结束而取消, 用它发通知会把
// "工单建好了但通知没发出去"变成常态, 所以用独立 context。失败只记日志 ——
// 通知是锦上添花, 不该反过来把已经建好的工单/回复判成失败。
//
// 走 NotifyTicket(内部是 NotificationCreate, 不带去重): 通知中心按 type 的
// 全局去重会把一小时窗口内不同工单的消息吞成一条, 工单通知必须逐条送达。
func ticketNotifyAsync(userID uint, ticketID uint64, title, content string) {
	if userID == 0 {
		return
	}
	ticketNotifyWG.Add(1)
	go func() {
		defer ticketNotifyWG.Done()
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := NotifyTicket(notifyCtx, userID, ticketID, title, content); err != nil {
			log.Warnf("ticket notify: user=%d ticket=%d err=%v", userID, ticketID, err)
		}
	}()
}

// notifyTicketAdmins 非阻塞通知全部在职管理员, 排除操作者本人(新工单/用户回复不通知自己)。
// 管理员名单解析也在后台做: 主流程只负责发指令, 不碰通知链路上的任何查询。
func notifyTicketAdmins(actorID uint, ticketID uint64, title, content string) {
	ticketNotifyWG.Add(1)
	go func() {
		defer ticketNotifyWG.Done()
		resolveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var admins []model.User
		if err := db.GetDB().WithContext(resolveCtx).Select("id").
			Where("role = ? AND status = ? AND id <> ?", model.RoleAdmin, model.StatusActive, actorID).
			Find(&admins).Error; err != nil {
			log.Warnf("ticket notify: load admins failed: %v", err)
			return
		}
		for _, admin := range admins {
			ticketNotifyAsync(admin.ID, ticketID, title, content)
		}
	}()
}

// notifyTicketUser 非阻塞通知工单主人; 主人就是操作者本人时不发(不通知自己)。
func notifyTicketUser(actorID, userID uint, ticketID uint64, title, content string) {
	if userID == 0 || userID == actorID {
		return
	}
	ticketNotifyAsync(userID, ticketID, title, content)
}

// ticketToView 将工单实体转换为视图(含用户名、分配人名、消息数)。
func ticketToView(ctx context.Context, ticket *model.Ticket) (model.TicketView, error) {
	username, _ := getUsernameByID(ticket.UserID)

	var assignedName string
	if ticket.AssignedTo != nil {
		assignedName, _ = getUsernameByID(*ticket.AssignedTo)
	}

	var messageCount int64
	if err := db.GetDB().WithContext(ctx).Model(&model.TicketMessage{}).
		Where("ticket_id = ?", ticket.ID).Count(&messageCount).Error; err != nil {
		return model.TicketView{}, fmt.Errorf("failed to count ticket messages: %w", err)
	}

	return model.TicketView{
		ID:           ticket.ID,
		UserID:       ticket.UserID,
		Username:     username,
		Title:        ticket.Title,
		Content:      ticket.Content,
		Status:       ticket.Status,
		Priority:     ticket.Priority,
		Category:     ticket.Category,
		AssignedTo:   ticket.AssignedTo,
		AssignedName: assignedName,
		MessageCount: messageCount,
		CreatedAt:    ticket.CreatedAt,
		UpdatedAt:    ticket.UpdatedAt,
		ClosedAt:     ticket.ClosedAt,
	}, nil
}

// getUsernameByID 辅助函数: 查询用户名。
func getUsernameByID(userID uint) (string, error) {
	var user model.User
	err := db.GetDB().Select("username").Where("id = ?", userID).Take(&user).Error
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

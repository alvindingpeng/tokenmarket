package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

func init() {
	// 审计记录属管理后台, 仅管理员可读。
	router.NewGroupRouter("/api/v1/audit").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listAuditLogs),
		).
		AddRoute(
			router.NewRoute("/actions", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listAuditActions),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(exportAuditLogs),
		)
}

// parseAuditTime 解析 RFC3339 时间参数; 缺省或非法时返回零值表示不限。
func parseAuditTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// listAuditLogs 按筛选条件分页返回审计记录, 按时间倒序; limit 上限 200。
// 支持 keyword(操作者/目标/明细模糊匹配)、action(动作标识)、operator(操作者)与 from/to(时间区间)。
// auditExportLimit 单次导出的审计记录上限, 与计费明细导出一致。
const auditExportLimit = 5000

// auditFilterFromQuery 解析审计筛选参数; 列表与导出共用, 保证两处语义完全一致。
func auditFilterFromQuery(c *gin.Context) op.AuditLogFilter {
	return op.AuditLogFilter{
		Keyword:  c.Query("keyword"),
		Action:   c.Query("action"),
		Operator: c.Query("operator"),
		From:     parseAuditTime(c.Query("from")),
		To:       parseAuditTime(c.Query("to")),
	}
}

func listAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	filter := auditFilterFromQuery(c)
	logs, total, err := op.AuditLogList(filter, limit, offset)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": logs, "total": total})
}

// listAuditActions 返回已有记录中出现过的动作标识, 供前端筛选下拉展示。
func listAuditActions(c *gin.Context) {
	actions, err := op.AuditActions()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, actions)
}

// exportAuditLogs 以 CSV 导出命中筛选条件的审计记录; 单次上限 5000, 与计费明细导出保持同一约定。
// 超出上限时在文件末尾写截断说明, 避免"看起来是全量其实只导了一页"的静默丢数据 —— 导出是留痕动作,
// 缺行必须自己说出来。导出本身也记审计, 否则"谁把审计日志带走了"会成为审计的盲区。
func exportAuditLogs(c *gin.Context) {
	filter := auditFilterFromQuery(c)
	logs, total, err := op.AuditLogList(filter, auditExportLimit, 0)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=octopus-audit-"+time.Now().Format("20060102-150405")+".csv")
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"id", "created_at", "username", "action", "target", "detail"})
	for _, item := range logs {
		_ = writer.Write([]string{
			strconv.FormatUint(uint64(item.ID), 10),
			item.CreatedAt.Format(time.RFC3339),
			item.Username,
			item.Action,
			item.Target,
			item.Detail,
		})
	}
	if int64(len(logs)) < total {
		_ = writer.Write([]string{"", "", "", "", "", fmt.Sprintf("# truncated: exported %d of %d rows", len(logs), total)})
	}
	writer.Flush()
	audit(c, "audit.export", fmt.Sprintf("n=%d/%d", len(logs), total), fmt.Sprintf("filters: %s", describeAuditFilter(filter)))
}

// describeAuditFilter 把生效中的筛选条件拼成一行可读文本, 供审计记录还原"导出时看到了什么"。
func describeAuditFilter(filter op.AuditLogFilter) string {
	var parts []string
	if filter.Keyword != "" {
		parts = append(parts, "keyword="+filter.Keyword)
	}
	if filter.Action != "" {
		parts = append(parts, "action="+filter.Action)
	}
	if filter.Operator != "" {
		parts = append(parts, "operator="+filter.Operator)
	}
	if !filter.From.IsZero() {
		parts = append(parts, "from="+filter.From.Format(time.RFC3339))
	}
	if !filter.To.IsZero() {
		parts = append(parts, "to="+filter.To.Format(time.RFC3339))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, " ")
}

// audit 记录一条操作审计; 审计写入失败只记警告, 不影响主流程。
func audit(c *gin.Context, action, target, detail string) {
	userID, _ := middleware.CurrentUser(c)
	if err := op.AuditLogAdd(c.Request.Context(), userID, action, target, detail); err != nil {
		log.Warnf("audit write failed: %v", err)
	}
}

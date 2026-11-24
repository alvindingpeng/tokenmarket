package op

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// AutoRoutingTaskSummary 汇总一个任务分类在观测窗口内的请求结果。
type AutoRoutingTaskSummary struct {
	Task          string  `json:"task"`
	Requests      int64   `json:"requests"`
	Success       int64   `json:"success"`
	Failed        int64   `json:"failed"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

// AutoRoutingModelSummary 按任务与实际落地模型汇总成功率和延迟。
type AutoRoutingModelSummary struct {
	Task          string  `json:"task"`
	Model         string  `json:"model"`
	Attempts      int64   `json:"attempts"`
	Success       int64   `json:"success"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	AvgCostUSD    float64 `json:"avg_cost_usd"`
}

// AutoRoutingReasonSummary 是排除原因、运行事件或自动路由决策的计数。
type AutoRoutingReasonSummary struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// AutoRoutingSummary 是近期调用日志的自动路由实验聚合。数据来自现有请求和尝试日志，
// 不新增高频写入的独立报表表，也不需要迁移数据库。
type AutoRoutingSummary struct {
	Days           int                        `json:"days"`
	GroupID        int                        `json:"group_id"`
	GeneratedAt    time.Time                  `json:"generated_at"`
	WindowStart    time.Time                  `json:"window_start"`
	Requests       int64                      `json:"requests"`
	TaskClassified int64                      `json:"task_classified"`
	Success        int64                      `json:"success"`
	Failed         int64                      `json:"failed"`
	SuccessRate    float64                    `json:"success_rate"`
	Tasks          []AutoRoutingTaskSummary   `json:"tasks"`
	Models         []AutoRoutingModelSummary  `json:"models"`
	Exclusions     []AutoRoutingReasonSummary `json:"exclusions"`
	Events         []AutoRoutingReasonSummary `json:"events"`
}

type autoRequestSample struct {
	RequestID   uint64
	Status      string
	DurationMs  int64
	Cost        float64
	TargetModel string
	RouteEvents string
	Model       string
	GroupID     int
	AutoTask    string
	AutoMode    string
}

func autoTaskFromDecision(reason string) string {
	switch reason {
	case "auto_general":
		return "general"
	case "auto_summary":
		return "summary"
	case "auto_coding":
		return "coding"
	case "auto_tool":
		return "tool"
	case "auto_extraction":
		return "extraction"
	default:
		return ""
	}
}

func autoTaskFromEvents(raw string) string {
	var events []struct {
		Phase  string `json:"phase"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(raw), &events) != nil {
		return ""
	}
	for _, event := range events {
		if event.Phase != "auto_shadow" && event.Phase != "auto_routing" {
			continue
		}
		task := strings.SplitN(event.Reason, ":", 2)[0]
		switch task {
		case "general", "summary", "coding", "tool", "extraction":
			return task
		default:
			return "unsupported"
		}
	}
	return ""
}

// AutoRoutingAggregate 聚合指定分组在最近 days 天内的自动路由效果，scope 与调用日志的
// 可见范围一致，普通用户只能看到自己的请求。
func AutoRoutingAggregate(scope model.Scope, groupID, days int) (AutoRoutingSummary, error) {
	if days != 1 && days != 7 && days != 30 {
		days = 7
	}
	if groupID < 0 {
		return AutoRoutingSummary{}, fmt.Errorf("invalid group id")
	}
	now := time.Now()
	summary := AutoRoutingSummary{
		Days: days, GroupID: groupID, GeneratedAt: now,
		WindowStart: now.AddDate(0, 0, -days),
		Tasks:       make([]AutoRoutingTaskSummary, 0), Models: make([]AutoRoutingModelSummary, 0),
		Exclusions: make([]AutoRoutingReasonSummary, 0), Events: make([]AutoRoutingReasonSummary, 0),
	}
	query := db.GetDB().Model(&model.RelayRequest{}).
		Where("started_at >= ?", summary.WindowStart)
	if !scope.IsAdmin() {
		query = query.Where("user_id = ?", scope.ID)
	}
	if groupID > 0 {
		query = query.Where("group_id = ?", groupID)
	}
	var requests []autoRequestSample
	// 日志留存由系统生命周期管理；上限只约束交互式看板查询，避免一次把多年历史拉进内存。
	if err := query.Select("id AS request_id, status, duration_ns / 1000000 AS duration_ms, cost, target_model, route_events, model, group_id, auto_task, auto_mode").
		Order("id DESC").Limit(5000).Scan(&requests).Error; err != nil {
		return summary, fmt.Errorf("failed to aggregate relay requests: %w", err)
	}
	if len(requests) == 0 {
		return summary, nil
	}

	ids := make([]uint64, 0, len(requests))
	requestByID := make(map[uint64]autoRequestSample, len(requests))
	for _, request := range requests {
		ids = append(ids, request.RequestID)
		requestByID[request.RequestID] = request
		summary.Requests++
		if request.Status == "success" {
			summary.Success++
		} else if request.Status == "failed" {
			summary.Failed++
		}
	}
	var attempts []model.RelayRequestAttempt
	if err := db.GetDB().Where("request_id IN ?", ids).Order("request_id ASC, round ASC").Find(&attempts).Error; err != nil {
		return summary, fmt.Errorf("failed to aggregate relay attempts: %w", err)
	}

	type taskCount struct {
		requests, success, failed int64
		duration                  int64
	}
	tasks := make(map[string]*taskCount)
	modelKey := func(task, name string) string { return task + "\x00" + name }
	type modelCount struct {
		attempts, success int64
		duration          int64
		cost              float64
	}
	models := make(map[string]*modelCount)
	exclusions := make(map[string]int64)
	events := make(map[string]int64)
	requestTasks := make(map[uint64]string)
	for _, request := range requests {
		task := request.AutoTask
		if task == "" {
			task = autoTaskFromEvents(request.RouteEvents)
		}
		if task == "" {
			continue
		}
		requestTasks[request.RequestID] = task
		tc := tasks[task]
		if tc == nil {
			tc = &taskCount{}
			tasks[task] = tc
		}
		tc.requests++
		tc.duration += request.DurationMs
		if request.Status == "success" {
			tc.success++
		} else if request.Status == "failed" {
			tc.failed++
		}
		var routeEvents []struct {
			Phase  string `json:"phase"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(request.RouteEvents), &routeEvents) == nil {
			for _, event := range routeEvents {
				if event.Phase != "auto_shadow" && event.Phase != "auto_routing" {
					continue
				}
				if event.Phase == "auto_routing" && event.Reason == "auto_circuit_open" {
					events["circuit_open"]++
				} else if strings.Contains(event.Reason, ":no_candidate") {
					events["no_candidate"]++
				} else if strings.Contains(event.Reason, ":item=") {
					events["predicted"]++
				} else {
					events[event.Reason]++
				}
			}
		}
	}
	for _, attempt := range attempts {
		var decision model.RelayRouteDecision
		if json.Unmarshal([]byte(attempt.Decision), &decision) != nil {
			continue
		}
		request := requestByID[attempt.RequestID]
		task := autoTaskFromDecision(decision.Reason)
		if task == "" {
			task = requestTasks[attempt.RequestID]
		}
		if task == "" {
			continue
		}
		name := attempt.TargetModel
		if name == "" {
			name = request.TargetModel
		}
		if name == "" {
			continue
		}
		key := modelKey(task, name)
		mc := models[key]
		if mc == nil {
			mc = &modelCount{}
			models[key] = mc
		}
		mc.attempts++
		mc.duration += attempt.DurationMs
		if attempt.Status == "success" {
			mc.success++
			// Request-level cost is booked once for the final successful route.
			// Counting it for every try of the same successful request would inflate
			// the model's average cost when a retry preceded the successful attempt.
			if request.Status == "success" && request.TargetModel == name {
				mc.cost += request.Cost
			}
		}
		for _, candidate := range decision.Candidates {
			if strings.HasPrefix(candidate.Reason, "auto_") {
				exclusions[candidate.Reason]++
			}
		}
	}
	for _, task := range tasks {
		summary.TaskClassified += task.requests
	}
	if completed := summary.Success + summary.Failed; completed > 0 {
		summary.SuccessRate = float64(summary.Success) / float64(completed)
	}
	for task, count := range tasks {
		item := AutoRoutingTaskSummary{
			Task: task, Requests: count.requests, Success: count.success, Failed: count.failed,
		}
		if count.requests > 0 {
			item.AvgDurationMs = float64(count.duration) / float64(count.requests)
		}
		if completed := count.success + count.failed; completed > 0 {
			item.SuccessRate = float64(count.success) / float64(completed)
		}
		summary.Tasks = append(summary.Tasks, item)
	}
	for key, count := range models {
		parts := strings.SplitN(key, "\x00", 2)
		item := AutoRoutingModelSummary{Task: parts[0], Model: parts[1], Attempts: count.attempts, Success: count.success}
		if count.attempts > 0 {
			item.AvgDurationMs = float64(count.duration) / float64(count.attempts)
		}
		item.SuccessRate = float64(count.success) / float64(count.attempts)
		if count.success > 0 {
			item.AvgCostUSD = count.cost / float64(count.success)
		}
		summary.Models = append(summary.Models, item)
	}
	for reason, count := range exclusions {
		summary.Exclusions = append(summary.Exclusions, AutoRoutingReasonSummary{Reason: reason, Count: count})
	}
	for reason, count := range events {
		summary.Events = append(summary.Events, AutoRoutingReasonSummary{Reason: reason, Count: count})
	}
	sort.Slice(summary.Tasks, func(i, j int) bool { return summary.Tasks[i].Requests > summary.Tasks[j].Requests })
	sort.Slice(summary.Models, func(i, j int) bool {
		if summary.Models[i].Task != summary.Models[j].Task {
			return summary.Models[i].Task < summary.Models[j].Task
		}
		return summary.Models[i].Attempts > summary.Models[j].Attempts
	})
	sort.Slice(summary.Exclusions, func(i, j int) bool { return summary.Exclusions[i].Count > summary.Exclusions[j].Count })
	sort.Slice(summary.Events, func(i, j int) bool { return summary.Events[i].Count > summary.Events[j].Count })
	return summary, nil
}

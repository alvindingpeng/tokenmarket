package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/group").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getGroupList),
		).
		AddRoute(
			router.NewRoute("/get/:id", http.MethodGet).
				Handle(getGroup),
		).
		AddRoute(
			router.NewRoute("/events", http.MethodGet).
				Handle(streamGroupEvents),
		).
		AddRoute(
			router.NewRoute("/metrics/:id", http.MethodGet).
				Handle(getGroupMetrics),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createGroup),
		).
		AddRoute(
			router.NewRoute("/update/:id", http.MethodPost).
				Handle(updateGroup),
		).
		AddRoute(
			// 只追加成员: 与 /update 的 items 整体替换是两条语义, 不可互相替代 ——
			// 整体替换要求调用方持有准确的当前集合, 广场那类"卡片数据来自轮询缓存"的入口做不到。
			router.NewRoute("/add-items", http.MethodPost).
				Handle(addGroupItems),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteGroup),
		)
}

// groupResponse 是分组读取响应: 分组配置加其当前实时路由状态。
// 路由状态由 Relay 持有且不落库, 不属于分组配置, 故在此拼接而不作为 model.Group 的字段;
// 嵌入的分组字段在 JSON 中展平, 前端看到的仍是一层对象。
type groupResponse struct {
	model.Group
	// ActiveItemID 恒为零值, 只为遮蔽嵌入分组的同名字段: 当前成员一律从 runtime.current_item_id 读,
	// 出两份会让消费方无从选择, 而故障转移模式下嵌入的那份只是写入侧的陈旧值。
	// model.Group 上的 tag 供数据库转储使用不能删, 遮蔽也不能用 json:"-" ——
	// 带该 tag 的字段被 encoding/json 直接跳过, 不进候选集也就不参与同名冲突消解, 嵌入的那份仍会输出;
	// 同名加 omitempty 才既胜出又因零值被省略。
	ActiveItemID int              `json:"active_item_id,omitempty"`
	Runtime      relay.RouteState `json:"runtime"` // 分组当前的实时路由状态。
}

// 分组变更事件, SSE 收到后按事件名原样转发。
type groupEvent struct {
	Name    string // SSE 事件名: changed 表示分组配置或成员发生变更, deleted 表示分组已删除。
	Data    any    // changed 携带完整的 groupResponse, deleted 携带分组 ID。
	ownerID uint   // 分组归属用户, 仅用于按访问者过滤事件流, 不出 JSON。
}

const groupEventBuffer = 16 // 单个分组事件流连接的非阻塞消息缓冲容量。

// groupEventStream 一条事件流连接及其访问者作用域: 事件按归属过滤后才推送。
type groupEventStream struct {
	ch    chan groupEvent
	scope model.Scope
}

var (
	groupEventMu      sync.Mutex                                   // groupEventMu 保护全部分组事件流连接。
	groupEventStreams = make(map[chan groupEvent]groupEventStream) // 全部分组事件流 SSE 连接及其访问者作用域。
)

// publishGroupEvent 非阻塞发布一条分组变更事件, 连接拥塞时关闭它并交给客户端重连后重新拉取对齐。
// 发布点在此而非 op: 事件携带的运行状态取自 Relay, 而 Relay 依赖 op, 放进 op 会形成循环依赖。
func publishGroupEvent(event groupEvent) {
	groupEventMu.Lock()
	defer groupEventMu.Unlock()

	for stream, meta := range groupEventStreams {
		// 归属过滤: 管理员收全部, 其余只收自有分组的事件。
		if !meta.scope.Owns(event.ownerID) {
			continue
		}
		select {
		case stream <- event:
		default:
			delete(groupEventStreams, stream)
			close(stream)
		}
	}
}

// streamGroupEvents 向前端发送分组的变更事件与实时运行状态。
// 一条连接承载两个来源: 分组的增删改由本包发布, 故障转移模式的运行状态增量由 Relay 的路由自身发布。
// 不发初始快照: 分组读取接口已随分组带回当前状态, 前端由此拿到的初始值即全量。
func streamGroupEvents(c *gin.Context) {
	prepareSSE(c)
	routeUpdates := relay.OpenRouteStream()
	defer relay.CloseRouteStream(routeUpdates)

	userID, role := middleware.CurrentUser(c)
	scope := model.Scope{ID: userID, Role: role}
	events := make(chan groupEvent, groupEventBuffer)
	groupEventMu.Lock()
	groupEventStreams[events] = groupEventStream{ch: events, scope: scope}
	groupEventMu.Unlock()
	defer func() {
		groupEventMu.Lock()
		defer groupEventMu.Unlock()
		// 通道可能已被拥塞时的发布方关闭, 重复关闭会 panic。
		if _, exists := groupEventStreams[events]; exists {
			delete(groupEventStreams, events)
			close(events)
		}
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case update, ok := <-routeUpdates:
			if !ok {
				return
			}
			// 运行状态按分组归属过滤: 非管理员只收自有分组。
			if ownerID, exists := op.GroupOwnerID(update.GroupID); !exists || !scope.Owns(ownerID) {
				continue
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: "runtime", Data: update}); err != nil {
				return
			}
			c.Writer.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: event.Name, Data: event.Data}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// getGroupList 返回全部分组, 并为每个分组补齐当前的实时路由状态。
// 路由状态由 Relay 持有而 Relay 依赖 op, 故补齐点放在此处而非 op.GroupList 内。
func getGroupList(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	groups := op.GroupList(model.Scope{ID: userID, Role: role})
	responses := make([]groupResponse, len(groups))
	for i, group := range groups {
		responses[i] = groupResponse{Group: group, Runtime: relay.RouteStateOf(group)}
	}
	resp.Success(c, responses)
}

// getGroup 返回单个分组及其当前实时路由状态。
// 日志详情只关心承载该请求的那一个分组, 由此无需拉取整份分组列表。
func getGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, role := middleware.CurrentUser(c)
	group, err := op.GroupGet(id, model.Scope{ID: userID, Role: role})
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, groupResponse{Group: group, Runtime: relay.RouteStateOf(group)})
}

// getGroupMetrics 返回分组各成员的实时选路指标与综合评分。
// 指标由 Relay 在每次转发后更新, 进程内存储; 综合评分按分组当前生效的评分配比(分组自定义 >
// 系统设置 > 内置兜底)算出, 与实际选路同一套归一化与可靠性软惩罚, 只是把全部成员的分数都给出。
// 全部成员都会返回(冷成员 samples 为 0), 让每个渠道模型都能显示评分, 而不只是已有观测的那些。
func getGroupMetrics(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, role := middleware.CurrentUser(c)
	group, err := op.GroupGet(id, model.Scope{ID: userID, Role: role})
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"basis": relay.MemberScoreBasis(group),
		"items": relay.MemberScoreViews(group),
	})
}

func createGroup(c *gin.Context) {
	var req model.GroupCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, role := middleware.CurrentUser(c)
	group, err := op.GroupCreate(&req, model.Scope{ID: userID, Role: role}, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response := groupResponse{Group: *group, Runtime: relay.RouteStateOf(*group)}
	publishGroupEvent(groupEvent{Name: "changed", Data: response, ownerID: group.UserID})
	resp.Success(c, response)
}

// updateGroup 更新分组配置, 成员和手动模式的当前成员。
// 变更后的完整分组一并推入事件流: 其他会话由此同步到新的成员与配置, 无需各自重新拉取列表;
// 手动模式的当前成员也随之出去, 它由分组配置定稿, Relay 自身不会为它发运行状态增量。
func updateGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	var req model.GroupUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, role := middleware.CurrentUser(c)
	scope := model.Scope{ID: userID, Role: role}
	oldGroup, err := op.GroupGet(id, scope)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	group, err := op.GroupUpdate(id, &req, scope, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 选择模式变化后进程内路由不再适用, 丢弃它以免旧的冷却与亲和在切回故障转移时复活。
	if oldGroup.Mode != group.Mode {
		relay.ResetRouteState(id)
	}
	response := groupResponse{Group: *group, Runtime: relay.RouteStateOf(*group)}
	publishGroupEvent(groupEvent{Name: "changed", Data: response, ownerID: group.UserID})
	resp.Success(c, response)
}

// addGroupItems 把一个或多个渠道授权追加到若干分组末尾, 不整体替换成员集合。
// 幂等: 已存在的授权跳过而不是报错; 既有成员的顺序与优先级一律不动。
// 结果逐个分组返回, 让界面能显示"每个配置各加了几条"。
func addGroupItems(c *gin.Context) {
	var req model.GroupAddItemsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, role := middleware.CurrentUser(c)
	scope := model.Scope{ID: userID, Role: role}
	results, err := op.GroupAddItems(&req, scope, c.Request.Context())
	if err != nil {
		// 部分成功时 results 非空: 已完成的那些已经落库, 必须让调用方看见,
		// 否则界面会报"全部失败"而库里其实已经加进去几个分组。
		if len(results) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"code":    http.StatusConflict,
				"message": err.Error(),
				"data":    results,
			})
			return
		}
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		resp.Error(c, status, err.Error())
		return
	}
	// 改的是成员集合, 每个分组都要推一次事件: 广场、分组页与日志详情共用同一条事件流。
	for _, result := range results {
		response := groupResponse{Group: result.Group, Runtime: relay.RouteStateOf(result.Group)}
		publishGroupEvent(groupEvent{Name: "changed", Data: response, ownerID: result.Group.UserID})
	}
	resp.Success(c, results)
}

func deleteGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, role := middleware.CurrentUser(c)
	// 先取归属: 删除事件要按归属过滤推送, 且非归属访问按 404 处理。
	group, err := op.GroupGet(id, model.Scope{ID: userID, Role: role})
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	if err := op.GroupDel(id, model.Scope{ID: userID, Role: role}, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	relay.ResetRouteState(id)
	publishGroupEvent(groupEvent{Name: "deleted", Data: id, ownerID: group.UserID})
	resp.Success(c, "group deleted successfully")
}

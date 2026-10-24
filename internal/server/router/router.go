package router

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// GroupRouter represents a group of routes with shared path prefix and middlewares
type GroupRouter struct {
	Path        string
	Routes      []*Route
	Middlewares []gin.HandlerFunc
}

// Global registry for route groups
var registeredRouters []*GroupRouter

// NewGroupRouter creates a new GroupRouter with the given path and automatically registers it.
func NewGroupRouter(path string) *GroupRouter {
	router := &GroupRouter{
		Path:   path,
		Routes: make([]*Route, 0),
	}
	registeredRouters = append(registeredRouters, router)
	return router
}

// Use adds middlewares to the group.
func (g *GroupRouter) Use(middlewares ...gin.HandlerFunc) *GroupRouter {
	g.Middlewares = append(g.Middlewares, middlewares...)
	return g
}

// AddRoute adds a route to the group.
func (g *GroupRouter) AddRoute(route *Route) *GroupRouter {
	g.Routes = append(g.Routes, route)
	return g
}

// Route defines a single endpoint with its handlers and middlewares.
type Route struct {
	Path        string
	Method      string
	Handlers    []gin.HandlerFunc
	Middlewares []gin.HandlerFunc
	Roles       []model.Role // 允许访问的角色白名单; 为空表示只要登录态即可, RegisterAll 自动挂 RequireRole。
}

// Allow 声明本路由允许的角色白名单。
func (r *Route) Allow(roles ...model.Role) *Route {
	r.Roles = append(r.Roles, roles...)
	return r
}

// NewRoute creates a new Route instance with the given path and method.
func NewRoute(path string, method string) *Route {
	return &Route{
		Path:     path,
		Method:   method,
		Handlers: make([]gin.HandlerFunc, 0),
	}
}

// Handle adds handler functions to the route.
func (r *Route) Handle(handlers ...gin.HandlerFunc) *Route {
	r.Handlers = append(r.Handlers, handlers...)
	return r
}

// Use adds middlewares to the route.
func (r *Route) Use(middlewares ...gin.HandlerFunc) *Route {
	r.Middlewares = append(r.Middlewares, middlewares...)
	return r
}

// Validate checks if the route is valid
func (r *Route) Validate() error {
	if len(r.Handlers) == 0 {
		return fmt.Errorf("route must have at least one handler")
	}
	return nil
}

// GetRouterCount returns the total count of registered routes
func GetRouterCount() int {
	count := 0
	for _, router := range registeredRouters {
		count += len(router.Routes)
	}
	return count
}

// RegisterAll registers all globally registered route groups to the Gin engine
func RegisterAll(engine *gin.Engine) error {
	for _, router := range registeredRouters {
		// Validate all routes in the group first
		for _, route := range router.Routes {
			if err := route.Validate(); err != nil {
				return fmt.Errorf("invalid route in group %s: %w", router.Path, err)
			}
		}

		// Create the route group
		group := engine.Group(router.Path, router.Middlewares...)

		// Register all routes in the group
		for _, route := range router.Routes {
			handlers := make([]gin.HandlerFunc, 0, len(route.Middlewares)+len(route.Handlers)+1)
			// 声明式 RBAC: 带角色白名单的路由统一在此挂角色中间件, 免得各处手写一遍。
			// 角色闸门排在路由自定义中间件之前: 403 必须先于 step-up 的 428 落地。
			// 无权限者既不该被弹密码框(那是给有权者的二次确认), 428 也不该比 403 更早地把
			// 「存在这么个高危端点」透露给无权限者。两个闸门都排在业务 handler 之前,
			// 任何一边拒绝都不会产生业务副作用。
			if len(route.Roles) > 0 {
				handlers = append(handlers, middleware.RequireRole(route.Roles...))
			}
			handlers = append(handlers, route.Middlewares...)
			handlers = append(handlers, route.Handlers...)

			registerRoute(group, route.Method, route.Path, handlers)
		}
	}
	// 注册表不再清空: 它就是路由表本体。保留既便于启动后核对(GetRouterCount),
	// 也让测试能从同一张表各自构建引擎来验证「真实注册」而不是手抄一份路由。
	// 同一个 engine 只能 RegisterAll 一次: 重复注册同路径会触发 gin 的 panic。
	return nil
}

// registerRoute registers a single route to a Gin route group.
func registerRoute(group *gin.RouterGroup, method string, path string, handlers []gin.HandlerFunc) {
	if len(handlers) == 0 {
		return
	}

	if path != "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}

	switch method {
	case http.MethodGet:
		group.GET(path, handlers...)
	case http.MethodPost:
		group.POST(path, handlers...)
	case http.MethodPut:
		group.PUT(path, handlers...)
	case http.MethodDelete:
		group.DELETE(path, handlers...)
	case http.MethodHead:
		group.HEAD(path, handlers...)
	case http.MethodOptions:
		group.OPTIONS(path, handlers...)
	case http.MethodPatch:
		group.PATCH(path, handlers...)
	default:
		group.GET(path, handlers...)
	}
}

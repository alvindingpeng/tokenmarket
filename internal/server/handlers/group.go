package handlers

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
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
			router.NewRoute("/create", http.MethodPost).
				Handle(createGroup),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateGroup),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteGroup),
		)
	// AddRoute(
	// 	router.NewRoute("/auto-add-item", http.MethodPost).
	// 		Handle(autoAddGroupItem),
	// )
}

func getGroupList(c *gin.Context) {
	groups, err := op.GroupList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, groups)
}

func createGroup(c *gin.Context) {
	var group model.Group
	if err := c.ShouldBindJSON(&group); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if group.MatchRegex != "" {
		_, err := regexp.Compile(group.MatchRegex)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := op.GroupCreate(&group, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, group)
}

func updateGroup(c *gin.Context) {
	var req model.GroupUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.MatchRegex != nil {
		_, err := regexp.Compile(*req.MatchRegex)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	group, err := op.GroupUpdate(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, group)
}

func deleteGroup(c *gin.Context) {
	id := c.Param("id")
	idNum, err := strconv.Atoi(id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.GroupDel(idNum, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "group deleted successfully")
}

// func autoAddGroupItem(c *gin.Context) {
// 	var req struct {
// 		ID int `json:"id"`
// 	}
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		resp.Error(c, http.StatusBadRequest, err.Error())
// 		return
// 	}
// 	if req.ID <= 0 {
// 		resp.Error(c, http.StatusBadRequest, "invalid id")
// 		return
// 	}
// 	err := worker.AutoAddGroupItem(req.ID, c.Request.Context())
// 	if err != nil {
// 		resp.Error(c, http.StatusInternalServerError, err.Error())
// 		return
// 	}
// 	resp.Success(c, nil)
// }

package handlers

import (
	"net/http"
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
}

func getGroupList(c *gin.Context) {
	groups, err := op.GroupList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	// Transform groups to GroupResponse format
	groupMap := make(map[string]*model.GroupResponse)
	for _, group := range groups {
		if _, exists := groupMap[group.Name]; !exists {
			groupMap[group.Name] = &model.GroupResponse{
				Name:  group.Name,
				Items: []model.GroupItem{},
			}
		}
		groupMap[group.Name].Items = append(groupMap[group.Name].Items, model.GroupItem{
			ID:        group.ID,
			ChannelID: group.ChannelID,
			ModelName: group.ModelName,
			Priority:  group.Priority,
		})
	}

	// Convert map to slice
	var groupResponses []model.GroupResponse
	for _, groupResponse := range groupMap {
		groupResponses = append(groupResponses, *groupResponse)
	}

	resp.Success(c, groupResponses)
}

func createGroup(c *gin.Context) {
	var group model.Group
	if err := c.ShouldBindJSON(&group); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.GroupCreate(&group, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, group)
}

func updateGroup(c *gin.Context) {
	var group model.Group
	if err := c.ShouldBindJSON(&group); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.GroupUpdate(&group, c.Request.Context()); err != nil {
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

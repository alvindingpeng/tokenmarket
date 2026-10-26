package handlers

import (
	"net/http"
	"time"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// channelEgressHealth is the safe public shape of an egress health row.
// Proxy URLs and credentials are intentionally excluded.
type channelEgressHealth struct {
	ID            int               `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Region        string            `json:"region"`
	Weight        int               `json:"weight"`
	Enabled       bool              `json:"enabled"`
	Samples             int               `json:"samples"`
	HasData             bool              `json:"has_data"`
	SuccessRate         float64           `json:"success_rate"`
	AvgWaitMs           float64           `json:"avg_wait_ms"`
	CooldownUntil       *time.Time        `json:"cooldown_until,omitempty"`
	CooldownRemainingMs int64             `json:"cooldown_remaining_ms"`
	Cooling             bool              `json:"cooling"`
	UpdatedAt           *time.Time        `json:"updated_at,omitempty"`
}

func init() {
	router.NewGroupRouter("/api/v1/channel/egress-health").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/:id", http.MethodGet).
			Allow(model.RoleAdmin, model.RoleReseller).
			Handle(getChannelEgressHealth))
}

func getChannelEgressHealth(c *gin.Context) {
	channelID, ok := parseChannelIDParam(c)
	if !ok {
		return
	}
	detail, err := op.ChannelDetailGet(channelID, scopeOf(c))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	views := relay.EgressRuntimeViews(detail.Egresses)
	viewsByID := make(map[int]relay.EgressRuntimeView, len(views))
	for _, view := range views {
		viewsByID[view.EgressID] = view
	}
	rows := make([]channelEgressHealth, 0, len(detail.Egresses))
	for _, egress := range detail.Egresses {
		view := viewsByID[egress.ID]
		row := channelEgressHealth{
			ID: egress.ID, Name: egress.Name, Kind: string(egress.Kind), Region: egress.Region,
			Weight: egress.Weight, Enabled: egress.Enabled, Samples: view.Samples, HasData: view.HasData,
			SuccessRate: view.SuccessRate, AvgWaitMs: view.AvgWaitMs, CooldownRemainingMs: view.CooldownRemainingMs, Cooling: view.Cooling,
		}
		if view.CooldownUntil > 0 {
			value := time.UnixMilli(view.CooldownUntil).UTC()
			row.CooldownUntil = &value
		}
		if view.UpdatedAt > 0 {
			value := time.UnixMilli(view.UpdatedAt).UTC()
			row.UpdatedAt = &value
		}
		rows = append(rows, row)
	}
	resp.Success(c, gin.H{"channel_id": channelID, "egresses": rows})
}

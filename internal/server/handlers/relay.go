package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/router"
)

func init() {
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/chat/completions", http.MethodPost).
			Handle(relay.HandleChatCompletions),
		).
		AddRoute(
			router.NewRoute("/responses", http.MethodPost).
			Handle(relay.HandleResponses),
		).
		AddRoute(
			router.NewRoute("/messages", http.MethodPost).
			Handle(relay.HandleMessages),
		)
}

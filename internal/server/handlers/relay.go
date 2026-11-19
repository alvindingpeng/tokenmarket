package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/looplj/axonhub/llm"
)

func init() {
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/chat/completions", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIChatCompletion)),
		).
		AddRoute(
			router.NewRoute("/responses", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIResponse)),
		).
		AddRoute(
			router.NewRoute("/messages", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatAnthropicMessage)),
		).
		AddRoute(
			router.NewRoute("/images/generations", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIImageGeneration)),
		).
		AddRoute(
			router.NewRoute("/images/edits", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIImageEdit)),
		).
		AddRoute(
			router.NewRoute("/videos", http.MethodPost).
				// 生视频创建先过时长闸门: 声明了非法 seconds 的请求在派发前以 400 拒绝,
				// 既不冻结余额也不发往上游 —— 计费时长与转发时长始终同一套口径。
				Handle(relay.VideoCreateDurationGuard(), relay.Forward(llm.APIFormatOpenAIVideo)),
		).
		AddRoute(
			router.NewRoute("/videos/:id", http.MethodGet).
				Handle(relay.ForwardVideoTask(false)),
		).
		AddRoute(
			router.NewRoute("/videos/:id", http.MethodDelete).
				Handle(relay.ForwardVideoTask(true)),
		)
}

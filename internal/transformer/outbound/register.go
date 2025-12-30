package outbound

import (
	"github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound/authropic"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound/gemini"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound/openai"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound/volcengine"
)

type OutboundType int

const (
	OutboundTypeOpenAIChat OutboundType = iota
	OutboundTypeOpenAIResponse
	OutboundTypeAnthropic
	OutboundTypeGemini
	OutboundTypeVolcengine
)

var outboundFactories = map[OutboundType]func() model.Outbound{
	OutboundTypeOpenAIChat:     func() model.Outbound { return &openai.ChatOutbound{} },
	OutboundTypeOpenAIResponse: func() model.Outbound { return &openai.ResponseOutbound{} },
	OutboundTypeAnthropic:      func() model.Outbound { return &authropic.MessageOutbound{} },
	OutboundTypeGemini:         func() model.Outbound { return &gemini.MessagesOutbound{} },
	OutboundTypeVolcengine:     func() model.Outbound { return &volcengine.ResponseOutbound{} },
}

func Get(outboundType OutboundType) model.Outbound {
	if factory, ok := outboundFactories[outboundType]; ok {
		return factory()
	}
	return nil
}

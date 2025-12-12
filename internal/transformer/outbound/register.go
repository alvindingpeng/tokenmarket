package outbound

import (
	"github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound/openai"
)

type OutboundType int

const (
	OutboundTypeOpenAIChat OutboundType = iota
	OutboundTypeOpenAIResponse
	OutboundTypeAnthropic
	OutboundTypeOneAPI
)

var outboundFactories = map[OutboundType]func() model.Outbound{
	OutboundTypeOpenAIChat: func() model.Outbound { return &openai.ChatOutbound{} },
}

func Get(outboundType OutboundType) model.Outbound {
	if factory, ok := outboundFactories[outboundType]; ok {
		return factory()
	}
	return nil
}

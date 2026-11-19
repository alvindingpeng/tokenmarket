package op

import (
	"github.com/alvindingpeng/switchyard/internal/model"
	"strings"
	"testing"
)

// Exercise the same create/update transaction that saves fetched model grants.
func TestChannelSaveGeminiCLIProtocols(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	detail := reviewTestDetail("subscription-protocol-save")
	detail.Dialect = model.DialectSubscription
	detail.SubPlatform = "gemini"
	detail.Grants[0].Protocols = model.ProtocolGeminiCLI
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create Gemini CLI grant: %v", err)
	}
	mixed := model.ProtocolGeminiCLI | model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage
	created.Grants[0].Protocols = mixed
	saved, err := ChannelUpdate(created, admin, ctx)
	if err != nil {
		t.Fatalf("save protocols 142: %v", err)
	}
	if len(saved.Grants) != 1 || saved.Grants[0].Protocols != mixed {
		t.Fatalf("grant protocols not persisted: %+v", saved.Grants)
	}
	for _, invalid := range []model.Protocol{0, mixed | model.Protocol(1)} {
		saved.Grants[0].Protocols = invalid
		if _, err := ChannelUpdate(saved, admin, ctx); err == nil || !strings.Contains(err.Error(), "undefined bits") {
			t.Fatalf("invalid protocols %d should fail, got %v", invalid, err)
		}
	}
}

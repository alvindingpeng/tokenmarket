package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func ssrfTestEnv(t *testing.T) context.Context {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "ssrf.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	return context.Background()
}

func TestChannelCreateRejectsPrivateUpstream(t *testing.T) {
	ctx := ssrfTestEnv(t)
	for _, baseURL := range []string{
		"http://127.0.0.1:8080",
		"http://169.254.169.254",
		"http://192.168.1.10:11434",
		"http://[::1]:9000",
	} {
		detail := &model.ChannelDetail{
			ChannelConfig: model.ChannelConfig{Name: "bad", BaseURL: baseURL},
		}
		if _, err := ChannelCreate(detail, model.Scope{ID: 1, Role: model.RoleAdmin}, ctx); err == nil {
			t.Fatalf("private upstream %s should be rejected", baseURL)
		}
	}
}

func TestChannelCreateAllowsPrivateWhenSettingEnabled(t *testing.T) {
	ctx := ssrfTestEnv(t)
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("enable allow-private: %v", err)
	}
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "local-vllm", BaseURL: "http://192.168.1.10:11434"},
	}
	if _, err := ChannelCreate(detail, model.Scope{ID: 1, Role: model.RoleAdmin}, ctx); err != nil {
		t.Fatalf("allow-private upstream should be accepted: %v", err)
	}
}

func TestEgressRejectsPrivateProxyURL(t *testing.T) {
	ctx := ssrfTestEnv(t)
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "ok", BaseURL: "https://api.openai.com"},
		Egresses: []model.ChannelEgress{
			{Name: "inner", Kind: model.EgressKindHTTP, URL: "http://10.0.0.1:3128"},
		},
	}
	if _, err := ChannelCreate(detail, model.Scope{ID: 1, Role: model.RoleAdmin}, ctx); err == nil {
		t.Fatal("private egress proxy should be rejected")
	}
}

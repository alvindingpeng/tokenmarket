package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

func probeFixture(t *testing.T, url string, protocol model.Protocol, kind model.MediaKind) (model.Channel, model.ChannelKey, model.ChannelModel) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe.db"), false); err != nil {
		t.Fatal(err)
	}
	if err := op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatal(err)
	}
	ch := model.Channel{ChannelConfig: model.ChannelConfig{Name: "probe", BaseURL: url, Enabled: true, OpenAIChatCompletionPath: "/v1/chat/completions"}}
	if err := db.GetDB().Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	key := model.ChannelKey{ChannelID: ch.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "probe", Key: "sk-probe", Enabled: true}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	cm := model.ChannelModel{ChannelID: ch.ID, Name: "text-model", Kind: kind}
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatal(err)
	}
	grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: cm.ID, Protocols: protocol}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	return ch, key, cm
}

func TestBreakerProbeRealTextRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-probe" {
			t.Errorf("wrong request: %s %s auth=%s", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "text-model" || body["stream"] == true || body["max_tokens"] != float64(1) || body["tools"] != nil {
			t.Errorf("unsafe body: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"probe","object":"chat.completion","model":"text-model","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer srv.Close()
	ch, key, cm := probeFixture(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText)
	if err := ProbeBreakerTarget(context.Background(), ch.ID, key.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := ProbeBreakerTarget(context.Background(), ch.ID, 0, cm.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("no real requests")
	}
}

func TestBreakerProbeRejectsFalseSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"auth", 401, `{"error":{"message":"bad auth"}}`}, {"quota", 429, `{"error":{"message":"quota"}}`}, {"server", 503, `{}`}, {"malformed", 200, "hello"}, {"empty", 200, `{"choices":[]}`}, {"error200", 200, `{"error":{"message":"failed"}}`}, {"redirect", 302, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "http://127.0.0.1:1")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			ch, key, _ := probeFixture(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText)
			if err := ProbeBreakerTarget(context.Background(), ch.ID, key.ID, 0); err == nil {
				t.Fatal("accepted false success")
			}
		})
	}
}

func TestBreakerProbeNeverDispatchesUnsafeTargets(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol model.Protocol
		kind     model.MediaKind
		override bool
		ssrf     bool
	}{{"image", model.ProtocolOpenAIImage, model.MediaKindImage, false, false}, {"video", model.ProtocolOpenAIVideo, model.MediaKindVideo, false, false}, {"media-with-chat", model.ProtocolOpenAIChatCompletion, model.MediaKindImage, false, false}, {"override", model.ProtocolOpenAIChatCompletion, model.MediaKindText, true, false}, {"ssrf", model.ProtocolOpenAIChatCompletion, model.MediaKindText, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer srv.Close()
			ch, key, _ := probeFixture(t, srv.URL, tc.protocol, tc.kind)
			if tc.override {
				db.GetDB().Model(&ch).Update("param_override", `{"tools":[{}]}`)
			}
			if tc.ssrf {
				op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "false")
			}
			if err := ProbeBreakerTarget(context.Background(), ch.ID, key.ID, 0); err == nil {
				t.Fatal("unsafe target accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("unsafe request dispatched")
			}
		})
	}
}
func TestBreakerProbeUsesConfiguredProxy(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Host != "upstream.example" {
			t.Errorf("wrong proxy target %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"probe","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
	}))
	defer proxy.Close()
	ch, key, _ := probeFixture(t, "http://upstream.example", model.ProtocolOpenAIChatCompletion, model.MediaKindText)
	if err := db.GetDB().Model(&ch).Updates(map[string]interface{}{"proxy": true, "channel_proxy": proxy.URL}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ProbeBreakerTarget(context.Background(), ch.ID, key.ID, 0); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatal("configured proxy bypassed")
	}
}

func TestBreakerProbeAnthropicText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "sk-probe" {
			t.Errorf("wrong anthropic request: %s", r.URL)
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["max_tokens"] != float64(1) || body["stream"] == true {
			t.Errorf("unsafe payload %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"probe","type":"message","role":"assistant","model":"text-model","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`))
	}))
	defer srv.Close()
	ch, key, _ := probeFixture(t, srv.URL, model.ProtocolAnthropicMessage, model.MediaKindText)
	if err := ProbeBreakerTarget(context.Background(), ch.ID, key.ID, 0); err != nil {
		t.Fatal(err)
	}
}

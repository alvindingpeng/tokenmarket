package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

func TestFetchModelWithStoredAndDraftCredentials(t *testing.T) {
	handlerTestResetCaches(t)
	t.Cleanup(func() { handlerTestResetCaches(t) })
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channels.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatal(err)
	}

	const savedKey = "test-stored-second-credential"
	const draftKey = "test-draft-credential"
	var requests atomic.Int32
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		key := r.Header.Get("X-Api-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if fail.Load() || (key != savedKey && key != draftKey) {
			http.Error(w, key, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"available-model"}]}`))
	}))
	defer upstream.Close()

	channel, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "probe", BaseURL: upstream.URL, Enabled: true},
		Keys: []model.ChannelKeyConfig{
			{Name: "first", Key: "test-stored-first-credential", Enabled: true},
			{Name: "second", Key: savedKey, Enabled: true},
		},
	}, model.Scope{ID: 41, Role: model.RoleReseller}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if channel.Keys[1].Key == savedKey || !op.IsMaskedChannelSecret(channel.Keys[1].Key) {
		t.Fatal("channel detail should only expose the masked credential")
	}

	probe := func(userID int, input model.ChannelFetchModelRequest) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/channel/fetch-model", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("user_id", userID)
		c.Set("role", string(model.RoleReseller))
		fetchModel(c)
		return w
	}
	stored := model.ChannelFetchModelRequest{
		ChannelID: channel.ID, KeyName: "second",
		Channel: model.ChannelConfig{BaseURL: "http://127.0.0.1:1"},
	}
	if w := probe(41, stored); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "available-model") {
		t.Fatalf("stored credential refresh: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := probe(41, model.ChannelFetchModelRequest{Channel: channel.ChannelConfig, Key: draftKey}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "available-model") {
		t.Fatalf("draft credential refresh: status=%d body=%s", w.Code, w.Body.String())
	}
	before := requests.Load()
	if w := probe(42, stored); w.Code != http.StatusNotFound {
		t.Fatalf("another owner's channel returned %d", w.Code)
	}
	missing := stored
	missing.KeyName = "missing"
	if w := probe(41, missing); w.Code != http.StatusNotFound {
		t.Fatalf("missing credential returned %d", w.Code)
	}
	if w := probe(41, model.ChannelFetchModelRequest{Channel: channel.ChannelConfig, Key: channel.Keys[1].Key}); w.Code != http.StatusBadRequest {
		t.Fatalf("masked draft credential returned %d", w.Code)
	}
	if requests.Load() != before {
		t.Fatal("invalid requests should not reach the upstream")
	}
	fail.Store(true)
	if w := probe(41, stored); w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), savedKey) {
		t.Fatalf("stored credential failure should return a redacted 502, got %d", w.Code)
	}
}

package relay

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

const gzipOKBody = `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`

func forwardGzipRequest(t *testing.T, userID uint, body string, extra http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/relay-test", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
	for k, v := range extra {
		req.Header[k] = v
	}
	c.Request = req
	c.Set("api_key_user_id", int(userID))
	c.Set("api_key_id", 0)
	Forward(llm.APIFormatOpenAIChatCompletion)(c)
	return w
}

// A compatibility error after a delivered first event must never splice another channel's stream.
func TestGzipCompatibilityAfterCommitDoesNotFailover(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"cmpl-gzip\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"unsupported request content encoding gzip\"}}\n\n")
	}))
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))
	body := fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	w := forwardGzipRequest(t, env.userID, body, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "cmpl-gzip") {
		t.Fatalf("first event not delivered: %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("committed stream redispatched: %d", calls.Load())
	}
}

// Verify actual upstream bytes, not just successful metadata parsing or a mutated inbound header.
func TestGzipInboundIsDecodedAndHeaderStripped(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("noncanonical_headers_%t", mixed), func(t *testing.T) {
			type observation struct {
				body     []byte
				encoding string
				length   int64
				err      error
			}
			observed := make(chan observation, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				observed <- observation{body, r.Header.Get("Content-Encoding"), r.ContentLength, err}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, gzipOKBody)
			}))
			defer srv.Close()
			env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
				model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
			var extra http.Header
			if mixed {
				extra = http.Header{"cOnTeNt-EnCoDiNg": {"gzip"}, "content-length": {"999999"}}
			}
			w := forwardGzipRequest(t, env.userID, body, extra)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			select {
			case got := <-observed:
				if got.err != nil {
					t.Fatal(got.err)
				}
				if got.encoding != "" {
					t.Fatalf("upstream encoding = %q", got.encoding)
				}
				if got.length != int64(len(got.body)) {
					t.Fatalf("upstream length %d != plaintext length %d", got.length, len(got.body))
				}
				var request struct {
					Model    string
					Messages []struct{ Content string }
				}
				if err := json.Unmarshal(got.body, &request); err != nil {
					t.Fatalf("not plaintext JSON: %q: %v", got.body, err)
				}
				if len(request.Messages) != 1 || request.Messages[0].Content != "hi" || request.Model == "" || request.Model == env.group {
					t.Fatalf("unexpected upstream payload: %s", got.body)
				}
			default:
				t.Fatal("upstream was not called")
			}
		})
	}
}

func TestGzipDecodedHeadersStrippedCaseInsensitive(t *testing.T) {
	// A distinct raw map makes the contract independent of the dependency's current aliasing.
	original := http.Header{"Content-Encoding": {"gzip"}, "cOnTeNt-EnCoDiNg": {"gzip"}, "content-length": {"123"}, "Content-Length": {"123"}, "X-Test": {"keep"}}
	raw := &httpclient.Request{Headers: original.Clone()}
	stripDecodedRequestHeaders(raw.Headers)
	if len(raw.Headers) != 1 || raw.Headers.Get("X-Test") != "keep" {
		t.Fatalf("headers: %#v", raw.Headers)
	}
	if original.Get("Content-Encoding") != "gzip" {
		t.Fatal("test maps unexpectedly alias")
	}
	stripDecodedRequestHeaders(nil)
}

// The first channel reports an encoding failure despite receiving plaintext. This also
// models an upstream-internal gzip hop; its error does not establish local compression.
func TestGzipCompatibilityFailoverAndClient400(t *testing.T) {
	for _, tc := range []struct {
		name, message         string
		wantCalls, wantStatus int
	}{
		{"encoding_compatibility", "Unsupported request content encoding: gzip", 2, http.StatusOK},
		{"genuine_client_error", "Invalid value for temperature", 1, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var credentials []string
			var badWire bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				mu.Lock()
				credentials = append(credentials, r.Header.Get("Authorization"))
				n := len(credentials)
				badWire = badWire || err != nil || !json.Valid(body) || r.Header.Get("Content-Encoding") != ""
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if n == 1 {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","message":%q}}`, tc.message)
					return
				}
				fmt.Fprint(w, gzipOKBody)
			}))
			defer srv.Close()
			env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2,
				model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
			w := forwardGzipRequest(t, env.userID, body, nil)
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if badWire {
				t.Fatal("upstream received compressed/non-JSON body or encoding header")
			}
			if len(credentials) != tc.wantCalls {
				t.Fatalf("calls %d want %d: %v", len(credentials), tc.wantCalls, credentials)
			}
			if len(credentials) == 2 && credentials[0] == credentials[1] {
				t.Fatalf("retried same channel instead of failover: %v", credentials)
			}
			if tc.wantStatus == http.StatusBadRequest && !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("client error lost: %s", w.Body.String())
			}
		})
	}
}

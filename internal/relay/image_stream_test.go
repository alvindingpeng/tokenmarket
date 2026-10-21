package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// newImageStreamingUpstream 起一个按 OpenAI Images 流式协议应答的上游: 每个请求返回两次
// partial_image 事件与一次 completed 事件, 最后以 [DONE] 收尾。统计收到的 POST 数。
func newImageStreamingUpstream() (*httptest.Server, *atomic.Int64) {
	var posts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: "+imageStreamPartialEvent+"\n")
		fmt.Fprint(w, "data: {\"object\":\"image\",\"index\":0,\"b64_json\":\"partial1\"}\n\n")
		fmt.Fprint(w, "event: "+imageStreamPartialEvent+"\n")
		fmt.Fprint(w, "data: {\"object\":\"image\",\"index\":0,\"b64_json\":\"partial2\"}\n\n")
		fmt.Fprint(w, "event: "+imageStreamCompletedEvent+"\n")
		fmt.Fprint(w, "data: {\"object\":\"image\",\"index\":0,\"b64_json\":\"final\",\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	return srv, &posts
}

// TestInspectImageStreamEvent 钉住生图流事件的终态判定: 只有 [DONE] 和 error 形状才算流末,
// partial_image 与 completed 事件都是继续传输的中间事件。
func TestInspectImageStreamEvent(t *testing.T) {
	partial := &httpclient.StreamEvent{Type: imageStreamPartialEvent, Data: []byte(`{"object":"image","index":0,"b64_json":"p"}`)}
	completed := &httpclient.StreamEvent{Type: imageStreamCompletedEvent, Data: []byte(`{"object":"image","b64_json":"f"}`)}
	done := &httpclient.StreamEvent{Data: []byte("[DONE]")}
	errEvt := &httpclient.StreamEvent{Type: "error", Data: []byte(`{"error":{"message":"boom","type":"server_error"}}`)}
	if last, err := inspectImageStreamEvent(partial); last || err != nil {
		t.Fatalf("partial event must keep stream open: last=%v err=%v", last, err)
	}
	if last, err := inspectImageStreamEvent(completed); last || err != nil {
		t.Fatalf("completed event must keep stream open: last=%v err=%v", last, err)
	}
	if last, err := inspectImageStreamEvent(done); !last || err != nil {
		t.Fatalf("[DONE] must end stream: last=%v err=%v", last, err)
	}
	if last, err := inspectImageStreamEvent(errEvt); !last || err == nil {
		t.Fatalf("error event must fail stream: last=%v err=%v", last, err)
	}
}

// TestAggregateImageStreamEvents 钉住流末聚合口径: partial 事件不计张, completed 事件计入,
// [DONE] 不构成图片对象; 产出 {"data":[...]} 供 mediaImageCount 直接数张, usage 取自 completed。
func TestAggregateImageStreamEvents(t *testing.T) {
	chunks := []*httpclient.StreamEvent{
		{Type: imageStreamPartialEvent, Data: []byte(`{"object":"image","index":0,"b64_json":"p1"}`)},
		{Type: imageStreamPartialEvent, Data: []byte(`{"object":"image","index":0,"b64_json":"p2"}`)},
		{Type: imageStreamCompletedEvent, Data: []byte(`{"object":"image","index":0,"b64_json":"f1","usage":{"prompt_tokens":2,"completion_tokens":1}}`)},
		{Type: imageStreamCompletedEvent, Data: []byte(`{"object":"image","index":1,"b64_json":"f2"}`)},
		{Data: []byte("[DONE]")},
	}
	body, meta := aggregateImageStreamEvents(chunks)
	var parsed struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("aggregated body not json: %v", err)
	}
	if len(parsed.Data) != 2 {
		t.Fatalf("completed images = %d, want 2 (partials excluded)", len(parsed.Data))
	}
	if mediaImageCount(string(body)) != 2 {
		t.Fatalf("mediaImageCount over aggregated body = %d, want 2", mediaImageCount(string(body)))
	}
	if meta.Usage == nil || meta.Usage.PromptTokens != 2 || meta.Usage.CompletionTokens != 1 {
		t.Fatalf("usage not extracted from completed event: %+v", meta.Usage)
	}
}

// TestImageStreamingForwardsAndBills 端到端钉住生图流式: 客户端 stream:true 的 generations 请求
// 被透传到上游, SSE 事件原样回给客户端; 流末按 completed 事件数张计费(partial 不计), 单次派发。
func TestImageStreamingForwardsAndBills(t *testing.T) {
	srv, posts := newImageStreamingUpstream()
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIImage, model.MediaKindImage, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"prompt":"a cat","stream":true}`, env.group)
	w := forwardRequest(llm.APIFormatOpenAIImageGeneration, env.userID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("image stream failed: status %d body %s", w.Code, w.Body.String())
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("image stream dispatched %d times, want 1", got)
	}
	got := w.Body.String()
	// 流式语义: partial 事件也要逐条透传给客户端(客户端据此显示进度), 因此这里断言全部在场;
	// partial 不计费由计费断言(record.MediaUnits.Images == 1)单独覆盖。
	for _, want := range []string{"partial1", "partial2", "final", "[DONE]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("client must receive %q in stream (got: %s)", want, got)
		}
	}
	state, ok := latestRelayRequest()
	if !ok {
		t.Fatal("request state missing after image stream")
	}
	if state.Status != StatusSuccess {
		t.Fatalf("want success final state, got %q", state.Status)
	}
	var record model.BillingRecord
	if err := db.GetDB().Where("request_id = ?", state.ID).First(&record).Error; err != nil {
		t.Fatalf("no billing record settled for the image stream: %v", err)
	}
	if record.MediaUnits.Images != 1 {
		t.Fatalf("image stream billed %d images, want 1 (partials excluded)", record.MediaUnits.Images)
	}
	if record.UserCost <= 0 {
		t.Fatalf("billed cost should cover the streamed image, got %.6f", record.UserCost)
	}
}

func init() { gin.SetMode(gin.TestMode) }

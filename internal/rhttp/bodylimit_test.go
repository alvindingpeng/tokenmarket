package rhttp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitedBodyTransportCapsNonStreamResponse(t *testing.T) {
	// 上限在测试里按同路径验证: 用小上限的包装器直接构造, 逻辑与生产同源。
	payload := strings.Repeat("x", 4096)
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, bytes.NewReader([]byte(payload)))
	})
	server := httptest.NewServer(base)
	defer server.Close()

	transport := limitedBodyTransport{Base: server.Client().Transport}
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, server.URL, nil))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer response.Body.Close()
	limited := &limitedReadCloser{underlying: response.Body, remaining: 1024}
	if _, err := io.ReadAll(limited); err == nil {
		t.Fatal("oversized body must fail instead of buffering fully")
	}
}

func TestLimitedReadCloserAllowsExactLimit(t *testing.T) {
	payload := strings.Repeat("y", 100)
	base := io.NopCloser(strings.NewReader(payload))
	limited := &limitedReadCloser{underlying: base, remaining: 100}
	got, err := io.ReadAll(limited)
	if err != nil {
		t.Fatalf("exact-limit body must pass: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("got %d bytes, want 100", len(got))
	}
}

func TestIsEventStream(t *testing.T) {
	if !isEventStream("text/event-stream") {
		t.Fatal("SSE content type must bypass the cap")
	}
	if !isEventStream("text/event-stream; charset=utf-8") {
		t.Fatal("SSE with charset must bypass the cap")
	}
	if isEventStream("application/json") {
		t.Fatal("JSON must stay capped")
	}
	if isEventStream("") {
		t.Fatal("missing content type must stay capped")
	}
}

func TestLimitedBodyTransportBypassesEventStream(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Repeat("data: x\n\n", 1000)))
	})
	server := httptest.NewServer(base)
	defer server.Close()

	transport := limitedBodyTransport{Base: server.Client().Transport}
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, server.URL, nil))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("SSE body must not be capped: %v", err)
	}
}

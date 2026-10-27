package xfyun

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignURL(t *testing.T) {
	got, err := SignURL("wss://example.com/v1.1/chat", Credentials{APIKey: "key", APISecret: "secret"}, time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("host") != "example.com" {
		t.Fatalf("host = %q", u.Query().Get("host"))
	}
	if !strings.Contains(RedactURL(got), "wss://example.com/v1.1/chat") || strings.Contains(RedactURL(got), "authorization=") {
		t.Fatalf("redaction failed: %s", RedactURL(got))
	}
	raw, err := base64.StdEncoding.DecodeString(u.Query().Get("authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "api_key=\"key\"") {
		t.Fatalf("authorization missing key")
	}
}

func TestEndpointFromBaseURL(t *testing.T) {
	got, err := EndpointFromBaseURL("https://spark-api-open.xf-yun.com", "generalv3.5")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://spark-api-open.xf-yun.com/v3.5/chat" {
		t.Fatalf("endpoint = %q", got)
	}
}

package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestSendPassthroughStreamHTTPErrorPreservesStatusAndBody(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		class  ErrorClass
	}{
		{name: "400 empty", status: http.StatusBadRequest, class: ErrorClassClient},
		{name: "401 html", status: http.StatusUnauthorized, body: "<html>unauthorized</html>", class: ErrorClassAuth},
		{name: "429 empty", status: http.StatusTooManyRequests, class: ErrorClassRate},
		{name: "529 html", status: 529, body: "<html>overloaded</html>", class: ErrorClassOverload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			request := &httpclient.Request{Method: http.MethodPost, URL: srv.URL, Body: []byte(`{"model":"test"}`), Headers: make(http.Header)}
			_, err := sendPassthroughStream(context.Background(), llm.APIFormatOpenAIChatCompletion, request, srv.Client())
			if err == nil {
				t.Fatal("expected HTTP error")
			}
			var failure *httpclient.Error
			if !errors.As(err, &failure) {
				t.Fatalf("expected httpclient.Error, got %T: %v", err, err)
			}
			if failure.StatusCode != tt.status {
				t.Fatalf("status code = %d, want %d", failure.StatusCode, tt.status)
			}
			wantStatusPrefix := fmt.Sprintf("%d ", tt.status)
			if !strings.HasPrefix(failure.Status, wantStatusPrefix) {
				t.Fatalf("status = %q, want prefix %q", failure.Status, wantStatusPrefix)
			}
			if string(failure.Body) != tt.body {
				t.Fatalf("body = %q, want %q", failure.Body, tt.body)
			}
			if got, rule := classifyUpstreamError(err); got != tt.class {
				t.Fatalf("classify = %s (%s), want %s", got, rule, tt.class)
			}
			if !strings.Contains(err.Error(), failure.Status) {
				t.Fatalf("error %q does not retain status %q", err, failure.Status)
			}
		})
	}
}

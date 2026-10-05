package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Failures below the API's own error responses: the request can't be built
// or sent, or the body isn't what was asked for. Each must come back as an
// error, never a zero-value success.

func TestDoRequestFailures(t *testing.T) {
	ctx := context.Background()

	if _, _, err := NewClient("http://x", "k").doRequest(ctx, "PUT", "http://x/", make(chan int)); err == nil ||
		!strings.Contains(err.Error(), "marshal") {
		t.Errorf("unmarshalable body: %v", err)
	}

	if _, _, err := NewClient("http://x", "k").doRequest(ctx, "GET", "://bad", nil); err == nil ||
		!strings.Contains(err.Error(), "create request") {
		t.Errorf("bad URL: %v", err)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, err := NewClient(closed.URL, "k").GetScopes(ctx); err == nil ||
		!strings.Contains(err.Error(), "request failed") {
		t.Errorf("unreachable API: %v", err)
	}
}

func TestErrorBodyWithoutMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>upstream down</html>"))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "k").GetTable(context.Background(), "default", "case")
	if err == nil || !strings.Contains(err.Error(), "API error (502): <html>upstream down</html>") {
		t.Errorf("non-JSON error body must be passed through, got %v", err)
	}
}

func TestSuccessBodyThatIsNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "k").GetTableDefinitions(context.Background(), "default")
	if err == nil || !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected an unmarshal error, got %v", err)
	}
}

func TestAPIErrorString(t *testing.T) {
	if got := (&APIError{Message: "Unknown table: x"}).String(); got != "Unknown table: x" {
		t.Errorf("String() = %q", got)
	}
}

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// What a slow or briefly unavailable API gets from the client (#29): which
// failures are retried for which method, how long it waits, and what the
// error says when it gives up.

// script answers request n with steps[n], then 200 {} for every request after
// the last step. A step of 0 drops the connection without answering.
type script struct {
	mu       sync.Mutex
	steps    []step
	requests []string // "METHOD body"
	release  chan struct{}
}

type step struct {
	status     int
	retryAfter string
	// hang never answers, so the attempt runs out of time. The handler is
	// released when the test ends, before the server closes.
	hang bool
}

func (s *script) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	n := len(s.requests)
	s.requests = append(s.requests, r.Method+" "+string(body))
	s.mu.Unlock()

	if n >= len(s.steps) {
		_, _ = w.Write([]byte(`{}`))
		return
	}
	st := s.steps[n]
	if st.hang {
		<-s.release
		return
	}
	if st.status == 0 {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	if st.retryAfter != "" {
		w.Header().Set("Retry-After", st.retryAfter)
	}
	w.WriteHeader(st.status)
	_, _ = w.Write([]byte(`{"error":"Service Unavailable","message":"try later"}`))
}

func (s *script) count() int {
	return len(s.sent())
}

func (s *script) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// scripted starts a server running steps and a client whose waits are
// recorded instead of slept.
func scripted(t *testing.T, steps ...step) (*Client, *script, *[]time.Duration) {
	t.Helper()
	s := &script{steps: steps, release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(s.release) }) // runs first: Close waits for handlers
	c := NewClient(srv.URL, "k")
	var waits []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	return c, s, &waits
}

func TestRetry_readRecoversFromGatewayErrors(t *testing.T) {
	c, s, waits := scripted(t, step{status: 503}, step{status: 502}, step{status: 504})
	if _, err := c.GetScopes(context.Background()); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if s.count() != 4 || len(*waits) != 3 {
		t.Errorf("requests = %d, waits = %d; want 4 and 3", s.count(), len(*waits))
	}
}

func TestRetry_saveResendsTheSameBody(t *testing.T) {
	c, s, _ := scripted(t, step{status: 0})
	if _, err := c.SaveTableDraft(context.Background(), "default", "case", []byte(`{"routeName":"case"}`)); err != nil {
		t.Fatalf("expected success after a dropped connection, got %v", err)
	}
	sent := s.sent()
	if len(sent) != 2 || sent[0] != sent[1] || !strings.Contains(sent[1], `"routeName":"case"`) {
		t.Errorf("retried PUT must resend its body, got %q", sent)
	}
}

func TestRetry_postOnlyRetriesWhenTheAPIDidNotAct(t *testing.T) {
	c, s, _ := scripted(t, step{status: 429}, step{status: 429})
	if _, err := c.PublishTables(context.Background(), "default", []string{"case"}); err != nil {
		t.Fatalf("429 should be retried for a POST, got %v", err)
	}
	if s.count() != 3 {
		t.Errorf("requests = %d, want 3", s.count())
	}

	// A 503 can be a front door's origin timeout, after the API took the
	// publish, so it isn't proof the API did nothing.
	for _, status := range []int{0, 502, 503, 504} {
		c, s, _ := scripted(t, step{status: status})
		_, err := c.PublishTables(context.Background(), "default", []string{"case"})
		if err == nil || strings.Contains(err.Error(), "gave up") {
			t.Errorf("status %d: a POST that may have landed must fail without retrying, got %v", status, err)
		}
		if s.count() != 1 {
			t.Errorf("status %d: requests = %d, want 1", status, s.count())
		}
	}
}

func TestRetry_errorsThatWontChangeAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 404, 500} {
		c, s, _ := scripted(t, step{status: status})
		_, err := c.GetTable(context.Background(), "default", "case")
		if err == nil || strings.Contains(err.Error(), "gave up") {
			t.Errorf("status %d: got %v", status, err)
		}
		if s.count() != 1 {
			t.Errorf("status %d: requests = %d, want 1", status, s.count())
		}
	}
}

func TestRetry_slowAttemptTimesOutThenSucceeds(t *testing.T) {
	c, s, _ := scripted(t, step{hang: true})
	c.HTTPClient.Timeout = 250 * time.Millisecond
	if _, err := c.GetScopes(context.Background()); err != nil {
		t.Fatalf("expected the second attempt to succeed, got %v", err)
	}
	if s.count() != 2 {
		t.Errorf("requests = %d, want 2", s.count())
	}
}

func TestRetry_givingUpSaysWhatToChange(t *testing.T) {
	c, s, _ := scripted(t, step{status: 503}, step{status: 503}, step{status: 503})
	c.MaxRetries = 2
	_, err := c.GetScopes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API error (503): try later (gave up after 3 attempts;") ||
		!strings.Contains(err.Error(), "raise timeout or max_retries") {
		t.Errorf("got %v", err)
	}
	if s.count() != 3 {
		t.Errorf("requests = %d, want 3", s.count())
	}

	// A timeout also names the per-attempt limit, even with retries off.
	c, s, _ = scripted(t, step{hang: true})
	c.HTTPClient.Timeout = 250 * time.Millisecond
	c.MaxRetries = 0
	_, err = c.GetScopes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gave up after 1 attempt, each limited to 250ms") {
		t.Errorf("got %v", err)
	}
	if s.count() != 1 {
		t.Errorf("max_retries = 0: requests = %d, want 1", s.count())
	}

	// A publish that times out isn't retried, but still says what to raise.
	c, s, _ = scripted(t, step{hang: true})
	c.HTTPClient.Timeout = 250 * time.Millisecond
	_, err = c.PublishTables(context.Background(), "default", []string{"case"})
	if err == nil || !strings.Contains(err.Error(), "timed out after 250ms and not retried") ||
		!strings.Contains(err.Error(), "raise timeout in the provider configuration") {
		t.Errorf("got %v", err)
	}
	if s.count() != 1 {
		t.Errorf("POST timeout: requests = %d, want 1", s.count())
	}
}

func TestRetry_retryAfterIsHonouredAndCapped(t *testing.T) {
	c, _, waits := scripted(t, step{status: 429, retryAfter: "7"}, step{status: 503, retryAfter: "120"}, step{status: 503})
	if _, err := c.GetScopes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if (*waits)[0] != 7*time.Second || (*waits)[1] != maxBackoff {
		t.Errorf("waits = %v; want 7s, then %s for a Retry-After over the cap", *waits, maxBackoff)
	}
	if w := (*waits)[2]; w < 0 || w > 4*time.Second {
		t.Errorf("third wait = %s, want the backoff for retry 3 (up to 4s)", w)
	}
}

func TestRetry_cancellationStopsRetrying(t *testing.T) {
	c, s, _ := scripted(t, step{status: 503}, step{status: 503})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepContext(ctx, d)
	}
	_, err := c.GetScopes(ctx)
	if err == nil || !strings.Contains(err.Error(), "stopped retrying: context canceled") {
		t.Errorf("got %v", err)
	}
	if s.count() != 1 {
		t.Errorf("requests = %d, want 1", s.count())
	}

	// Already cancelled: the failed attempt isn't retried at all.
	c, s, _ = scripted(t, step{status: 503})
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := c.GetScopes(cancelled); err == nil {
		t.Error("expected an error from a cancelled context")
	}
	if s.count() > 1 {
		t.Errorf("requests = %d, want at most 1", s.count())
	}
}

// A DELETE whose response was lost may have deleted the thing; the retry's
// 404 then means done. A 404 is still an error when nothing could have
// landed first.
func TestRetry_deleteThatAlreadyLanded(t *testing.T) {
	for _, first := range []int{0, 502, 504} {
		c, _, _ := scripted(t, step{status: first}, step{status: 404})
		if err := c.PermanentlyDeleteTable(context.Background(), "default", "case"); err != nil {
			t.Errorf("first attempt %d: retry's 404 should count as deleted, got %v", first, err)
		}
	}

	c, _, _ := scripted(t, step{status: 503}, step{status: 404})
	if err := c.PermanentlyDeleteTable(context.Background(), "default", "case"); !IsNotFound(err) {
		t.Errorf("after a 503 the 404 stands; got %v", err)
	}
	c, _, _ = scripted(t, step{status: 404})
	if err := c.PermanentlyDeleteTable(context.Background(), "default", "case"); !IsNotFound(err) {
		t.Errorf("first-attempt 404 must stay an error, got %v", err)
	}
}

// A connection that was never made can't have deleted anything, so a 404
// after it stays an error.
func TestRetry_deleteAfterADialErrorKeepsThe404(t *testing.T) {
	c, s, _ := scripted(t, step{status: 404})
	next := c.HTTPClient.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	dialed := false
	c.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !dialed {
			dialed = true
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}
		return next.RoundTrip(r)
	})
	if err := c.PermanentlyDeleteTable(context.Background(), "default", "case"); !IsNotFound(err) {
		t.Errorf("got %v", err)
	}
	if s.count() != 1 {
		t.Errorf("requests reaching the server = %d, want 1", s.count())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNeverSent(t *testing.T) {
	for err, want := range map[error]bool{
		&net.DNSError{Err: "no such host", Name: "api.example"}:  true,
		&net.OpError{Op: "dial", Err: errors.New("refused")}:     true,
		&net.OpError{Op: "read", Err: errors.New("reset")}:       false,
		errors.New("context deadline exceeded (Client.Timeout)"): false,
	} {
		if got := neverSent(fmt.Errorf("request failed: %w", err)); got != want {
			t.Errorf("neverSent(%v) = %v, want %v", err, got, want)
		}
	}
}

// A remove that fails says nothing about whether the thing is gone, so the
// delete must fail rather than let a lost purge response pass for success.
func TestRetry_deleteStopsWhenRemoveFails(t *testing.T) {
	ctx := context.Background()

	// remove (POST) 502, not retried; the purge's 502 + 404 must not be reached.
	c, s, _ := scripted(t, step{status: 502}, step{status: 502}, step{status: 404})
	if err := c.DeleteCustomApi(ctx, "default", "expand-calendar"); err == nil ||
		!strings.Contains(err.Error(), `failed to remove custom API "expand-calendar"`) {
		t.Errorf("custom API: got %v", err)
	}
	if s.count() != 1 {
		t.Errorf("custom API: requests = %d, want 1", s.count())
	}

	// unpublish 200, remove 502.
	c, s, _ = scripted(t, step{status: 200}, step{status: 502}, step{status: 502}, step{status: 404})
	if err := c.DeleteTable(ctx, "default", "case"); err == nil ||
		!strings.Contains(err.Error(), `failed to remove table "case"`) {
		t.Errorf("table: got %v", err)
	}
	if s.count() != 2 {
		t.Errorf("table: requests = %d, want 2", s.count())
	}

	// A 404 from remove is an answer: nothing was there. The purge runs.
	c, s, _ = scripted(t, step{status: 404})
	if err := c.DeleteCustomApi(ctx, "default", "expand-calendar"); err != nil {
		t.Errorf("remove 404 then purge 200: got %v", err)
	}
	if s.count() != 2 {
		t.Errorf("remove 404: requests = %d, want 2", s.count())
	}
}

func TestBackoff(t *testing.T) {
	for retry := 1; retry <= 8; retry++ {
		full := min(time.Second<<(retry-1), maxBackoff)
		for range 50 {
			if d := backoff(retry); d < 0 || d > full {
				t.Fatalf("backoff(%d) = %s, want between 0 and %s", retry, d, full)
			}
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	for v, want := range map[string]time.Duration{"": 0, "abc": 0, "-5": 0, "3": 3 * time.Second, past: 0} {
		if got := parseRetryAfter(v); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", v, got, want)
		}
	}
	if got := parseRetryAfter(future); got < 59*time.Minute {
		t.Errorf("parseRetryAfter(future date) = %s", got)
	}
}

func TestSleepContext(t *testing.T) {
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Errorf("sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sleep: %v", err)
	}
}

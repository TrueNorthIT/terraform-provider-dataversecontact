package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	// DefaultTimeout bounds each attempt at a request, not the request with
	// its retries.
	DefaultTimeout = 60 * time.Second
	// DefaultMaxRetries is how many times a failed request is retried when
	// the provider configuration doesn't say.
	DefaultMaxRetries = 3
	// maxBackoff caps the wait between attempts, Retry-After included.
	maxBackoff = 30 * time.Second
)

// Client is the HTTP client for the Dataverse Contact API.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	// MaxRetries is how many more times a request is sent after a failure
	// that is safe to retry (see retryable). 0 sends every request once.
	MaxRetries int

	// sleep waits between attempts. Tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewClient creates a new API client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: DefaultTimeout},
		MaxRetries: DefaultMaxRetries,
		sleep:      sleepContext,
	}
}

// adminURL builds the full URL for an admin endpoint.
func (c *Client) adminURL(scope, path string) string {
	return fmt.Sprintf("%s/api/v2/_admin/%s/%s", c.BaseURL, scope, path)
}

// doRequest executes an HTTP request with auth and logging, retrying the
// failures retryable allows for its method.
func (c *Client) doRequest(ctx context.Context, method, url string, body interface{}) ([]byte, int, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	// mayHaveLanded is set once an attempt fails in a way that doesn't say
	// whether the API acted on it: no response, or a gateway's 502 or 504.
	mayHaveLanded := false
	for attempt := 1; ; attempt++ {
		var reqBody io.Reader
		if payload != nil {
			reqBody = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		respBody, status, retryAfter, err := c.send(ctx, req)
		if err == nil && status == http.StatusNotFound && method == http.MethodDelete && mayHaveLanded {
			// An earlier attempt deleted it and its response was lost.
			tflog.Debug(ctx, "DELETE retried after an unanswered attempt is already gone", map[string]interface{}{"url": url})
			return respBody, status, nil
		}
		if err == nil && status >= 400 {
			err = statusError(status, respBody)
		}
		if err == nil {
			return respBody, status, nil
		}

		if !retryable(method, status) || ctx.Err() != nil {
			return respBody, status, err
		}
		if attempt > c.MaxRetries {
			return respBody, status, c.gaveUp(err, attempt)
		}
		if status == 0 || status == http.StatusBadGateway || status == http.StatusGatewayTimeout {
			mayHaveLanded = true
		}

		wait := backoff(attempt)
		if retryAfter > 0 {
			wait = min(retryAfter, maxBackoff)
		}
		tflog.Warn(ctx, "Retrying API request", map[string]interface{}{
			"method":  method,
			"url":     url,
			"attempt": attempt,
			"error":   err.Error(),
			"wait":    wait.String(),
		})
		if sleepErr := c.sleep(ctx, wait); sleepErr != nil {
			return respBody, status, fmt.Errorf("%w (stopped retrying: %v)", err, sleepErr)
		}
	}
}

// send makes one attempt. err is set only when no complete response came
// back; an error status is a response, returned with err nil.
func (c *Client) send(ctx context.Context, req *http.Request) (respBody []byte, status int, retryAfter time.Duration, err error) {
	tflog.Debug(ctx, "API request", map[string]interface{}{
		"method": req.Method,
		"url":    req.URL.String(),
	})

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to read response body: %w", err)
	}

	tflog.Debug(ctx, "API response", map[string]interface{}{
		"status": resp.StatusCode,
		"bytes":  len(respBody),
	})
	return respBody, resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After")), nil
}

// retryable reports whether a failure is worth another attempt. status 0
// means no response came back.
//
// GET, PUT and DELETE are idempotent here (a PUT replaces a draft whole), so
// they retry anything that may be a passing problem: no response, which
// includes an attempt that ran out of time, 429, and the gateway statuses a
// cold or overloaded App Service answers with. POST publishes, unpublishes and
// removes, and a second publish of a draft the first one already took fails;
// so a POST only retries 429 and 503, which say the API did not act.
func retryable(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	case 0, http.StatusBadGateway, http.StatusGatewayTimeout:
		return method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	}
	return false
}

// gaveUp explains a failure that was still retryable when the attempts ran
// out, so a slow API reads as something the configuration can fix.
func (c *Client) gaveUp(err error, attempts int) error {
	plural := "s"
	if attempts == 1 {
		plural = ""
	}
	limit := ""
	if isTimeout(err) && c.HTTPClient.Timeout > 0 {
		limit = fmt.Sprintf(", each limited to %s", c.HTTPClient.Timeout)
	}
	return fmt.Errorf("%w (gave up after %d attempt%s%s; if the API is slow, raise timeout or max_retries in the provider configuration)",
		err, attempts, plural, limit)
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func statusError(status int, respBody []byte) error {
	var apiErr APIError
	if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
		return fmt.Errorf("API error (%d): %s", status, apiErr.Message)
	}
	return fmt.Errorf("API error (%d): %s", status, string(respBody))
}

// backoff is the wait before retry n (1-based): 1s, 2s, 4s and so on up to
// maxBackoff, scaled by a random factor between 0.5 and 1 so resources that
// failed together don't all retry together.
func backoff(retry int) time.Duration {
	d := maxBackoff
	if retry <= 5 {
		d = min(time.Second<<(retry-1), maxBackoff)
	}
	return d/2 + rand.N(d/2+1)
}

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date.
// Anything else, or a time already past, is 0.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// doJSON executes a request and unmarshals the response into the target.
func (c *Client) doJSON(ctx context.Context, method, url string, body interface{}, target interface{}) error {
	respBody, _, err := c.doRequest(ctx, method, url, body)
	if err != nil {
		return err
	}

	if target != nil {
		if err := json.Unmarshal(respBody, target); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// IsNotFound checks if an error is a 404 response.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "API error (404)")
}

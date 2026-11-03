// Package client is a small client for the SpinupWP REST API (https://api.spinupwp.com).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the SpinupWP API base URL.
const DefaultBaseURL = "https://api.spinupwp.app/v1"

// Client talks to the SpinupWP API.
type Client struct {
	baseURL    string
	token      string
	userAgent  string
	httpClient *http.Client

	// maxRetries is how many times a request is retried after a 429, or a 5xx on a GET.
	maxRetries int
	// retryWait is the wait before a retry when the API sends no Retry-After header.
	retryWait time.Duration
	// PollInterval is how often WaitForEvent checks an event.
	PollInterval time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (used by tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithRetry sets how often and how long to retry rate-limited or failed requests.
func WithRetry(maxRetries int, wait time.Duration) Option {
	return func(c *Client) { c.maxRetries, c.retryWait = maxRetries, wait }
}

// New returns a Client authenticated with an API token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL:      DefaultBaseURL,
		token:        token,
		userAgent:    "terraform-provider-spinupwp",
		httpClient:   &http.Client{Timeout: 60 * time.Second},
		maxRetries:   5,
		retryWait:    10 * time.Second,
		PollInterval: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Error is a non-2xx response from the API.
type Error struct {
	StatusCode int
	Message    string
	// Errors holds field validation messages (422 responses).
	Errors map[string][]string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("spinupwp: %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	for field, errs := range e.Errors {
		msg += fmt.Sprintf("; %s: %s", field, strings.Join(errs, ", "))
	}
	return msg
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// do sends a request and decodes a JSON response into out. body is nil, url.Values
// (sent form-encoded) or any other value (sent as JSON, for nulls and empty arrays).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, c.retryDelay(lastErr)); err != nil {
				return err
			}
		}

		resp, err := c.send(ctx, method, path, body)
		if err != nil {
			return err
		}
		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("spinupwp: reading response: %w", err)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil || len(respBody) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("spinupwp: decoding response: %w", err)
			}
			return nil
		}

		apiErr := &Error{StatusCode: resp.StatusCode}
		_ = json.Unmarshal(respBody, apiErr)
		lastErr = &retryableError{err: apiErr, after: resp.Header.Get("Retry-After")}

		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode >= 500 && method == http.MethodGet)
		if !retryable {
			return apiErr
		}
	}
	return errors.Unwrap(lastErr)
}

func (c *Client) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		reader, contentType = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("spinupwp: encoding request: %w", err)
		}
		reader, contentType = bytes.NewReader(data), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("spinupwp: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spinupwp: %s %s: %w", method, path, err)
	}
	return resp, nil
}

type retryableError struct {
	err   error
	after string
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

func (c *Client) retryDelay(err error) time.Duration {
	var r *retryableError
	if errors.As(err, &r) {
		if secs, convErr := strconv.Atoi(r.after); convErr == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return c.retryWait
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

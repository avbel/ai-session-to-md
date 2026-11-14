// Package llm implements a hand-written OpenAI-compatible chat/completions
// client: thinking-style mapping, extra-body merging, bounded retries and
// token usage reporting.
package llm

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrUsage marks a configuration problem; callers can match it with errors.Is
// to derive exit code 2.
var ErrUsage = errors.New("llm: usage error")

const (
	userAgentPrefix = "ai-session-to-md/"
	maxAttempts     = 3
	maxErrorBodyLen = 500
	maxRetryAfter   = time.Minute
)

// retryableStatuses are the HTTP statuses a request is retried on.
var retryableStatuses = map[int]bool{
	http.StatusRequestTimeout:      true,
	http.StatusTooManyRequests:     true,
	http.StatusInternalServerError: true,
	http.StatusBadGateway:          true,
	http.StatusServiceUnavailable:  true,
	http.StatusGatewayTimeout:      true,
}

// Usage is the token accounting returned by the server.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// APIError is a non-2xx response. Body is the redacted, truncated response
// body; it never contains request headers or the API key.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("llm: HTTP %d", e.Status)
	}
	return fmt.Sprintf("llm: HTTP %d: %s", e.Status, e.Body)
}

// WaitFunc sleeps for delay, or returns early when ctx is cancelled. Tests
// replace it to keep retries fast and to observe the computed delay.
type WaitFunc func(ctx context.Context, delay time.Duration) error

// Options configures a Client. Every validation failure from New is a usage
// error (ErrUsage).
type Options struct {
	BaseURL         string
	Model           string
	APIKey          string
	Version         string
	MaxOutputTokens int
	ThinkingStyle   string
	ThinkingLevel   string
	ExtraBody       json.RawMessage
	Redact          func(string) string
	HTTPClient      *http.Client
	Wait            WaitFunc
}

// Client posts chat/completions requests to an OpenAI-compatible endpoint.
type Client struct {
	base       *url.URL
	model      string
	apiKey     string
	version    string
	style      ThinkingStyle
	level      string
	maxTokens  int
	extra      map[string]any
	redact     func(string) string
	httpClient *http.Client
	wait       WaitFunc
}

// New validates the options and returns a ready client.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("%w: model is required", ErrUsage)
	}
	base, err := validateBaseURL(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	if opts.APIKey == "" && !isLoopbackHost(base.Hostname()) {
		return nil, fmt.Errorf("%w: no API key: set the env var named by --api-key-env or use a loopback base URL", ErrUsage)
	}
	style, err := normalizeStyle(opts.ThinkingStyle)
	if err != nil {
		return nil, err
	}
	level, err := normalizeLevel(opts.ThinkingLevel)
	if err != nil {
		return nil, err
	}
	extra, err := parseExtraBody(opts.ExtraBody)
	if err != nil {
		return nil, err
	}
	c := &Client{
		base:       base,
		model:      opts.Model,
		apiKey:     opts.APIKey,
		version:    cmp.Or(opts.Version, "dev"),
		style:      style,
		level:      level,
		maxTokens:  opts.MaxOutputTokens,
		extra:      extra,
		redact:     opts.Redact,
		httpClient: opts.HTTPClient,
		wait:       opts.Wait,
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}
	if c.wait == nil {
		c.wait = sleepWait
	}
	return c, nil
}

// Summarize matches the summarizer seam used by the reduce step: it takes a
// system and a user message and returns only the answer text.
func (c *Client) Summarize(ctx context.Context, system, user string) (string, error) {
	text, _, err := c.Complete(ctx, system, user)
	return text, err
}

// Complete sends one chat completion and returns the answer text plus the
// usage reported by the server. Retries stay inside the caller's ctx.
func (c *Client) Complete(ctx context.Context, system, user string) (string, Usage, error) {
	payload, err := c.buildBody(system, user)
	if err != nil {
		return "", Usage{}, err
	}
	for attempt := 1; ; attempt++ {
		content, usage, retryable, retryAfter, err := c.attempt(ctx, payload)
		if err == nil {
			return content, usage, nil
		}
		if !retryable || attempt >= maxAttempts {
			return "", Usage{}, err
		}
		if waitErr := c.waitBefore(ctx, attempt, retryAfter); waitErr != nil {
			return "", Usage{}, fmt.Errorf("llm: before retry %d: %w", attempt+1, waitErr)
		}
	}
}

// attempt performs one HTTP request. retryable reports whether the failure
// (when err != nil) should be retried; retryAfter is the server-provided wait.
func (c *Client) attempt(ctx context.Context, payload []byte) (string, Usage, bool, time.Duration, error) {
	resp, body, err := c.post(ctx, payload)
	if err != nil {
		retryable := !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
		return "", Usage{}, retryable, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{Status: resp.StatusCode, Body: c.redactSnippet(body)}
		return "", Usage{}, retryableStatuses[resp.StatusCode], retryAfterOf(resp.Header.Get("Retry-After")), apiErr
	}
	content, usage, err := c.parseResponse(body)
	if err != nil {
		return "", Usage{}, false, 0, err
	}
	return content, usage, false, 0, nil
}

func (c *Client) post(ctx context.Context, payload []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgentPrefix+c.version)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("llm: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("llm: read response: %w", err)
	}
	return resp, body, nil
}

func (c *Client) endpoint() string {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/chat/completions"
	return u.String()
}

// buildBody assembles the chat/completions request body: fixed fields, then
// thinking fields, then the user's extra body merged in last so it wins.
func (c *Client) buildBody(system, user string) ([]byte, error) {
	body := map[string]any{
		"model":    c.model,
		"messages": []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		"stream":   false,
	}
	if c.maxTokens > 0 {
		body["max_tokens"] = c.maxTokens
	}
	applyThinking(body, resolveStyle(c.style, c.base.Hostname()), c.level)
	if len(c.extra) > 0 {
		deepMerge(body, c.extra)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: encode request body: %w", err)
	}
	return payload, nil
}

func (c *Client) parseResponse(body []byte) (string, Usage, error) {
	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", Usage{}, fmt.Errorf("llm: parse response: %w: body: %s", err, c.redactSnippet(body))
	}
	if len(out.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("llm: response has no choices: body: %s", c.redactSnippet(body))
	}
	choice := out.Choices[0]
	if choice.FinishReason == "length" {
		return "", out.Usage, fmt.Errorf("llm: response truncated (finish_reason length); raise --max-output-tokens")
	}
	msg := choice.Message
	if msg.Content == "" {
		if msg.ReasoningContent != "" || msg.Reasoning != "" {
			return "", out.Usage, errors.New("llm: model returned only reasoning; raise --max-output-tokens")
		}
		return "", out.Usage, fmt.Errorf("llm: model returned empty content (finish_reason %q)", choice.FinishReason)
	}
	return msg.Content, out.Usage, nil
}

// redactSnippet applies the caller's redactor and truncates, in that order,
// so a cut-off secret can no longer leak.
func (c *Client) redactSnippet(body []byte) string {
	s := string(body)
	if c.redact != nil {
		s = c.redact(s)
	}
	if len(s) > maxErrorBodyLen {
		s = s[:maxErrorBodyLen] + "…"
	}
	return s
}

func (c *Client) waitBefore(ctx context.Context, attempt int, retryAfter time.Duration) error {
	delay := backoffDelay(attempt)
	if retryAfter > 0 {
		delay = min(retryAfter, maxRetryAfter)
	}
	return c.wait(ctx, delay)
}

// backoffDelay doubles from 2s with ±20% jitter.
func backoffDelay(attempt int) time.Duration {
	bases := [3]time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	base := bases[min(attempt-1, len(bases)-1)]
	return time.Duration(float64(base) * (0.8 + 0.4*rand.Float64())) //nolint:gosec // jitter, not a security value
}

// sleepWait is the default WaitFunc.
func sleepWait(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryAfterOf parses a Retry-After seconds header; 0 means absent/invalid.
func retryAfterOf(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, maxRetryAfter)
}

// validateBaseURL enforces https, or http on a loopback host only.
func validateBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return nil, fmt.Errorf("%w: base URL is required", ErrUsage)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: parse base URL %q", ErrUsage, raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return nil, fmt.Errorf("%w: base URL %q is http:// but the host is not loopback; the API key would be sent in cleartext", ErrUsage, raw)
		}
	default:
		return nil, fmt.Errorf("%w: base URL scheme must be https (or http on loopback), got %q", ErrUsage, u.Scheme)
	}
	return u, nil
}

// isLoopbackHost reports whether the host is localhost, 127.x or ::1.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || strings.HasPrefix(host, "127.") || host == "::1" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// parseExtraBody decodes --extra-body / config extra_body; it must be a JSON
// object so it can be deep-merged.
func parseExtraBody(raw json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var extra map[string]any
	if err := json.Unmarshal(trimmed, &extra); err != nil {
		return nil, fmt.Errorf("%w: --extra-body must be a JSON object: %v", ErrUsage, err)
	}
	if extra == nil {
		return nil, fmt.Errorf("%w: --extra-body must be a JSON object", ErrUsage)
	}
	return extra, nil
}

// deepMerge merges extra into dst in place: nested objects merge, everything
// else (including type conflicts) is overridden by extra.
func deepMerge(dst, extra map[string]any) {
	for k, v := range extra {
		if dstMap, ok := dst[k].(map[string]any); ok {
			if extraMap, ok := v.(map[string]any); ok {
				deepMerge(dstMap, extraMap)
				continue
			}
		}
		dst[k] = v
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatChoice struct {
	FinishReason string          `json:"finish_reason"`
	Message      chatResponseMsg `json:"message"`
}

type chatResponseMsg struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	Reasoning        string `json:"reasoning"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

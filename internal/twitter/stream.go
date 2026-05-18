package twitter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultStreamKeepAliveTimeout = 20 * time.Second
	defaultStreamRuleTag          = "note-tweet-connector"
)

var (
	FilteredStreamEndpoint      = "https://api.x.com/2/tweets/search/stream"
	FilteredStreamRulesEndpoint = "https://api.x.com/2/tweets/search/stream/rules"
	ErrStreamKeepAliveTimeout   = errors.New("twitter stream keep-alive timeout")
)

type StreamRule struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Tag   string `json:"tag"`
}

type StreamClient struct {
	BearerTokenSource BearerTokenSource
	HTTPClient        *http.Client
	StreamEndpoint    string
	RulesEndpoint     string
	KeepAliveTimeout  time.Duration
	OnConnect         func()
}

type StreamHTTPError struct {
	StatusCode int
	Body       string
}

func (e *StreamHTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("twitter stream request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("twitter stream request failed with status %d: %s", e.StatusCode, e.Body)
}

type StreamRateLimitError struct {
	StatusCode int
	ResetAt    time.Time
	Body       string
}

func (e *StreamRateLimitError) Error() string {
	if e.ResetAt.IsZero() {
		return fmt.Sprintf("twitter stream rate limited with status %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("twitter stream rate limited with status %d until %s: %s", e.StatusCode, e.ResetAt.Format(time.RFC3339), e.Body)
}

func DefaultStreamRule(username string) string {
	if username == "" {
		return ""
	}
	return "from:" + username + " -is:retweet -is:reply"
}

func DefaultStreamRuleTag() string {
	return defaultStreamRuleTag
}

func NewStreamClient(source BearerTokenSource) *StreamClient {
	return &StreamClient{
		BearerTokenSource: source,
		HTTPClient:        httpClient,
		StreamEndpoint:    FilteredStreamEndpoint,
		RulesEndpoint:     FilteredStreamRulesEndpoint,
		KeepAliveTimeout:  defaultStreamKeepAliveTimeout,
	}
}

func (c *StreamClient) EnsureRule(ctx context.Context, value, tag string) error {
	if value == "" {
		return fmt.Errorf("twitter stream rule is not configured")
	}
	if tag == "" {
		tag = defaultStreamRuleTag
	}

	rules, err := c.ListRules(ctx)
	if err != nil {
		return err
	}

	var hasRule bool
	var staleIDs []string
	for _, rule := range rules {
		if rule.Tag != tag {
			continue
		}
		if rule.Value == value {
			hasRule = true
			continue
		}
		staleIDs = append(staleIDs, rule.ID)
	}
	if len(staleIDs) > 0 {
		if err := c.DeleteRules(ctx, staleIDs); err != nil {
			return err
		}
	}
	if hasRule {
		return nil
	}
	return c.AddRule(ctx, value, tag)
}

func (c *StreamClient) ListRules(ctx context.Context) ([]StreamRule, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.rulesEndpoint(), nil)
	if err != nil {
		return nil, err
	}
	respBytes, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var listResp struct {
		Data []StreamRule `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse twitter stream rules response: %w", err)
	}
	return listResp.Data, nil
}

func (c *StreamClient) AddRule(ctx context.Context, value, tag string) error {
	body, err := json.Marshal(map[string][]map[string]string{
		"add": {{
			"value": value,
			"tag":   tag,
		}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rulesEndpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.doRequest(req)
	return err
}

func (c *StreamClient) DeleteRules(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string]map[string][]string{
		"delete": {"ids": ids},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rulesEndpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.doRequest(req)
	return err
}

func (c *StreamClient) Consume(ctx context.Context, handleLine func(context.Context, []byte) error) error {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(readCtx, http.MethodGet, c.streamURL(), nil)
	if err != nil {
		return err
	}
	if err := c.authorize(ctx, req); err != nil {
		return err
	}

	client := c.client()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return rateLimitError(resp)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return httpError(resp)
	}
	if c.OnConnect != nil {
		c.OnConnect()
	}

	return c.consumeBody(readCtx, cancel, resp.Body, handleLine)
}

func (c *StreamClient) consumeBody(ctx context.Context, cancel context.CancelFunc, body io.Reader, handleLine func(context.Context, []byte) error) error {
	keepAliveTimeout := c.KeepAliveTimeout
	if keepAliveTimeout <= 0 {
		keepAliveTimeout = defaultStreamKeepAliveTimeout
	}

	var timedOut atomic.Bool
	timer := time.AfterFunc(keepAliveTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer timer.Stop()

	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if timedOut.Load() {
				return ErrStreamKeepAliveTimeout
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if timedOut.Load() {
			return ErrStreamKeepAliveTimeout
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			timer.Reset(keepAliveTimeout)
			continue
		}
		timer.Stop()
		if err := handleLine(ctx, line); err != nil {
			return err
		}
		timer.Reset(keepAliveTimeout)
	}
}

func (c *StreamClient) doRequest(req *http.Request) ([]byte, error) {
	if err := c.authorize(req.Context(), req); err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, rateLimitError(resp)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, httpError(resp)
	}
	return io.ReadAll(resp.Body)
}

func (c *StreamClient) authorize(ctx context.Context, req *http.Request) error {
	if c.BearerTokenSource == nil {
		return fmt.Errorf("twitter bearer token source is not configured")
	}
	token, err := c.BearerTokenSource.BearerToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (c *StreamClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return httpClient
}

func (c *StreamClient) rulesEndpoint() string {
	if c.RulesEndpoint != "" {
		return c.RulesEndpoint
	}
	return FilteredStreamRulesEndpoint
}

func (c *StreamClient) streamURL() string {
	endpoint := c.StreamEndpoint
	if endpoint == "" {
		endpoint = FilteredStreamEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	q := parsed.Query()
	q.Set("tweet.fields", strings.Join([]string{
		"author_id",
		"attachments",
		"entities",
		"referenced_tweets",
		"in_reply_to_user_id",
		"edit_history_tweet_ids",
	}, ","))
	q.Set("expansions", strings.Join([]string{
		"author_id",
		"attachments.media_keys",
		"referenced_tweets.id",
		"referenced_tweets.id.author_id",
	}, ","))
	q.Set("user.fields", "username")
	q.Set("media.fields", "type,url,preview_image_url")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func httpError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &StreamHTTPError{
		StatusCode: resp.StatusCode,
		Body:       string(bytes.TrimSpace(body)),
	}
}

func rateLimitError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var resetAt time.Time
	if reset := resp.Header.Get("x-rate-limit-reset"); reset != "" {
		if unix, err := parseUnixTimestamp(reset); err == nil {
			resetAt = time.Unix(unix, 0)
		}
	}
	return &StreamRateLimitError{
		StatusCode: resp.StatusCode,
		ResetAt:    resetAt,
		Body:       string(bytes.TrimSpace(body)),
	}
}

func parseUnixTimestamp(value string) (int64, error) {
	var unix int64
	if _, err := fmt.Sscanf(value, "%d", &unix); err != nil {
		return 0, err
	}
	return unix, nil
}

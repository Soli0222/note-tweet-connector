package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const responsePreviewLimit = 512

type EventKind string

const (
	EventTwitterAuthorizationRequired  EventKind = "twitter_authorization_required"
	EventTwitterAuthorizationRecovered EventKind = "twitter_authorization_recovered"
	EventTwitterPostFailed             EventKind = "twitter_post_failed"
	EventTwitterMediaUploadFailed      EventKind = "twitter_media_upload_failed"
	EventTwitterStreamDisconnectLoop   EventKind = "twitter_stream_disconnect_loop"
	EventMisskeyAPIFailed              EventKind = "misskey_api_failed"
)

type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
	SeverityInfo    Severity = "info"
)

type Field struct {
	Name  string
	Value string
}

type Event struct {
	Kind      EventKind
	Severity  Severity
	Title     string
	Message   string
	Fields    []Field
	DedupeKey string
}

type Notifier interface {
	Notify(ctx context.Context, event Event) error
}

type NoopNotifier struct{}

func (NoopNotifier) Notify(ctx context.Context, event Event) error {
	return nil
}

type DiscordNotifier struct {
	WebhookURL string
	Timeout    time.Duration
	HTTPClient *http.Client
}

func NewDiscordNotifier(webhookURL string, timeout time.Duration) Notifier {
	if webhookURL == "" {
		return NoopNotifier{}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &DiscordNotifier{
		WebhookURL: webhookURL,
		Timeout:    timeout,
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

func (n *DiscordNotifier) Notify(ctx context.Context, event Event) error {
	if n.WebhookURL == "" {
		return nil
	}

	payload := discordPayload{
		Embeds: []discordEmbed{{
			Title:       event.Title,
			Description: event.Message,
			Color:       discordColor(event.Severity),
			Fields:      discordFields(event.Fields),
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, n.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, n.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("discord webhook failed with status %d: %s", resp.StatusCode, previewBody(respBytes))
	}
	return nil
}

type DedupeNotifier struct {
	next   Notifier
	window time.Duration

	mu   sync.Mutex
	sent map[string]time.Time
}

func NewDedupeNotifier(next Notifier, window time.Duration) *DedupeNotifier {
	if next == nil {
		next = NoopNotifier{}
	}
	return &DedupeNotifier{
		next:   next,
		window: window,
		sent:   map[string]time.Time{},
	}
}

func (n *DedupeNotifier) Notify(ctx context.Context, event Event) error {
	if event.DedupeKey != "" && n.window > 0 {
		now := time.Now()
		n.mu.Lock()
		if last, ok := n.sent[event.DedupeKey]; ok && now.Sub(last) < n.window {
			n.mu.Unlock()
			return nil
		}
		n.sent[event.DedupeKey] = now
		n.pruneLocked(now)
		n.mu.Unlock()
	}
	return n.next.Notify(ctx, event)
}

func (n *DedupeNotifier) pruneLocked(now time.Time) {
	for key, last := range n.sent {
		if now.Sub(last) >= n.window {
			delete(n.sent, key)
		}
	}
}

type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Timestamp   string         `json:"timestamp"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

func discordFields(fields []Field) []discordField {
	result := make([]discordField, 0, len(fields))
	for _, field := range fields {
		if field.Name == "" || field.Value == "" {
			continue
		}
		result = append(result, discordField{
			Name:   field.Name,
			Value:  truncate(field.Value, 1024),
			Inline: false,
		})
	}
	return result
}

func discordColor(severity Severity) int {
	switch severity {
	case SeverityError:
		return 0xD83A34
	case SeverityWarning:
		return 0xF0B429
	case SeverityInfo:
		return 0x2F80ED
	default:
		return 0x808080
	}
}

func previewBody(body []byte) string {
	preview := strings.TrimSpace(string(body))
	return truncate(preview, responsePreviewLimit)
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

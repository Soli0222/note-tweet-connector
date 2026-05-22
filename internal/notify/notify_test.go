package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscordNotifierSendsEmbed(t *testing.T) {
	var got discordPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotContentType := r.Header.Get("Content-Type"); gotContentType != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", gotContentType)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := &DiscordNotifier{
		WebhookURL: server.URL,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
	}
	err := notifier.Notify(context.Background(), Event{
		Kind:     EventTwitterAuthorizationRequired,
		Severity: SeverityWarning,
		Title:    "title",
		Message:  "message",
		Fields: []Field{{
			Name:  "Login URL",
			Value: "https://example.com/login",
		}},
	})
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(got.Embeds))
	}
	if got.Embeds[0].Title != "title" || got.Embeds[0].Description != "message" {
		t.Fatalf("embed = %#v, want title/message", got.Embeds[0])
	}
	if len(got.Embeds[0].Fields) != 1 || got.Embeds[0].Fields[0].Value != "https://example.com/login" {
		t.Fatalf("fields = %#v, want login URL field", got.Embeds[0].Fields)
	}
}

func TestDiscordNotifierNon2xxDoesNotLeakWebhookURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad webhook", http.StatusBadRequest)
	}))
	defer server.Close()

	notifier := &DiscordNotifier{
		WebhookURL: server.URL + "/secret-token",
		Timeout:    time.Second,
		HTTPClient: server.Client(),
	}
	err := notifier.Notify(context.Background(), Event{Title: "title", Message: "message"})
	if err == nil {
		t.Fatal("Notify() succeeded, want error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("Notify() error leaked webhook URL: %v", err)
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("Notify() error = %v, want status code", err)
	}
}

func TestNoopNotifier(t *testing.T) {
	if err := (NoopNotifier{}).Notify(context.Background(), Event{}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
}

func TestDedupeNotifierSuppressesWithinWindow(t *testing.T) {
	var calls int32
	notifier := NewDedupeNotifier(notifierFunc(func(ctx context.Context, event Event) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}), time.Hour)

	event := Event{DedupeKey: "same"}
	if err := notifier.Notify(context.Background(), event); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if err := notifier.Notify(context.Background(), event); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

type notifierFunc func(context.Context, Event) error

func (f notifierFunc) Notify(ctx context.Context, event Event) error {
	return f(ctx, event)
}

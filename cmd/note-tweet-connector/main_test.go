package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/notify"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
)

func TestNotifyTwitterOAuth2AuthorizationRequiredDedupesLoginURL(t *testing.T) {
	manager, err := twitter.NewTokenManager(twitter.OAuth2Config{
		ClientID:    "client-1",
		RedirectURL: "https://example.com/twitter/callback",
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	login, err := twitter.NewOAuth2LoginManager(manager, twitter.OAuth2Config{
		ClientID:    "client-1",
		RedirectURL: "https://example.com/twitter/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2LoginManager() error = %v", err)
	}
	recorder := &mainRecordingNotifier{}
	notifier := notify.NewDedupeNotifier(recorder, time.Hour)

	notifyTwitterOAuth2AuthorizationRequired(context.Background(), login, notifier)
	notifyTwitterOAuth2AuthorizationRequired(context.Background(), login, notifier)

	if len(recorder.events) != 1 {
		t.Fatalf("events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Kind != notify.EventTwitterAuthorizationRequired {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventTwitterAuthorizationRequired)
	}
	assertMainField(t, event, "Login URL")
	assertMainField(t, event, "有効期限")
}

func TestNotifyTwitterOAuth2AuthorizationRecovered(t *testing.T) {
	recorder := &mainRecordingNotifier{}

	notifyTwitterOAuth2AuthorizationRecovered(context.Background(), recorder)

	if len(recorder.events) != 1 {
		t.Fatalf("events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Kind != notify.EventTwitterAuthorizationRecovered {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventTwitterAuthorizationRecovered)
	}
	assertMainField(t, event, "完了時刻")
}

func TestNotifyFailureDoesNotPanic(t *testing.T) {
	manager, err := twitter.NewTokenManager(twitter.OAuth2Config{
		ClientID:    "client-1",
		RedirectURL: "https://example.com/twitter/callback",
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	login, err := twitter.NewOAuth2LoginManager(manager, twitter.OAuth2Config{
		ClientID:    "client-1",
		RedirectURL: "https://example.com/twitter/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2LoginManager() error = %v", err)
	}

	notifyTwitterOAuth2AuthorizationRequired(context.Background(), login, mainFailingNotifier{})
	notifyTwitterOAuth2AuthorizationRecovered(context.Background(), mainFailingNotifier{})
}

func TestStreamDisconnectLoopTracker(t *testing.T) {
	tracker := &streamDisconnectLoopTracker{
		window:    10 * time.Minute,
		threshold: 3,
	}
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

	if got := tracker.record(now); got != 1 {
		t.Fatalf("record() = %d, want 1", got)
	}
	if got := tracker.record(now.Add(time.Minute)); got != 2 {
		t.Fatalf("record() = %d, want 2", got)
	}
	if got := tracker.record(now.Add(2 * time.Minute)); got != 3 {
		t.Fatalf("record() = %d, want 3", got)
	}
	if got := tracker.record(now.Add(20 * time.Minute)); got != 1 {
		t.Fatalf("record() after window = %d, want 1", got)
	}
}

func TestNotifyTwitterStreamDisconnectLoop(t *testing.T) {
	recorder := &mainRecordingNotifier{}
	notifyTwitterStreamDisconnectLoop(context.Background(), recorder, 10*time.Minute, 5, "eof", errors.New("EOF"), 5*time.Second)

	if len(recorder.events) != 1 {
		t.Fatalf("events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Kind != notify.EventTwitterStreamDisconnectLoop {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventTwitterStreamDisconnectLoop)
	}
	assertMainField(t, event, "disconnect_count")
	assertMainField(t, event, "latest_reason")
}

type mainRecordingNotifier struct {
	events []notify.Event
}

func (n *mainRecordingNotifier) Notify(ctx context.Context, event notify.Event) error {
	n.events = append(n.events, event)
	return nil
}

type mainFailingNotifier struct{}

func (mainFailingNotifier) Notify(ctx context.Context, event notify.Event) error {
	return errors.New("discord unavailable")
}

func assertMainField(t *testing.T, event notify.Event, name string) {
	t.Helper()
	for _, field := range event.Fields {
		if field.Name == name && field.Value != "" {
			return
		}
	}
	t.Fatalf("field %q not found in %#v", name, event.Fields)
}

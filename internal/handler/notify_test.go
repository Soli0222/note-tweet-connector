package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/misskey"
	"github.com/Soli0222/note-tweet-connector/internal/notify"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
)

func TestNote2TweetNotifiesTwitterPostFailure(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, time.Hour)
	m := metrics.NewNoop()
	notifier := &recordingNotifier{}

	oldPost := postTweet
	defer func() { postTweet = oldPost }()
	postTweet = func(ctx context.Context, text string) (string, error) {
		return "", &twitter.APIError{
			Operation:   "POST request",
			StatusCode:  403,
			BodyPreview: `{"detail":"duplicate"}`,
		}
	}

	payload := []byte(`{
		"server": "https://misskey.example",
		"body": {
			"note": {
				"id": "note-1",
				"visibility": "public",
				"text": "hello"
			}
		}
	}`)
	err := Note2TweetHandlerWithConfig(ctx, Config{Notifier: notifier}, payload, crossPostTracker, m)
	if err == nil {
		t.Fatal("Note2TweetHandlerWithConfig() succeeded, want error")
	}
	event := notifier.singleEvent(t)
	if event.Kind != notify.EventTwitterPostFailed {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventTwitterPostFailed)
	}
	assertField(t, event, "note_id", "note-1")
	assertField(t, event, "status", "403")
}

func TestNote2TweetNotifiesTwitterMediaUploadFailure(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, time.Hour)
	m := metrics.NewNoop()
	notifier := &recordingNotifier{}

	oldPostWithMedia := postTweetWithMedia
	defer func() { postTweetWithMedia = oldPostWithMedia }()
	postTweetWithMedia = func(ctx context.Context, text string, fileURLs []string) (string, error) {
		return "", &twitter.APIError{
			Operation:   "media upload",
			Command:     "INIT",
			StatusCode:  403,
			BodyPreview: `{"title":"Forbidden"}`,
		}
	}

	payload := []byte(`{
		"server": "https://misskey.example",
		"body": {
			"note": {
				"id": "note-1",
				"visibility": "public",
				"text": "hello",
				"files": [{"type": "image/png", "url": "https://media.example/a.png"}]
			}
		}
	}`)
	err := Note2TweetHandlerWithConfig(ctx, Config{Notifier: notifier}, payload, crossPostTracker, m)
	if err == nil {
		t.Fatal("Note2TweetHandlerWithConfig() succeeded, want error")
	}
	event := notifier.singleEvent(t)
	if event.Kind != notify.EventTwitterMediaUploadFailed {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventTwitterMediaUploadFailed)
	}
	assertField(t, event, "note_id", "note-1")
	assertField(t, event, "command", "INIT")
	assertField(t, event, "status", "403")
}

func TestTweet2NoteNotifiesMisskeyCreateFailure(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, time.Hour)
	m := metrics.NewNoop()
	notifier := &recordingNotifier{}

	oldCreate := createMisskeyNoteWithOptions
	defer func() { createMisskeyNoteWithOptions = oldCreate }()
	createMisskeyNoteWithOptions = func(ctx context.Context, host, token string, options misskey.CreateNoteOptions) (string, error) {
		return "", &misskey.APIError{
			Operation:   "create note",
			StatusCode:  500,
			BodyPreview: "boom",
		}
	}

	err := HandleIncomingTweetWithConfig(ctx, Config{
		MisskeyHost:     "misskey.example",
		MisskeyToken:    "token",
		TwitterUsername: "user",
		Notifier:        notifier,
	}, IncomingTweet{
		ID:       "tweet-1",
		Text:     "hello",
		Username: "user",
		URL:      "https://twitter.com/user/status/tweet-1",
	}, crossPostTracker, m)
	if err == nil {
		t.Fatal("HandleIncomingTweetWithConfig() succeeded, want error")
	}
	event := notifier.singleEvent(t)
	if event.Kind != notify.EventMisskeyAPIFailed {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventMisskeyAPIFailed)
	}
	assertField(t, event, "tweet_id", "tweet-1")
	assertField(t, event, "status", "500")
}

func TestTweet2NoteNotifiesMisskeyDriveUploadFailure(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, time.Hour)
	m := metrics.NewNoop()
	notifier := &recordingNotifier{}

	oldUpload := uploadMisskeyDriveFileFromURL
	defer func() { uploadMisskeyDriveFileFromURL = oldUpload }()
	uploadMisskeyDriveFileFromURL = func(ctx context.Context, host, token, fileURL string, allowedHosts []string) (string, error) {
		return "", &misskey.APIError{
			Operation:   "upload drive file",
			StatusCode:  502,
			BodyPreview: "bad gateway",
		}
	}

	err := HandleIncomingTweetWithConfig(ctx, Config{
		MisskeyHost:              "misskey.example",
		MisskeyToken:             "token",
		TwitterUsername:          "user",
		TwitterMediaAllowedHosts: []string{"pbs.twimg.com"},
		Notifier:                 notifier,
	}, IncomingTweet{
		ID:        "tweet-1",
		Text:      "hello",
		Username:  "user",
		URL:       "https://twitter.com/user/status/tweet-1",
		MediaURLs: []string{"https://pbs.twimg.com/media/a.png"},
	}, crossPostTracker, m)
	if err == nil {
		t.Fatal("HandleIncomingTweetWithConfig() succeeded, want error")
	}
	event := notifier.singleEvent(t)
	if event.Kind != notify.EventMisskeyAPIFailed {
		t.Fatalf("event.Kind = %q, want %q", event.Kind, notify.EventMisskeyAPIFailed)
	}
	assertField(t, event, "tweet_id", "tweet-1")
	assertField(t, event, "operation", "upload drive file")
	assertField(t, event, "media_index", "0")
	assertField(t, event, "status", "502")
}

func TestNotificationFailureDoesNotReplaceOriginalError(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, time.Hour)
	m := metrics.NewNoop()
	wantErr := errors.New("twitter unavailable")

	oldPost := postTweet
	defer func() { postTweet = oldPost }()
	postTweet = func(ctx context.Context, text string) (string, error) {
		return "", wantErr
	}

	payload := []byte(`{
		"server": "https://misskey.example",
		"body": {
			"note": {
				"id": "note-1",
				"visibility": "public",
				"text": "hello"
			}
		}
	}`)
	err := Note2TweetHandlerWithConfig(ctx, Config{Notifier: failingNotifier{}}, payload, crossPostTracker, m)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Note2TweetHandlerWithConfig() error = %v, want original error", err)
	}
}

type recordingNotifier struct {
	events []notify.Event
}

func (n *recordingNotifier) Notify(ctx context.Context, event notify.Event) error {
	n.events = append(n.events, event)
	return nil
}

func (n *recordingNotifier) singleEvent(t *testing.T) notify.Event {
	t.Helper()
	if len(n.events) != 1 {
		t.Fatalf("events = %d, want 1: %#v", len(n.events), n.events)
	}
	return n.events[0]
}

type failingNotifier struct{}

func (failingNotifier) Notify(ctx context.Context, event notify.Event) error {
	return errors.New("discord unavailable")
}

func assertField(t *testing.T, event notify.Event, name, want string) {
	t.Helper()
	for _, field := range event.Fields {
		if field.Name == name {
			if field.Value != want {
				t.Fatalf("field %q = %q, want %q", name, field.Value, want)
			}
			return
		}
	}
	t.Fatalf("field %q not found in %#v", name, event.Fields)
}

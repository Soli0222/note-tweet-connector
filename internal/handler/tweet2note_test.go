package handler

import (
	"context"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
)

func TestParseAccountActivityPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
		check   func(*testing.T, []IncomingTweet)
	}{
		{
			name: "valid tweet_create_events payload",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "Hello, world!",
						"user": {
							"id_str": "111",
							"screen_name": "dummy_user"
						}
					}
				]
			}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 1 {
					t.Fatalf("expected 1 tweet, got %d", len(tweets))
				}
				if tweets[0].Text != "Hello, world!" {
					t.Errorf("expected text 'Hello, world!', got '%s'", tweets[0].Text)
				}
				if tweets[0].URL != "https://twitter.com/dummy_user/status/123456789" {
					t.Errorf("unexpected URL: %s", tweets[0].URL)
				}
			},
		},
		{
			name: "full_text takes precedence",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "truncated...",
						"full_text": "Full tweet text",
						"user": {
							"id_str": "111",
							"screen_name": "dummy_user"
						}
					}
				]
			}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 1 {
					t.Fatalf("expected 1 tweet, got %d", len(tweets))
				}
				if tweets[0].Text != "Full tweet text" {
					t.Errorf("expected full_text, got '%s'", tweets[0].Text)
				}
			},
		},
		{
			name: "mention from another user is ignored",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "@dummy_user hello",
						"user": {
							"id_str": "222",
							"screen_name": "other_user"
						}
					}
				]
			}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 0 {
					t.Fatalf("expected no tweets, got %d", len(tweets))
				}
			},
		},
		{
			name:    "invalid JSON",
			payload: `{invalid json}`,
			wantErr: true,
		},
		{
			name:    "empty body",
			payload: `{}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 0 {
					t.Fatalf("expected no tweets, got %d", len(tweets))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseAccountActivityPayload([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseAccountActivityPayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestTweet2NoteHandler_SkipConditions(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	t.Setenv("MISSKEY_HOST", "misskey.example")
	t.Setenv("MISSKEY_TOKEN", "test-token")

	payload := `{
		"for_user_id": "111",
		"tweet_create_events": [
			{
				"id_str": "123",
				"text": "RN [at] someone This is a renote",
				"user": {
					"id_str": "111",
					"screen_name": "dummy_user"
				}
			}
		]
	}`

	err := Tweet2NoteHandler(ctx, []byte(payload), contentTracker, m)
	if err != nil {
		t.Errorf("Tweet2NoteHandler() should not return error for RN pattern, got %v", err)
	}
}

func TestTweet2NoteHandler_DuplicateDetection(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	t.Setenv("MISSKEY_HOST", "misskey.example")
	t.Setenv("MISSKEY_TOKEN", "test-token")

	testContent := "Duplicate tweet content for testing"
	contentTracker.MarkProcessed(testContent)

	payload := `{
		"for_user_id": "111",
		"tweet_create_events": [
			{
				"id_str": "222",
				"text": "Duplicate tweet content for testing",
				"user": {
					"id_str": "111",
					"screen_name": "dummy_user"
				}
			}
		]
	}`

	err := Tweet2NoteHandler(ctx, []byte(payload), contentTracker, m)
	if err != nil {
		t.Errorf("Tweet2NoteHandler() should not return error for duplicate, got %v", err)
	}
}

func TestTweet2NoteHandler_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	t.Setenv("MISSKEY_HOST", "misskey.example")
	t.Setenv("MISSKEY_TOKEN", "test-token")

	err := Tweet2NoteHandler(ctx, []byte(`{invalid json}`), contentTracker, m)
	if err == nil {
		t.Error("Tweet2NoteHandler() should return error for invalid JSON")
	}
}

func TestRnAtPattern(t *testing.T) {
	tests := []struct {
		text    string
		matches bool
	}{
		{"RN [at] someone: This is a renote", true},
		{"RN[at]someone no space", true},
		{"RN  [at]someone extra space", true},
		{"rn [at]someone lowercase", false},
		{"Not a renote", false},
		{"Something RN [at]someone in middle", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := rnAtPattern.MatchString(tt.text)
			if result != tt.matches {
				t.Errorf("rnAtPattern.MatchString(%q) = %v, want %v", tt.text, result, tt.matches)
			}
		})
	}
}

func TestBuildTweetURL(t *testing.T) {
	got := buildTweetURL("dummy_user", "123456789")
	want := "https://twitter.com/dummy_user/status/123456789"
	if got != want {
		t.Fatalf("buildTweetURL() = %q, want %q", got, want)
	}
}

func TestParseAccountActivityPayload_UsernameFallback(t *testing.T) {
	t.Setenv("TWITTER_USERNAME", "fallback_user")

	payload := `{
		"for_user_id": "111",
		"tweet_create_events": [
			{
				"id_str": "123456789",
				"text": "Hello, world!",
				"user": {
					"id_str": "111"
				}
			}
		]
	}`

	result, err := parseAccountActivityPayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseAccountActivityPayload() error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 tweet, got %d", len(result))
	}
	if result[0].Username != "fallback_user" {
		t.Fatalf("Username = %q, want fallback_user", result[0].Username)
	}
	if result[0].URL != "https://twitter.com/fallback_user/status/123456789" {
		t.Fatalf("URL = %q", result[0].URL)
	}
}

func TestTweet2NoteHandler_JapaneseContent(t *testing.T) {
	payload := `{
		"for_user_id": "111",
		"tweet_create_events": [
			{
				"id_str": "1234567890123456789",
				"text": "ダミーソング / ダミーアーティスト\n#NowPlaying #Testing\nhttps://t.co/dummylink123",
				"user": {
					"id_str": "111",
					"screen_name": "dummy_user"
				}
			}
		]
	}`

	result, err := parseAccountActivityPayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseAccountActivityPayload() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 tweet, got %d", len(result))
	}
	if result[0].Text != "ダミーソング / ダミーアーティスト\n#NowPlaying #Testing\nhttps://t.co/dummylink123" {
		t.Errorf("Japanese content not parsed correctly: %s", result[0].Text)
	}
}

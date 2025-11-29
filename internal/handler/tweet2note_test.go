package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
)

func TestParseTweetPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
		check   func(*testing.T, *payloadTweetData)
	}{
		{
			name: "valid tweet payload",
			payload: `{
				"body": {
					"tweet": {
						"text": "Hello, world!",
						"url": "https://twitter.com/user/status/123456789"
					}
				}
			}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadTweetData) {
				if p.Body.Tweet.Text != "Hello, world!" {
					t.Errorf("expected text 'Hello, world!', got '%s'", p.Body.Tweet.Text)
				}
				if p.Body.Tweet.Url != "https://twitter.com/user/status/123456789" {
					t.Errorf("expected URL 'https://twitter.com/user/status/123456789', got '%s'", p.Body.Tweet.Url)
				}
			},
		},
		{
			name: "tweet with hashtags - IFTTT format",
			payload: `{
				"body": {
					"tweet": {
						"text": "Dummy Song / Dummy Artist\n#NowPlaying #Testing\nhttps://t.co/dummylink123",
						"url": "https://twitter.com/dummy_user/status/1234567890123456789"
					}
				}
			}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadTweetData) {
				if p.Body.Tweet.Text != "Dummy Song / Dummy Artist\n#NowPlaying #Testing\nhttps://t.co/dummylink123" {
					t.Errorf("unexpected text: %s", p.Body.Tweet.Text)
				}
				if p.Body.Tweet.Url != "https://twitter.com/dummy_user/status/1234567890123456789" {
					t.Errorf("unexpected URL: %s", p.Body.Tweet.Url)
				}
			},
		},
		{
			name:    "invalid JSON",
			payload: `{invalid json}`,
			wantErr: true,
			check:   nil,
		},
		{
			name:    "empty body",
			payload: `{}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadTweetData) {
				if p.Body.Tweet.Text != "" {
					t.Errorf("expected empty text, got '%s'", p.Body.Tweet.Text)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTweetPayload([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTweetPayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil && result != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestTweet2NoteHandler_SkipConditions(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	// Set required environment variable for testing
	t.Setenv("MISSKEY_HOST", "misskey.example")
	t.Setenv("MISSKEY_TOKEN", "test-token")

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name: "skip RN [at] pattern",
			payload: `{
				"body": {
					"tweet": {
						"text": "RN [at] someone This is a renote",
						"url": "https://twitter.com/user/status/123"
					}
				}
			}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Tweet2NoteHandler(ctx, []byte(tt.payload), contentTracker, m)
			if (err != nil) != tt.wantErr {
				t.Errorf("Tweet2NoteHandler() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTweet2NoteHandler_DuplicateDetection(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	// Set required environment variables
	t.Setenv("MISSKEY_HOST", "misskey.example")
	t.Setenv("MISSKEY_TOKEN", "test-token")

	payload1 := `{
		"body": {
			"tweet": {
				"text": "Duplicate tweet content for testing",
				"url": "https://twitter.com/user/status/111"
			}
		}
	}`

	payload2 := `{
		"body": {
			"tweet": {
				"text": "Duplicate tweet content for testing",
				"url": "https://twitter.com/user/status/222"
			}
		}
	}`

	// First call - will fail at Misskey posting but content tracked
	_ = Tweet2NoteHandler(ctx, []byte(payload1), contentTracker, m)

	// Second call should detect duplicate and skip
	err := Tweet2NoteHandler(ctx, []byte(payload2), contentTracker, m)
	if err != nil {
		t.Errorf("Tweet2NoteHandler() should not return error for duplicate, got %v", err)
	}
}

func TestTweet2NoteHandler_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	// Set required environment variables
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

func TestPayloadTweetDataJSON(t *testing.T) {
	original := &payloadTweetData{}
	original.Body.Tweet.Text = "test tweet"
	original.Body.Tweet.Url = "https://twitter.com/user/status/123"

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed payloadTweetData
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if parsed.Body.Tweet.Text != original.Body.Tweet.Text {
		t.Errorf("Tweet.Text mismatch: got %s, want %s", parsed.Body.Tweet.Text, original.Body.Tweet.Text)
	}
	if parsed.Body.Tweet.Url != original.Body.Tweet.Url {
		t.Errorf("Tweet.Url mismatch: got %s, want %s", parsed.Body.Tweet.Url, original.Body.Tweet.Url)
	}
}

func TestTweetURLExtraction(t *testing.T) {
	payload := `{
		"body": {
			"tweet": {
				"text": "Check this out https://t.co/abc123",
				"url": "https://twitter.com/user/status/123456"
			}
		}
	}`

	result, err := parseTweetPayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseTweetPayload() error = %v", err)
	}

	if result.Body.Tweet.Url != "https://twitter.com/user/status/123456" {
		t.Errorf("expected URL 'https://twitter.com/user/status/123456', got '%s'", result.Body.Tweet.Url)
	}
}

func TestTweet2NoteHandler_JapaneseContent(t *testing.T) {
	payload := `{
		"body": {
			"tweet": {
				"text": "ダミーソング / ダミーアーティスト\n#NowPlaying #Testing\nhttps://t.co/dummylink123",
				"url": "https://twitter.com/dummy_user/status/1234567890123456789"
			}
		}
	}`

	result, err := parseTweetPayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseTweetPayload() error = %v", err)
	}

	// Verify Japanese content is parsed correctly
	if result.Body.Tweet.Text != "ダミーソング / ダミーアーティスト\n#NowPlaying #Testing\nhttps://t.co/dummylink123" {
		t.Errorf("Japanese content not parsed correctly: %s", result.Body.Tweet.Text)
	}
}

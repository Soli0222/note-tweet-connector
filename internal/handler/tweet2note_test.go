package handler

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/misskey"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func testHandlerConfig() Config {
	return Config{
		MisskeyHost:              "misskey.example",
		MisskeyToken:             "test-token",
		TwitterUsername:          "fallback_user",
		TwitterMediaAllowedHosts: []string{"pbs.twimg.com", "video.twimg.com"},
	}
}

func TestParseFilteredStreamPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
		check   func(*testing.T, []IncomingTweet)
	}{
		{
			name: "valid stream payload",
			payload: `{
					"data": {
						"id": "123456789",
						"text": "Hello, world!",
						"author_id": "111"
					},
					"includes": {
						"users": [
							{
								"id": "111",
								"username": "dummy_user"
							}
						]
					}
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
			name: "extract photo media URLs",
			payload: `{
					"data": {
						"id": "123456789",
						"text": "with media",
						"author_id": "111",
						"attachments": {
							"media_keys": ["photo-1", "video-1", "photo-2", "photo-2"]
						}
					},
					"includes": {
						"media": [
							{
								"media_key": "photo-1",
								"type": "photo",
								"url": "https://pbs.twimg.com/media/photo1.png"
							},
							{
								"media_key": "video-1",
								"type": "video",
								"preview_image_url": "https://pbs.twimg.com/media/video.jpg"
							},
							{
								"media_key": "photo-2",
								"type": "photo",
								"url": "https://pbs.twimg.com/media/photo2.png"
							}
						],
						"users": [
							{
								"id": "111",
								"username": "dummy_user"
							}
						]
					}
				}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 1 {
					t.Fatalf("expected 1 tweet, got %d", len(tweets))
				}
				want := []string{
					"https://pbs.twimg.com/media/photo1.png",
					"https://pbs.twimg.com/media/photo2.png",
				}
				if !reflect.DeepEqual(tweets[0].MediaURLs, want) {
					t.Fatalf("MediaURLs = %#v, want %#v", tweets[0].MediaURLs, want)
				}
			},
		},
		{
			name: "keeps media only tweet",
			payload: `{
					"data": {
						"id": "123456789",
						"text": "",
						"author_id": "111",
						"attachments": {
							"media_keys": ["photo-1"]
						}
					},
					"includes": {
						"media": [
							{
								"media_key": "photo-1",
								"type": "photo",
								"url": "https://pbs.twimg.com/media/photo1.png"
							}
						]
					}
				}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 1 {
					t.Fatalf("expected 1 tweet, got %d", len(tweets))
				}
				if tweets[0].Text != "" {
					t.Fatalf("Text = %q, want empty", tweets[0].Text)
				}
				want := []string{"https://pbs.twimg.com/media/photo1.png"}
				if !reflect.DeepEqual(tweets[0].MediaURLs, want) {
					t.Fatalf("MediaURLs = %#v, want %#v", tweets[0].MediaURLs, want)
				}
			},
		},
		{
			name: "extract quote tweet metadata",
			payload: `{
					"data": {
						"id": "123456789",
						"text": "quote text https://t.co/source",
						"author_id": "111",
						"referenced_tweets": [
							{
								"type": "quoted",
								"id": "987654321"
							}
						]
					},
					"includes": {
						"tweets": [
							{
								"id": "987654321",
								"text": "source text",
								"author_id": "111"
							}
						],
						"users": [
							{
								"id": "111",
								"username": "dummy_user"
							}
						]
					}
				}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 1 {
					t.Fatalf("expected 1 tweet, got %d", len(tweets))
				}
				if tweets[0].QuotedTweetID != "987654321" {
					t.Fatalf("QuotedTweetID = %q, want 987654321", tweets[0].QuotedTweetID)
				}
				if tweets[0].QuotedUserID != "111" {
					t.Fatalf("QuotedUserID = %q, want 111", tweets[0].QuotedUserID)
				}
				if tweets[0].QuotedUsername != "dummy_user" {
					t.Fatalf("QuotedUsername = %q, want dummy_user", tweets[0].QuotedUsername)
				}
			},
		},
		{
			name: "extract reply metadata",
			payload: `{
					"data": {
						"id": "123456789",
						"text": "reply text",
						"author_id": "111",
						"referenced_tweets": [
							{
								"type": "replied_to",
								"id": "987654321"
							}
						]
					}
				}`,
			check: func(t *testing.T, tweets []IncomingTweet) {
				if len(tweets) != 1 {
					t.Fatalf("expected 1 tweet, got %d", len(tweets))
				}
				if tweets[0].InReplyToTweetID != "987654321" {
					t.Fatalf("InReplyToTweetID = %q, want 987654321", tweets[0].InReplyToTweetID)
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
			result, err := parseFilteredStreamPayload([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFilteredStreamPayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestHandleIncomingTweet_WithMedia(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	oldCreate := createMisskeyNoteWithOptions
	oldUpload := uploadMisskeyDriveFileFromURL
	defer func() {
		createMisskeyNoteWithOptions = oldCreate
		uploadMisskeyDriveFileFromURL = oldUpload
	}()

	var uploadedURLs []string
	uploadMisskeyDriveFileFromURL = func(ctx context.Context, host, token, fileURL string, allowedHosts []string) (string, error) {
		if host != "misskey.example" || token != "test-token" {
			t.Fatalf("unexpected upload auth host=%q token=%q", host, token)
		}
		if !reflect.DeepEqual(allowedHosts, []string{"pbs.twimg.com", "video.twimg.com"}) {
			t.Fatalf("allowedHosts = %#v", allowedHosts)
		}
		uploadedURLs = append(uploadedURLs, fileURL)
		return "file-" + string(rune('0'+len(uploadedURLs))), nil
	}

	var gotText string
	var gotFileIDs []string
	createMisskeyNoteWithOptions = func(ctx context.Context, host, token string, options misskey.CreateNoteOptions) (string, error) {
		if host != "misskey.example" || token != "test-token" {
			t.Fatalf("unexpected create auth host=%q token=%q", host, token)
		}
		gotText = options.Text
		gotFileIDs = append([]string(nil), options.FileIDs...)
		return "note-123", nil
	}

	tweet := IncomingTweet{
		ID:        "123",
		Text:      "tweet with media",
		Username:  "dummy_user",
		URL:       "https://twitter.com/dummy_user/status/123",
		MediaURLs: []string{"https://pbs.twimg.com/media/1.png", "https://pbs.twimg.com/media/2.png"},
	}

	if err := HandleIncomingTweetWithConfig(ctx, testHandlerConfig(), tweet, crossPostTracker, m); err != nil {
		t.Fatalf("HandleIncomingTweet() error = %v", err)
	}
	if !reflect.DeepEqual(uploadedURLs, tweet.MediaURLs) {
		t.Fatalf("uploadedURLs = %#v, want %#v", uploadedURLs, tweet.MediaURLs)
	}
	if gotText != "tweet with media" {
		t.Fatalf("gotText = %q", gotText)
	}
	wantFileIDs := []string{"file-1", "file-2"}
	if !reflect.DeepEqual(gotFileIDs, wantFileIDs) {
		t.Fatalf("fileIDs = %#v, want %#v", gotFileIDs, wantFileIDs)
	}
	if ok, err := crossPostTracker.HasTweet(ctx, "123"); err != nil || !ok {
		t.Fatal("tweet ID was not recorded")
	}
	if ok, err := crossPostTracker.HasMisskeyNote(ctx, "note-123"); err != nil || !ok {
		t.Fatal("note ID was not recorded")
	}
}

func TestHandleIncomingTweet_QuoteTweetUsesTrackerNoteID(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	if err := crossPostTracker.RememberMisskeyToTweet(ctx, "source-note", "source-tweet"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	m := metrics.NewNoop()

	oldCreate := createMisskeyNoteWithOptions
	defer func() { createMisskeyNoteWithOptions = oldCreate }()

	var gotOptions misskey.CreateNoteOptions
	createMisskeyNoteWithOptions = func(ctx context.Context, host, token string, options misskey.CreateNoteOptions) (string, error) {
		if host != "misskey.example" || token != "test-token" {
			t.Fatalf("unexpected create auth host=%q token=%q", host, token)
		}
		gotOptions = options
		return "quote-note", nil
	}

	tweet := IncomingTweet{
		ID:             "quote-tweet",
		Text:           "my quote text https://t.co/source",
		UserID:         "user-1",
		Username:       "dummy_user",
		URL:            "https://twitter.com/dummy_user/status/quote-tweet",
		QuotedTweetID:  "source-tweet",
		QuotedUserID:   "user-1",
		QuotedUsername: "dummy_user",
	}

	if err := HandleIncomingTweetWithConfig(ctx, testHandlerConfig(), tweet, crossPostTracker, m); err != nil {
		t.Fatalf("HandleIncomingTweet() error = %v", err)
	}
	if gotOptions.Text != tweet.Text {
		t.Fatalf("Text = %q, want %q", gotOptions.Text, tweet.Text)
	}
	if gotOptions.RenoteID != "source-note" {
		t.Fatalf("RenoteID = %q, want source-note", gotOptions.RenoteID)
	}
	hasTweet, err := crossPostTracker.HasTweet(ctx, "quote-tweet")
	if err != nil {
		t.Fatalf("HasTweet() error = %v", err)
	}
	hasNote, err := crossPostTracker.HasMisskeyNote(ctx, "quote-note")
	if err != nil {
		t.Fatalf("HasMisskeyNote() error = %v", err)
	}
	if !hasTweet || !hasNote {
		t.Fatal("quote cross-post IDs were not recorded")
	}
}

func TestHandleIncomingTweet_QuoteTweetFallsBackWhenTrackerMiss(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	oldCreate := createMisskeyNoteWithOptions
	defer func() { createMisskeyNoteWithOptions = oldCreate }()

	var gotOptions misskey.CreateNoteOptions
	createMisskeyNoteWithOptions = func(ctx context.Context, host, token string, options misskey.CreateNoteOptions) (string, error) {
		gotOptions = options
		return "fallback-note", nil
	}

	tweet := IncomingTweet{
		ID:             "quote-tweet",
		Text:           "my quote text https://t.co/source",
		UserID:         "user-1",
		Username:       "dummy_user",
		URL:            "https://twitter.com/dummy_user/status/quote-tweet",
		QuotedTweetID:  "missing-source-tweet",
		QuotedUserID:   "user-1",
		QuotedUsername: "dummy_user",
	}

	if err := HandleIncomingTweetWithConfig(ctx, testHandlerConfig(), tweet, crossPostTracker, m); err != nil {
		t.Fatalf("HandleIncomingTweet() error = %v", err)
	}
	if gotOptions.RenoteID != "" {
		t.Fatalf("RenoteID = %q, want empty fallback", gotOptions.RenoteID)
	}
	if gotOptions.Text != tweet.Text {
		t.Fatalf("Text = %q, want %q", gotOptions.Text, tweet.Text)
	}
}

func TestTweet2NoteHandler_SkipConditions(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	payload := `{
		"data": {
			"id": "123",
			"text": "RN [at] someone This is a renote",
			"author_id": "111"
		},
		"includes": {
			"users": [
				{
					"id": "111",
					"username": "dummy_user"
				}
			]
		}
	}`

	err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(payload), crossPostTracker, m)
	if err != nil {
		t.Errorf("Tweet2NoteHandler() should not return error for RN pattern, got %v", err)
	}
}

func TestTweet2NoteHandler_KnownCrossPostDetection(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	if err := crossPostTracker.RememberMisskeyToTweet(ctx, "note-222", "222"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}

	payload := `{
		"data": {
			"id": "222",
			"text": "Duplicate tweet content for testing",
			"author_id": "111"
		},
		"includes": {
			"users": [
				{
					"id": "111",
					"username": "dummy_user"
				}
			]
		}
	}`

	err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(payload), crossPostTracker, m)
	if err != nil {
		t.Errorf("Tweet2NoteHandler() should not return error for known cross-post, got %v", err)
	}
}

func TestTweet2NoteHandler_SkipsReply(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	oldCreate := createMisskeyNoteWithOptions
	defer func() { createMisskeyNoteWithOptions = oldCreate }()
	createMisskeyNoteWithOptions = func(ctx context.Context, host, token string, options misskey.CreateNoteOptions) (string, error) {
		t.Fatal("CreateNoteWithOptions should not be called for a reply tweet")
		return "", nil
	}

	payload := `{
		"data": {
			"id": "333",
			"text": "Reply tweet",
			"author_id": "111",
			"referenced_tweets": [
				{
					"type": "replied_to",
					"id": "222"
				}
			]
		},
		"includes": {
			"users": [
				{
					"id": "111",
					"username": "dummy_user"
				}
			]
		}
	}`

	if err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(payload), crossPostTracker, m); err != nil {
		t.Fatalf("Tweet2NoteHandler() error = %v", err)
	}
	if got := testutil.ToFloat64(m.Tweet2NoteSkipped.WithLabelValues("reply")); got != 1 {
		t.Fatalf("reply skipped metric = %v, want 1", got)
	}
}

func TestTweet2NoteHandler_NoEligibleTweets(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	payload := `{}`

	err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(payload), crossPostTracker, m)
	if err != nil {
		t.Fatalf("Tweet2NoteHandler() error = %v", err)
	}
	if got := testutil.ToFloat64(m.Tweet2NoteSkipped.WithLabelValues("no_eligible_tweets")); got != 1 {
		t.Fatalf("no_eligible_tweets skipped metric = %v, want 1", got)
	}
}

func TestTweet2NoteHandler_SkipsMissingTweetID(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	oldCreate := createMisskeyNoteWithOptions
	defer func() { createMisskeyNoteWithOptions = oldCreate }()
	createMisskeyNoteWithOptions = func(ctx context.Context, host, token string, options misskey.CreateNoteOptions) (string, error) {
		t.Fatal("CreateNoteWithOptions should not be called when tweet ID is missing")
		return "", nil
	}

	payload := `{
		"data": {
			"text": "Tweet without ID",
			"author_id": "111"
		}
	}`

	if err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(payload), crossPostTracker, m); err != nil {
		t.Fatalf("Tweet2NoteHandler() error = %v", err)
	}
}

func TestTweet2NoteHandler_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(`{invalid json}`), crossPostTracker, m)
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

func TestParseFilteredStreamPayload_UsernameFallback(t *testing.T) {
	payload := `{
		"data": {
			"id": "123456789",
			"text": "Hello, world!",
			"author_id": "111"
		}
	}`

	result, err := parseFilteredStreamPayloadWithConfig([]byte(payload), testHandlerConfig())
	if err != nil {
		t.Fatalf("parseFilteredStreamPayload() error = %v", err)
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
		"data": {
			"id": "1234567890123456789",
			"text": "ダミーソング / ダミーアーティスト\n#NowPlaying #Testing\nhttps://t.co/dummylink123",
			"author_id": "111"
		},
		"includes": {
			"users": [
				{
					"id": "111",
					"username": "dummy_user"
				}
			]
		}
	}`

	result, err := parseFilteredStreamPayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseFilteredStreamPayload() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 tweet, got %d", len(result))
	}
	if result[0].Text != "ダミーソング / ダミーアーティスト\n#NowPlaying #Testing\nhttps://t.co/dummylink123" {
		t.Errorf("Japanese content not parsed correctly: %s", result[0].Text)
	}
}

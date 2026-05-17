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
			name: "extended_tweet full_text takes precedence",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "truncated...",
						"full_text": "legacy full text",
						"extended_tweet": {
							"full_text": "extended full text"
						},
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
				if tweets[0].Text != "extended full text" {
					t.Errorf("expected extended_tweet.full_text, got %q", tweets[0].Text)
				}
			},
		},
		{
			name: "extract photo media URLs",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "with media",
						"extended_entities": {
							"media": [
								{
									"type": "photo",
									"media_url_https": "https://pbs.twimg.com/media/photo1.png"
								},
								{
									"type": "video",
									"media_url_https": "https://video.twimg.com/video.mp4"
								}
							]
						},
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
				want := []string{"https://pbs.twimg.com/media/photo1.png"}
				if !reflect.DeepEqual(tweets[0].MediaURLs, want) {
					t.Fatalf("MediaURLs = %#v, want %#v", tweets[0].MediaURLs, want)
				}
			},
		},
		{
			name: "extended_tweet media takes precedence and deduplicates",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "with media",
						"entities": {
							"media": [
								{
									"type": "photo",
									"media_url_https": "https://pbs.twimg.com/media/photo2.png"
								}
							]
						},
						"extended_tweet": {
							"full_text": "with media full text",
							"extended_entities": {
								"media": [
									{
										"type": "photo",
										"media_url_https": "https://pbs.twimg.com/media/photo1.png"
									},
									{
										"type": "photo",
										"media_url_https": "https://pbs.twimg.com/media/photo2.png"
									}
								]
							}
						},
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
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "",
						"extended_entities": {
							"media": [
								{
									"type": "photo",
									"media_url_https": "https://pbs.twimg.com/media/photo1.png"
								}
							]
						},
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
			name: "extract quote tweet metadata",
			payload: `{
				"for_user_id": "111",
				"tweet_create_events": [
					{
						"id_str": "123456789",
						"text": "quote text https://t.co/source",
						"user": {
							"id_str": "111",
							"screen_name": "dummy_user"
						},
						"quoted_status_id_str": "987654321",
						"quoted_status": {
							"id_str": "987654321",
							"text": "source text",
							"user": {
								"id_str": "111",
								"screen_name": "dummy_user"
							}
						}
					}
				]
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

	err := Tweet2NoteHandlerWithConfig(ctx, testHandlerConfig(), []byte(payload), crossPostTracker, m)
	if err != nil {
		t.Errorf("Tweet2NoteHandler() should not return error for known cross-post, got %v", err)
	}
}

func TestTweet2NoteHandler_NoEligibleTweets(t *testing.T) {
	ctx := context.Background()
	crossPostTracker := tracker.NewCrossPostTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	payload := `{
		"for_user_id": "111",
		"tweet_create_events": []
	}`

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
		"for_user_id": "111",
		"tweet_create_events": [
			{
				"text": "Tweet without ID",
				"user": {
					"id_str": "111",
					"screen_name": "dummy_user"
				}
			}
		]
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

func TestParseAccountActivityPayload_UsernameFallback(t *testing.T) {
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

	result, err := parseAccountActivityPayloadWithConfig([]byte(payload), testHandlerConfig())
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

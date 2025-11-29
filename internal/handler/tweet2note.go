package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/misskey"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
)

type payloadTweetData struct {
	Body struct {
		Tweet struct {
			Text string `json:"text"`
			Url  string `json:"url"`
		} `json:"tweet"`
	} `json:"body"`
}

// RNとat記号の検出用正規表現
var rnAtPattern = regexp.MustCompile(`^RN\s*\[at\]`)

func Tweet2NoteHandler(ctx context.Context, data []byte, contentTracker *tracker.ContentTracker, m *metrics.Metrics) error {
	m.Tweet2NoteTotal.Inc()

	payload, err := parseTweetPayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}

	tweetText := payload.Body.Tweet.Text

	if rtAtPattern.MatchString(tweetText) {
		tweetText = tweetText + "\n\n" + payload.Body.Tweet.Url
	}

	// "RN [at]" で始まるツイートをスキップ
	if rnAtPattern.MatchString(tweetText) {
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Skipping RN [at] tweet",
			slog.String("text_preview", escapedText[:min(50, len(escapedText))]))
		m.Tweet2NoteSkipped.WithLabelValues("rn_pattern").Inc()
		return nil
	}

	misskeyHost := os.Getenv("MISSKEY_HOST")
	if misskeyHost == "" {
		slog.Error("MISSKEY_HOST is not set")
		m.Tweet2NoteErrors.Inc()
		return fmt.Errorf("MISSKEY_HOST environment variable is not set")
	}

	misskeyToken := os.Getenv("MISSKEY_TOKEN")
	if misskeyToken == "" {
		slog.Error("MISSKEY_TOKEN is not set")
		m.Tweet2NoteErrors.Inc()
		return fmt.Errorf("MISSKEY_TOKEN environment variable is not set")
	}

	// Atomically check and mark as processed to prevent race conditions
	if !contentTracker.MarkProcessedIfNotExists(tweetText) {
		slog.Info("Tweet already processed, skipping")
		m.Tweet2NoteSkipped.WithLabelValues("duplicate").Inc()
		m.TrackerDuplicatesHit.Inc()
		return nil
	}

	err = misskey.CreateNote(ctx, misskeyHost, misskeyToken, tweetText)

	if err == nil {
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Successfully forwarded tweet to note",
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.String("tweet_url", payload.Body.Tweet.Url))
		m.Tweet2NoteSuccess.Inc()
	} else {
		slog.Error("Failed to post tweet to note", slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}

	return nil
}

func parseTweetPayload(data []byte) (*payloadTweetData, error) {
	var payload payloadTweetData
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

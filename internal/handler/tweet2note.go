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

type IncomingTweet struct {
	ID       string
	Text     string
	Username string
	URL      string
}

type accountActivityPayload struct {
	ForUserID         string        `json:"for_user_id"`
	TweetCreateEvents []tweetObject `json:"tweet_create_events"`
}

type tweetObject struct {
	IDStr    string      `json:"id_str"`
	Text     string      `json:"text"`
	FullText string      `json:"full_text"`
	User     twitterUser `json:"user"`
}

type twitterUser struct {
	IDStr      string `json:"id_str"`
	ScreenName string `json:"screen_name"`
}

// RNとat記号の検出用正規表現
var rnAtPattern = regexp.MustCompile(`^RN\s*\[at\]`)

func Tweet2NoteHandler(ctx context.Context, data []byte, contentTracker *tracker.ContentTracker, m *metrics.Metrics) error {
	m.Tweet2NoteTotal.Inc()

	tweets, err := parseAccountActivityPayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}

	for _, tweet := range tweets {
		if err := HandleIncomingTweet(ctx, tweet, contentTracker, m); err != nil {
			return err
		}
	}

	return nil
}

func HandleIncomingTweet(ctx context.Context, tweet IncomingTweet, contentTracker *tracker.ContentTracker, m *metrics.Metrics) error {
	tweetText := tweet.Text

	if rtAtPattern.MatchString(tweetText) {
		tweetText = tweetText + "\n\n" + tweet.URL
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

	err := misskey.CreateNote(ctx, misskeyHost, misskeyToken, tweetText)

	if err == nil {
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Successfully forwarded tweet to note",
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.String("tweet_url", tweet.URL))
		m.Tweet2NoteSuccess.Inc()
	} else {
		slog.Error("Failed to post tweet to note", slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}

	return nil
}

func parseAccountActivityPayload(data []byte) ([]IncomingTweet, error) {
	var payload accountActivityPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	tweets := make([]IncomingTweet, 0, len(payload.TweetCreateEvents))
	for _, event := range payload.TweetCreateEvents {
		if payload.ForUserID != "" && event.User.IDStr != "" && event.User.IDStr != payload.ForUserID {
			continue
		}

		text := event.FullText
		if text == "" {
			text = event.Text
		}
		if text == "" {
			continue
		}

		tweetID := event.IDStr
		username := event.User.ScreenName
		if username == "" {
			username = os.Getenv("TWITTER_USERNAME")
		}
		tweets = append(tweets, IncomingTweet{
			ID:       tweetID,
			Text:     text,
			Username: username,
			URL:      buildTweetURL(username, tweetID),
		})
	}

	return tweets, nil
}

func buildTweetURL(username, tweetID string) string {
	if username == "" || tweetID == "" {
		return ""
	}
	return "https://twitter.com/" + username + "/status/" + tweetID
}

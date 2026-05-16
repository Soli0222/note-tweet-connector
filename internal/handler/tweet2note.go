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
	ID        string
	Text      string
	Username  string
	URL       string
	MediaURLs []string
}

type accountActivityPayload struct {
	ForUserID         string        `json:"for_user_id"`
	TweetCreateEvents []tweetObject `json:"tweet_create_events"`
}

type tweetObject struct {
	IDStr            string          `json:"id_str"`
	Text             string          `json:"text"`
	FullText         string          `json:"full_text"`
	Truncated        bool            `json:"truncated"`
	User             twitterUser     `json:"user"`
	Entities         twitterEntities `json:"entities"`
	ExtendedEntities twitterEntities `json:"extended_entities"`
	ExtendedTweet    extendedTweet   `json:"extended_tweet"`
}

type extendedTweet struct {
	FullText         string          `json:"full_text"`
	Entities         twitterEntities `json:"entities"`
	ExtendedEntities twitterEntities `json:"extended_entities"`
}

type twitterEntities struct {
	Media []twitterMedia `json:"media"`
}

type twitterMedia struct {
	Type          string `json:"type"`
	MediaURLHTTPS string `json:"media_url_https"`
	MediaURL      string `json:"media_url"`
}

type twitterUser struct {
	IDStr      string `json:"id_str"`
	ScreenName string `json:"screen_name"`
}

// RNとat記号の検出用正規表現
var rnAtPattern = regexp.MustCompile(`^RN\s*\[at\]`)

var createMisskeyNoteWithFiles = misskey.CreateNoteWithFiles
var uploadMisskeyDriveFileFromURL = misskey.UploadDriveFileFromURL

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

	trackerKey := tweetText
	if trackerKey == "" {
		trackerKey = tweet.URL
	}
	if trackerKey == "" {
		trackerKey = tweet.ID
	}

	// Atomically check and mark as processed to prevent race conditions
	if !contentTracker.MarkProcessedIfNotExists(trackerKey) {
		slog.Info("Tweet already processed, skipping")
		m.Tweet2NoteSkipped.WithLabelValues("duplicate").Inc()
		m.TrackerDuplicatesHit.Inc()
		return nil
	}

	fileIDs := make([]string, 0, min(len(tweet.MediaURLs), 4))
	for i := 0; i < len(tweet.MediaURLs) && i < 4; i++ {
		fileID, err := uploadMisskeyDriveFileFromURL(ctx, misskeyHost, misskeyToken, tweet.MediaURLs[i])
		if err != nil {
			slog.Error("Failed to upload tweet media to Misskey Drive",
				slog.String("media_url", tweet.MediaURLs[i]),
				slog.Any("error", err))
			m.Tweet2NoteErrors.Inc()
			return err
		}
		fileIDs = append(fileIDs, fileID)
	}

	err := createMisskeyNoteWithFiles(ctx, misskeyHost, misskeyToken, tweetText, fileIDs)

	if err == nil {
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Successfully forwarded tweet to note",
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.String("tweet_url", tweet.URL),
			slog.Bool("has_media", len(fileIDs) > 0),
			slog.Int("media_count", len(fileIDs)))
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

		text := tweetText(event)
		mediaURLs := tweetMediaURLs(event)
		if text == "" && len(mediaURLs) == 0 {
			continue
		}

		tweetID := event.IDStr
		username := event.User.ScreenName
		if username == "" {
			username = os.Getenv("TWITTER_USERNAME")
		}
		tweets = append(tweets, IncomingTweet{
			ID:        tweetID,
			Text:      text,
			Username:  username,
			URL:       buildTweetURL(username, tweetID),
			MediaURLs: mediaURLs,
		})
	}

	return tweets, nil
}

func tweetText(event tweetObject) string {
	if event.ExtendedTweet.FullText != "" {
		return event.ExtendedTweet.FullText
	}
	if event.FullText != "" {
		return event.FullText
	}
	return event.Text
}

func tweetMediaURLs(event tweetObject) []string {
	seen := map[string]struct{}{}
	mediaURLs := make([]string, 0, 4)

	collect := func(mediaItems []twitterMedia) {
		for _, media := range mediaItems {
			if media.Type != "photo" {
				continue
			}
			mediaURL := media.MediaURLHTTPS
			if mediaURL == "" {
				mediaURL = media.MediaURL
			}
			if mediaURL == "" {
				continue
			}
			if _, ok := seen[mediaURL]; ok {
				continue
			}
			seen[mediaURL] = struct{}{}
			mediaURLs = append(mediaURLs, mediaURL)
		}
	}

	collect(event.ExtendedTweet.ExtendedEntities.Media)
	collect(event.ExtendedTweet.Entities.Media)
	collect(event.ExtendedEntities.Media)
	collect(event.Entities.Media)

	return mediaURLs
}

func buildTweetURL(username, tweetID string) string {
	if username == "" || tweetID == "" {
		return ""
	}
	return "https://twitter.com/" + username + "/status/" + tweetID
}

package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
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

func Tweet2NoteHandler(data []byte, tracker *ContentTracker) error {
	payload, err := parseTweetPayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
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
		return nil
	}

	// Check if this content has already been processed
	if tracker.IsProcessed(tweetText) {
		slog.Info("Tweet already processed, skipping")
		return nil
	}

	MISSKEY_HOST := os.Getenv("MISSKEY_HOST")
	if MISSKEY_HOST == "" {
		slog.Error("MISSKEY_HOST is not set")
		return nil
	}

	MISSKEY_TOKEN := os.Getenv("MISSKEY_TOKEN")
	if MISSKEY_TOKEN == "" {
		slog.Error("MISSKEY_TOKEN is not set")
		return nil
	}

	err = Note(MISSKEY_HOST, MISSKEY_TOKEN, tweetText)

	if err == nil {
		// Mark as processed only if posting was successful
		tracker.MarkProcessed(tweetText)
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Successfully forwarded tweet to note",
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.String("tweet_url", payload.Body.Tweet.Url))
	} else {
		slog.Error("Failed to post tweet to note", slog.Any("error", err))
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

func Note(host, token, text string) error {

	endpoint := "https://" + host + "/api/notes/create"

	jsonData := map[string]interface{}{
		"i":    token,
		"text": text,
	}

	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		slog.Error("Failed to marshal json", slog.Any("error", err))
		return err
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		slog.Error("Failed to create request", slog.Any("error", err))
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Failed to send request", slog.Any("error", err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Failed to send request",
			slog.Int("status_code", resp.StatusCode),
			slog.String("status", resp.Status),
			slog.String("endpoint", endpoint))
		return fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	slog.Debug("Successfully posted note to Misskey",
		slog.String("endpoint", endpoint),
		slog.Int("status_code", resp.StatusCode))

	return nil
}

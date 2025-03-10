package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
)

type payloadTweetData struct {
	Body struct {
		Tweet struct {
			Text string `json:"text"`
			Url  string `json:"url"`
		} `json:"tweet"`
	} `json:"body"`
}

func Tweet2NoteHandler(data []byte, tracker *ContentTracker) error {
	payload, err := parseTweetPayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		return err
	}

	tweetText := payload.Body.Tweet.Text

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
	} else {
		slog.Error("Failed to post tweet to note", slog.Any("error", err))
		return err
	}

	slog.Info("Successfully forwarded tweet to note")

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
		slog.Error("Failed to send request", slog.Any("status", resp.Status))
		return err
	}

	return nil
}

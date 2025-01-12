package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
)

func Tweet2NoteHandler(data []byte) error {
	datastr := string(data)

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

	err := Note(MISSKEY_HOST, MISSKEY_TOKEN, datastr)
	if err != nil {
		slog.Error("Failed to note", slog.Any("error", err))
		return err
	}

	slog.Info("Success Tweet to Note")

	return nil
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

package misskey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// httpClient is a reusable HTTP client with timeout
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// CreateNote creates a new note on Misskey
func CreateNote(ctx context.Context, host, token, text string) error {
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

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		slog.Error("Failed to create request", slog.Any("error", err))
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Error("Failed to send request", slog.Any("error", err))
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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

package misskey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// httpClient is a reusable HTTP client with timeout
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

type CreateNoteOptions struct {
	Text     string
	FileIDs  []string
	RenoteID string
}

// CreateNote creates a new note on Misskey
func CreateNote(ctx context.Context, host, token, text string) (string, error) {
	return CreateNoteWithOptions(ctx, host, token, CreateNoteOptions{Text: text})
}

// CreateNoteWithFiles creates a new note on Misskey with optional file attachments.
func CreateNoteWithFiles(ctx context.Context, host, token, text string, fileIDs []string) (string, error) {
	return CreateNoteWithOptions(ctx, host, token, CreateNoteOptions{Text: text, FileIDs: fileIDs})
}

func CreateNoteWithOptions(ctx context.Context, host, token string, options CreateNoteOptions) (string, error) {
	endpoint := "https://" + host + "/api/notes/create"

	jsonData := map[string]interface{}{}
	if options.Text != "" {
		jsonData["text"] = options.Text
	}
	if len(options.FileIDs) > 0 {
		jsonData["fileIds"] = options.FileIDs
	}
	if options.RenoteID != "" {
		jsonData["renoteId"] = options.RenoteID
	}

	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		slog.Error("Failed to marshal json", slog.Any("error", err))
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		slog.Error("Failed to create request", slog.Any("error", err))
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Error("Failed to send request", slog.Any("error", err))
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("Failed to send request",
			slog.Int("status_code", resp.StatusCode),
			slog.String("status", resp.Status),
			slog.String("endpoint", endpoint))
		return "", fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	var createResp struct {
		CreatedNote struct {
			ID string `json:"id"`
		} `json:"createdNote"`
	}
	if err := json.Unmarshal(respBytes, &createResp); err != nil {
		return "", fmt.Errorf("failed to parse note create response: %w", err)
	}
	if createResp.CreatedNote.ID == "" {
		return "", fmt.Errorf("note create response did not include note id")
	}

	slog.Debug("Successfully posted note to Misskey",
		slog.String("endpoint", endpoint),
		slog.String("note_id", createResp.CreatedNote.ID),
		slog.Int("status_code", resp.StatusCode))

	return createResp.CreatedNote.ID, nil
}

// UploadDriveFileFromURL downloads an image from fileURL and uploads it to Misskey Drive.
func UploadDriveFileFromURL(ctx context.Context, host, token, fileURL string) (string, error) {
	if err := validateTwitterMediaURL(fileURL); err != nil {
		return "", err
	}

	mediaBytes, mediaType, filename, err := downloadImage(ctx, fileURL)
	if err != nil {
		return "", err
	}

	endpoint := "https://" + host + "/api/drive/files/create"
	bodyBuffer := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuffer)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(mediaBytes); err != nil {
		return "", err
	}
	if err := writer.WriteField("name", filename); err != nil {
		return "", err
	}
	if err := writer.WriteField("force", "true"); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bodyBuffer)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Upload-Content-Type", mediaType)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("drive file upload failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	var driveFile struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBytes, &driveFile); err != nil {
		return "", fmt.Errorf("failed to parse drive file response: %w", err)
	}
	if driveFile.ID == "" {
		return "", fmt.Errorf("drive file response did not include id")
	}
	return driveFile.ID, nil
}

func downloadImage(ctx context.Context, fileURL string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, "", "", err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", "", fmt.Errorf("media download failed with status %d", resp.StatusCode)
	}

	mediaType := strings.Split(resp.Header.Get("Content-Type"), ";")[0]
	if !strings.HasPrefix(mediaType, "image/") {
		return nil, "", "", fmt.Errorf("unsupported media type %q", mediaType)
	}

	mediaBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", err
	}
	if len(mediaBytes) == 0 {
		return nil, "", "", fmt.Errorf("media body is empty")
	}

	return mediaBytes, mediaType, filenameFromURL(fileURL), nil
}

func validateTwitterMediaURL(fileURL string) error {
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed")
	}

	host := strings.ToLower(parsed.Host)
	for _, allowedHost := range allowedTwitterMediaHosts() {
		if host == allowedHost {
			return nil
		}
	}

	return fmt.Errorf("URL host %q is not allowed", host)
}

func allowedTwitterMediaHosts() []string {
	rawHosts := os.Getenv("TWITTER_MEDIA_HOSTS")
	if rawHosts == "" {
		rawHosts = "pbs.twimg.com,video.twimg.com"
	}

	hosts := strings.Split(rawHosts, ",")
	allowedHosts := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			allowedHosts = append(allowedHosts, host)
		}
	}
	return allowedHosts
}

func filenameFromURL(fileURL string) string {
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return "media"
	}
	filename := path.Base(parsed.Path)
	if filename == "." || filename == "/" || filename == "" {
		return "media"
	}
	return filename
}

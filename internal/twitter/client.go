package twitter

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
	"strings"
	"time"

	"github.com/dghubble/oauth1"
)

const (
	UploadMediaEndpoint = "https://upload.twitter.com/1.1/media/upload.json"
	ManageTweetEndpoint = "https://api.twitter.com/2/tweets"
)

// httpClient is a reusable HTTP client with timeout
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

type UploadMediaResponse struct {
	MediaIDString string `json:"media_id_string"`
}

// validateMediaURL validates that the media URL is from an allowed host
func validateMediaURL(fileURL string) error {
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow HTTPS
	if parsed.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed")
	}

	// Check against allowed media host
	host := strings.ToLower(parsed.Host)
	mediaHost := os.Getenv("MISSKEY_MEDIA_HOST")

	if mediaHost == "" {
		return fmt.Errorf("MISSKEY_MEDIA_HOST is not configured")
	}

	if host == strings.ToLower(mediaHost) {
		return nil
	}

	return fmt.Errorf("URL host %q is not allowed (expected %q)", host, mediaHost)
}

func loadTwitterEnv() (string, string, string, string, error) {
	apiKey := os.Getenv("API_KEY")
	apiKeySecret := os.Getenv("API_KEY_SECRET")
	accessToken := os.Getenv("ACCESS_TOKEN")
	accessTokenSecret := os.Getenv("ACCESS_TOKEN_SECRET")

	if apiKey == "" || apiKeySecret == "" || accessToken == "" || accessTokenSecret == "" {
		return "", "", "", "", fmt.Errorf("missing Twitter API environment variables")
	}
	return apiKey, apiKeySecret, accessToken, accessTokenSecret, nil
}

// Post posts a tweet via IFTTT
func Post(ctx context.Context, text string) error {
	iftttEvent := os.Getenv("IFTTT_EVENT")
	if iftttEvent == "" {
		slog.Error("IFTTT event name not set")
		return fmt.Errorf("IFTTT event name not set")
	}

	iftttKey := os.Getenv("IFTTT_KEY")
	if iftttKey == "" {
		slog.Error("IFTTT key not set")
		return fmt.Errorf("IFTTT key not set")
	}

	iftttEndpoint := "https://maker.ifttt.com/trigger/" + iftttEvent + "/with/key/" + iftttKey

	payload := map[string]string{
		"value1": text,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Error marshaling IFTTT payload", slog.Any("error", err))
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", iftttEndpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		slog.Error("Error creating IFTTT request", slog.Any("error", err))
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Error("Error sending POST request to IFTTT", slog.Any("error", err))
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Non-OK response from IFTTT", slog.Int("status", resp.StatusCode))
		return fmt.Errorf("IFTTT POST request failed with status %d", resp.StatusCode)
	}

	escapedText := strings.ReplaceAll(text, "\n", "\\n")
	slog.Info("Successfully posted note to tweet via IFTTT",
		slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
		slog.String("endpoint", iftttEvent))

	return nil
}

// PostWithMedia posts a tweet with media attachments via Twitter API
func PostWithMedia(ctx context.Context, text string, fileURLs []string) error {
	ak, aks, at, ats, err := loadTwitterEnv()
	if err != nil {
		slog.Error("Error loading Twitter API keys", slog.Any("error", err))
		return err
	}

	config := oauth1.NewConfig(ak, aks)
	token := oauth1.NewToken(at, ats)
	oauthClient := config.Client(ctx, token)

	limit := len(fileURLs)
	if limit > 4 {
		limit = 4
	}

	var mediaIDs []string
	for i := 0; i < limit; i++ {
		mediaID, err := uploadMediaFromURL(ctx, oauthClient, fileURLs[i])
		if err != nil {
			return err
		}
		mediaIDs = append(mediaIDs, mediaID)
	}

	tweetBodyMap := map[string]interface{}{"text": text}
	if len(mediaIDs) > 0 {
		tweetBodyMap["media"] = map[string]interface{}{
			"media_ids": mediaIDs,
		}
	}

	tweetBody, err := json.Marshal(tweetBodyMap)
	if err != nil {
		slog.Error("Error marshaling tweet data", slog.Any("error", err))
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ManageTweetEndpoint, bytes.NewBuffer(tweetBody))
	if err != nil {
		slog.Error("Error creating tweet request", slog.Any("error", err))
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := oauthClient.Do(req)
	if err != nil {
		slog.Error("Error sending tweet request", slog.Any("error", err))
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Non-OK response from Twitter", slog.Int("status", resp.StatusCode))
		return fmt.Errorf("twitter POST request failed with status %d", resp.StatusCode)
	}

	escapedText := strings.ReplaceAll(text, "\n", "\\n")
	slog.Info("Successfully posted note to tweet with media",
		slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
		slog.Int("media_count", len(mediaIDs)))

	return nil
}

func uploadMediaFromURL(ctx context.Context, oauthClient *http.Client, fileURL string) (string, error) {
	// Validate URL to prevent SSRF attacks
	if err := validateMediaURL(fileURL); err != nil {
		slog.Error("Invalid media URL", slog.String("url", fileURL), slog.Any("error", err))
		return "", fmt.Errorf("invalid media URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBuffer := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuffer)

	part, err := writer.CreateFormFile("media", "image")
	if err != nil {
		return "", err
	}

	if _, err = io.Copy(part, resp.Body); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}

	uploadReq, err := http.NewRequestWithContext(ctx, "POST", UploadMediaEndpoint, bodyBuffer)
	if err != nil {
		return "", err
	}
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := oauthClient.Do(uploadReq)
	if err != nil {
		return "", err
	}
	defer func() { _ = uploadResp.Body.Close() }()

	respBytes, err := io.ReadAll(uploadResp.Body)
	if err != nil {
		return "", err
	}

	return extractMediaID(string(respBytes))
}

func extractMediaID(respBody string) (string, error) {
	var uploadResponse UploadMediaResponse
	if err := json.Unmarshal([]byte(respBody), &uploadResponse); err != nil {
		return "", fmt.Errorf("failed to parse JSON response: %w", err)
	}
	return uploadResponse.MediaIDString, nil
}

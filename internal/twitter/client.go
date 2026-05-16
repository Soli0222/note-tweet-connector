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
	"path"
	"strings"
	"time"

	"github.com/dghubble/oauth1"
)

const (
	UploadMediaEndpoint = "https://api.x.com/2/media/upload"
	ManageTweetEndpoint = "https://api.twitter.com/2/tweets"
	mediaChunkSize      = 4 * 1024 * 1024
	maxMediaStatusPolls = 30
)

// httpClient is a reusable HTTP client with timeout
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

type UploadMediaResponse struct {
	Data struct {
		ID             string          `json:"id"`
		MediaKey       string          `json:"media_key"`
		ProcessingInfo *ProcessingInfo `json:"processing_info"`
	} `json:"data"`
}

type ProcessingInfo struct {
	State          string `json:"state"`
	CheckAfterSecs int    `json:"check_after_secs"`
	Error          struct {
		Code    int    `json:"code"`
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
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

func loadTwitterUserAccessToken() (string, error) {
	token := os.Getenv("TWITTER_USER_ACCESS_TOKEN")
	if token == "" {
		return "", fmt.Errorf("TWITTER_USER_ACCESS_TOKEN environment variable is not set")
	}
	return token, nil
}

// Post posts a tweet via Twitter API.
func Post(ctx context.Context, text string) (string, error) {
	return PostWithMedia(ctx, text, nil)
}

// PostWithMedia posts a tweet with media attachments via Twitter API
func PostWithMedia(ctx context.Context, text string, fileURLs []string) (string, error) {
	ak, aks, at, ats, err := loadTwitterEnv()
	if err != nil {
		slog.Error("Error loading Twitter API keys", slog.Any("error", err))
		return "", err
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
		mediaID, err := uploadMediaFromURL(ctx, fileURLs[i])
		if err != nil {
			return "", err
		}
		mediaIDs = append(mediaIDs, mediaID)
	}

	return postTweet(ctx, oauthClient, text, mediaIDs)
}

func postTweet(ctx context.Context, oauthClient *http.Client, text string, mediaIDs []string) (string, error) {
	tweetBodyMap := map[string]interface{}{"text": text}
	if len(mediaIDs) > 0 {
		tweetBodyMap["media"] = map[string]interface{}{
			"media_ids": mediaIDs,
		}
	}
	tweetBody, err := json.Marshal(tweetBodyMap)
	if err != nil {
		slog.Error("Error marshaling tweet data", slog.Any("error", err))
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ManageTweetEndpoint, bytes.NewBuffer(tweetBody))
	if err != nil {
		slog.Error("Error creating tweet request", slog.Any("error", err))
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := oauthClient.Do(req)
	if err != nil {
		slog.Error("Error sending tweet request", slog.Any("error", err))
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		slog.Error("Non-OK response from Twitter", slog.Int("status", resp.StatusCode))
		return "", fmt.Errorf("twitter POST request failed with status %d", resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var postResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &postResp); err != nil {
		return "", fmt.Errorf("failed to parse twitter post response: %w", err)
	}
	if postResp.Data.ID == "" {
		return "", fmt.Errorf("twitter post response did not include tweet id")
	}

	escapedText := strings.ReplaceAll(text, "\n", "\\n")
	slog.Info("Successfully posted note to tweet",
		slog.String("tweet_id", postResp.Data.ID),
		slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
		slog.Int("media_count", len(mediaIDs)))

	return postResp.Data.ID, nil
}

func uploadMediaFromURL(ctx context.Context, fileURL string) (string, error) {
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

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("media download failed with status %d", resp.StatusCode)
	}

	mediaBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if len(mediaBytes) == 0 {
		return "", fmt.Errorf("media body is empty")
	}

	mediaType := resp.Header.Get("Content-Type")
	mediaType = strings.Split(mediaType, ";")[0]
	if mediaType == "" {
		mediaType = mediaTypeFromURL(fileURL)
	}
	mediaCategory, err := mediaCategoryForType(mediaType)
	if err != nil {
		return "", err
	}

	bearerToken, err := loadTwitterUserAccessToken()
	if err != nil {
		return "", err
	}

	mediaID, err := initMediaUpload(ctx, bearerToken, mediaType, mediaCategory, len(mediaBytes))
	if err != nil {
		return "", err
	}

	for segmentIndex, offset := 0, 0; offset < len(mediaBytes); segmentIndex, offset = segmentIndex+1, offset+mediaChunkSize {
		end := offset + mediaChunkSize
		if end > len(mediaBytes) {
			end = len(mediaBytes)
		}
		if err := appendMediaUpload(ctx, bearerToken, mediaID, segmentIndex, mediaBytes[offset:end]); err != nil {
			return "", err
		}
	}

	if err := finalizeMediaUpload(ctx, bearerToken, mediaID); err != nil {
		return "", err
	}

	return mediaID, nil
}

func initMediaUpload(ctx context.Context, bearerToken, mediaType, mediaCategory string, totalBytes int) (string, error) {
	fields := map[string]string{
		"command":        "INIT",
		"media_type":     mediaType,
		"media_category": mediaCategory,
		"total_bytes":    fmt.Sprintf("%d", totalBytes),
	}

	var uploadResponse UploadMediaResponse
	if err := postMediaForm(ctx, bearerToken, fields, "", nil, &uploadResponse); err != nil {
		return "", err
	}
	if uploadResponse.Data.ID == "" {
		return "", fmt.Errorf("media INIT response did not include data.id")
	}
	return uploadResponse.Data.ID, nil
}

func appendMediaUpload(ctx context.Context, bearerToken, mediaID string, segmentIndex int, mediaBytes []byte) error {
	fields := map[string]string{
		"command":       "APPEND",
		"media_id":      mediaID,
		"segment_index": fmt.Sprintf("%d", segmentIndex),
	}
	return postMediaForm(ctx, bearerToken, fields, "media", mediaBytes, nil)
}

func finalizeMediaUpload(ctx context.Context, bearerToken, mediaID string) error {
	fields := map[string]string{
		"command":  "FINALIZE",
		"media_id": mediaID,
	}

	var uploadResponse UploadMediaResponse
	if err := postMediaForm(ctx, bearerToken, fields, "", nil, &uploadResponse); err != nil {
		return err
	}
	if uploadResponse.Data.ProcessingInfo == nil {
		return nil
	}
	return waitForMediaProcessing(ctx, bearerToken, mediaID, uploadResponse.Data.ProcessingInfo)
}

func waitForMediaProcessing(ctx context.Context, bearerToken, mediaID string, processingInfo *ProcessingInfo) error {
	for i := 0; i < maxMediaStatusPolls; i++ {
		switch processingInfo.State {
		case "succeeded":
			return nil
		case "failed":
			return fmt.Errorf("media processing failed: %s", processingInfo.Error.Message)
		}

		wait := time.Duration(processingInfo.CheckAfterSecs) * time.Second
		if wait <= 0 {
			wait = time.Second
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		statusURL := UploadMediaEndpoint + "?command=STATUS&media_id=" + url.QueryEscape(mediaID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+bearerToken)

		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		respBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("media STATUS failed with status %d: %s", resp.StatusCode, string(respBytes))
		}

		var uploadResponse UploadMediaResponse
		if err := json.Unmarshal(respBytes, &uploadResponse); err != nil {
			return err
		}
		if uploadResponse.Data.ProcessingInfo == nil {
			return nil
		}
		processingInfo = uploadResponse.Data.ProcessingInfo
	}

	return fmt.Errorf("media processing did not complete after %d polls", maxMediaStatusPolls)
}

func postMediaForm(ctx context.Context, bearerToken string, fields map[string]string, fileField string, fileBytes []byte, responseBody interface{}) error {
	bodyBuffer := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuffer)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}

	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, "media")
		if err != nil {
			return err
		}
		if _, err := part.Write(fileBytes); err != nil {
			return err
		}
	}

	if err := writer.Close(); err != nil {
		return err
	}

	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, UploadMediaEndpoint, bodyBuffer)
	if err != nil {
		return err
	}
	uploadReq.Header.Set("Authorization", "Bearer "+bearerToken)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := httpClient.Do(uploadReq)
	if err != nil {
		return err
	}
	defer func() { _ = uploadResp.Body.Close() }()

	respBytes, err := io.ReadAll(uploadResp.Body)
	if err != nil {
		return err
	}

	if uploadResp.StatusCode < http.StatusOK || uploadResp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("media upload request failed with status %d: %s", uploadResp.StatusCode, string(respBytes))
	}

	if responseBody == nil || len(respBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBytes, responseBody); err != nil {
		return fmt.Errorf("failed to parse media upload response: %w", err)
	}
	return nil
}

func mediaCategoryForType(mediaType string) (string, error) {
	switch {
	case strings.HasPrefix(mediaType, "image/gif"):
		return "tweet_gif", nil
	case strings.HasPrefix(mediaType, "image/"):
		return "tweet_image", nil
	case strings.HasPrefix(mediaType, "video/"):
		return "tweet_video", nil
	default:
		return "", fmt.Errorf("unsupported media type %q", mediaType)
	}
}

func mediaTypeFromURL(fileURL string) string {
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	default:
		return ""
	}
}

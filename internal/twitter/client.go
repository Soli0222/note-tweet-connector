package twitter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	mediaChunkSize            = 4 * 1024 * 1024
	maxSimpleImageUploadBytes = 5 * 1024 * 1024
	maxMediaStatusPolls       = 30
)

var (
	ManageTweetEndpoint = "https://api.twitter.com/2/tweets"
	UploadMediaEndpoint = "https://api.x.com/2/media/upload"
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

type PostOptions struct {
	Text         string
	MediaURLs    []string
	QuoteTweetID string
}

type Config struct {
	OAuth2ClientID    string
	OAuth2RedirectURL string
	TokenStorePath    string
	BearerTokenSource BearerTokenSource
	MisskeyMediaHost  string
}

// validateMediaURL validates that the media URL is from an allowed host
func validateMediaURL(fileURL, mediaHost string) error {
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
	if mediaHost == "" {
		return fmt.Errorf("MISSKEY_MEDIA_HOST is not configured")
	}

	if host == strings.ToLower(mediaHost) {
		return nil
	}

	return fmt.Errorf("URL host %q is not allowed (expected %q)", host, mediaHost)
}

func (cfg Config) validate() error {
	if cfg.BearerTokenSource != nil {
		return nil
	}
	if cfg.OAuth2ClientID == "" {
		return fmt.Errorf("twitter OAuth 2.0 bearer token source is not configured")
	}
	return nil
}

func (cfg Config) bearerTokenSource() (BearerTokenSource, error) {
	if cfg.BearerTokenSource != nil {
		return cfg.BearerTokenSource, nil
	}
	if cfg.OAuth2ClientID != "" {
		return NewTokenManager(OAuth2Config{
			ClientID:       cfg.OAuth2ClientID,
			RedirectURL:    cfg.OAuth2RedirectURL,
			TokenStorePath: cfg.TokenStorePath,
		})
	}
	return nil, fmt.Errorf("twitter OAuth 2.0 bearer token source is not configured")
}

// Post posts a tweet via Twitter API.
func Post(ctx context.Context, text string) (string, error) {
	return PostWithOptionsConfig(ctx, Config{}, PostOptions{Text: text})
}

// PostWithMedia posts a tweet with media attachments via Twitter API
func PostWithMedia(ctx context.Context, text string, fileURLs []string) (string, error) {
	return PostWithOptionsConfig(ctx, Config{}, PostOptions{Text: text, MediaURLs: fileURLs})
}

func PostWithOptions(ctx context.Context, options PostOptions) (string, error) {
	return PostWithOptionsConfig(ctx, Config{}, options)
}

func PostWithOptionsConfig(ctx context.Context, cfg Config, options PostOptions) (string, error) {
	if err := cfg.validate(); err != nil {
		slog.Error("Error loading Twitter OAuth 2.0 token source", slog.Any("error", err))
		return "", err
	}

	limit := len(options.MediaURLs)
	if limit > 4 {
		limit = 4
	}

	var mediaIDs []string
	for i := 0; i < limit; i++ {
		mediaID, err := uploadMediaFromURL(ctx, cfg, options.MediaURLs[i])
		if err != nil {
			return "", err
		}
		mediaIDs = append(mediaIDs, mediaID)
	}

	tokenSource, err := cfg.bearerTokenSource()
	if err != nil {
		return "", err
	}
	return postTweet(ctx, tokenSource, options.Text, mediaIDs, options.QuoteTweetID)
}

func tweetBody(text string, mediaIDs []string, quoteTweetID string) map[string]interface{} {
	tweetBodyMap := map[string]interface{}{"text": text}
	if len(mediaIDs) > 0 {
		tweetBodyMap["media"] = map[string]interface{}{
			"media_ids": mediaIDs,
		}
	}
	if quoteTweetID != "" {
		tweetBodyMap["quote_tweet_id"] = quoteTweetID
	}
	return tweetBodyMap
}

func postTweet(ctx context.Context, tokenSource BearerTokenSource, text string, mediaIDs []string, quoteTweetID string) (string, error) {
	tweetBodyMap := tweetBody(text, mediaIDs, quoteTweetID)
	tweetBody, err := json.Marshal(tweetBodyMap)
	if err != nil {
		slog.Error("Error marshaling tweet data", slog.Any("error", err))
		return "", err
	}

	var respBytes []byte
	var statusCode int
	for attempt := 0; attempt < 2; attempt++ {
		bearerToken, err := tokenSource.BearerToken(ctx)
		if err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ManageTweetEndpoint, bytes.NewReader(tweetBody))
		if err != nil {
			slog.Error("Error creating tweet request", slog.Any("error", err))
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+bearerToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			slog.Error("Error sending tweet request", slog.Any("error", err))
			return "", err
		}
		respBytes, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "", err
		}

		statusCode = resp.StatusCode
		if statusCode == http.StatusUnauthorized && attempt == 0 {
			refresher, ok := tokenSource.(ForceRefreshBearerTokenSource)
			if ok {
				if err := refresher.Refresh(ctx); err != nil {
					return "", err
				}
				continue
			}
		}
		break
	}

	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		slog.Error("Non-OK response from Twitter", slog.Int("status", statusCode))
		return "", fmt.Errorf("twitter POST request failed with status %d: %s", statusCode, previewBody(respBytes))
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

func uploadMediaFromURL(ctx context.Context, cfg Config, fileURL string) (string, error) {
	// Validate URL to prevent SSRF attacks
	if err := validateMediaURL(fileURL, cfg.MisskeyMediaHost); err != nil {
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

	tokenSource, err := cfg.bearerTokenSource()
	if err != nil {
		return "", err
	}

	if shouldUseSimpleMediaUpload(mediaType, len(mediaBytes)) {
		return simpleMediaUpload(ctx, tokenSource, mediaType, mediaCategory, mediaBytes)
	}

	mediaID, err := initMediaUpload(ctx, tokenSource, mediaType, mediaCategory, len(mediaBytes))
	if err != nil {
		return "", err
	}

	for segmentIndex, offset := 0, 0; offset < len(mediaBytes); segmentIndex, offset = segmentIndex+1, offset+mediaChunkSize {
		end := offset + mediaChunkSize
		if end > len(mediaBytes) {
			end = len(mediaBytes)
		}
		if err := appendMediaUpload(ctx, tokenSource, mediaID, segmentIndex, mediaBytes[offset:end]); err != nil {
			return "", err
		}
	}

	if err := finalizeMediaUpload(ctx, tokenSource, mediaID); err != nil {
		return "", err
	}

	return mediaID, nil
}

func shouldUseSimpleMediaUpload(mediaType string, totalBytes int) bool {
	return strings.HasPrefix(mediaType, "image/") &&
		!strings.HasPrefix(mediaType, "image/gif") &&
		totalBytes <= maxSimpleImageUploadBytes
}

func simpleMediaUpload(ctx context.Context, tokenSource BearerTokenSource, mediaType, mediaCategory string, mediaBytes []byte) (string, error) {
	body := map[string]interface{}{
		"media":          base64.StdEncoding.EncodeToString(mediaBytes),
		"media_type":     mediaType,
		"media_category": mediaCategory,
	}

	var uploadResponse UploadMediaResponse
	if err := postMediaJSON(ctx, tokenSource, body, &uploadResponse); err != nil {
		return "", err
	}
	if uploadResponse.Data.ID == "" {
		return "", fmt.Errorf("media upload response did not include data.id")
	}
	return uploadResponse.Data.ID, nil
}

func postMediaJSON(ctx context.Context, tokenSource BearerTokenSource, body map[string]interface{}, responseBody interface{}) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	var respBytes []byte
	for attempt := 0; attempt < 2; attempt++ {
		bearerToken, err := tokenSource.BearerToken(ctx)
		if err != nil {
			return err
		}

		uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, UploadMediaEndpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		uploadReq.Header.Set("Authorization", "Bearer "+bearerToken)
		uploadReq.Header.Set("Content-Type", "application/json")

		uploadResp, err := httpClient.Do(uploadReq)
		if err != nil {
			return err
		}
		respBytes, err = io.ReadAll(uploadResp.Body)
		_ = uploadResp.Body.Close()
		if err != nil {
			return err
		}

		if uploadResp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			refresher, ok := tokenSource.(ForceRefreshBearerTokenSource)
			if ok {
				if err := refresher.Refresh(ctx); err != nil {
					return err
				}
				continue
			}
		}
		if uploadResp.StatusCode < http.StatusOK || uploadResp.StatusCode >= http.StatusMultipleChoices {
			return mediaUploadRequestError("", uploadResp.StatusCode, respBytes)
		}
		break
	}

	if responseBody == nil || len(respBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBytes, responseBody); err != nil {
		return fmt.Errorf("failed to parse media upload response: %w", err)
	}
	return nil
}

func initMediaUpload(ctx context.Context, tokenSource BearerTokenSource, mediaType, mediaCategory string, totalBytes int) (string, error) {
	fields := map[string]string{
		"command":        "INIT",
		"media_type":     mediaType,
		"media_category": mediaCategory,
		"total_bytes":    fmt.Sprintf("%d", totalBytes),
	}

	var uploadResponse UploadMediaResponse
	if err := postMediaForm(ctx, tokenSource, fields, "", nil, &uploadResponse); err != nil {
		return "", err
	}
	if uploadResponse.Data.ID == "" {
		return "", fmt.Errorf("media INIT response did not include data.id")
	}
	return uploadResponse.Data.ID, nil
}

func appendMediaUpload(ctx context.Context, tokenSource BearerTokenSource, mediaID string, segmentIndex int, mediaBytes []byte) error {
	fields := map[string]string{
		"command":       "APPEND",
		"media_id":      mediaID,
		"segment_index": fmt.Sprintf("%d", segmentIndex),
	}
	return postMediaForm(ctx, tokenSource, fields, "media", mediaBytes, nil)
}

func finalizeMediaUpload(ctx context.Context, tokenSource BearerTokenSource, mediaID string) error {
	fields := map[string]string{
		"command":  "FINALIZE",
		"media_id": mediaID,
	}

	var uploadResponse UploadMediaResponse
	if err := postMediaForm(ctx, tokenSource, fields, "", nil, &uploadResponse); err != nil {
		return err
	}
	if uploadResponse.Data.ProcessingInfo == nil {
		return nil
	}
	return waitForMediaProcessing(ctx, tokenSource, mediaID, uploadResponse.Data.ProcessingInfo)
}

func waitForMediaProcessing(ctx context.Context, tokenSource BearerTokenSource, mediaID string, processingInfo *ProcessingInfo) error {
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

		var respBytes []byte
		var statusCode int
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}

			statusCode, respBytes, err = getMediaUploadStatus(ctx, tokenSource, mediaID)
			if err != nil {
				return err
			}
			if statusCode == http.StatusUnauthorized && attempt == 0 {
				refresher, ok := tokenSource.(ForceRefreshBearerTokenSource)
				if ok {
					if err := refresher.Refresh(ctx); err != nil {
						return err
					}
					wait = 0
					continue
				}
			}
			break
		}
		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("media STATUS failed with status %d: %s", statusCode, string(respBytes))
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

func getMediaUploadStatus(ctx context.Context, tokenSource BearerTokenSource, mediaID string) (int, []byte, error) {
	statusURL := UploadMediaEndpoint + "?command=STATUS&media_id=" + url.QueryEscape(mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return 0, nil, err
	}
	bearerToken, err := tokenSource.BearerToken(ctx)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	respBytes, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return 0, nil, readErr
	}
	return resp.StatusCode, respBytes, nil
}

func postMediaForm(ctx context.Context, tokenSource BearerTokenSource, fields map[string]string, fileField string, fileBytes []byte, responseBody interface{}) error {
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

	bodyBytes := bodyBuffer.Bytes()
	contentType := writer.FormDataContentType()

	var respBytes []byte
	for attempt := 0; attempt < 2; attempt++ {
		bearerToken, err := tokenSource.BearerToken(ctx)
		if err != nil {
			return err
		}

		uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, UploadMediaEndpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		uploadReq.Header.Set("Authorization", "Bearer "+bearerToken)
		uploadReq.Header.Set("Content-Type", contentType)

		uploadResp, err := httpClient.Do(uploadReq)
		if err != nil {
			return err
		}
		respBytes, err = io.ReadAll(uploadResp.Body)
		_ = uploadResp.Body.Close()
		if err != nil {
			return err
		}

		if uploadResp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			refresher, ok := tokenSource.(ForceRefreshBearerTokenSource)
			if ok {
				if err := refresher.Refresh(ctx); err != nil {
					return err
				}
				continue
			}
		}
		if uploadResp.StatusCode < http.StatusOK || uploadResp.StatusCode >= http.StatusMultipleChoices {
			return mediaUploadRequestError(fields["command"], uploadResp.StatusCode, respBytes)
		}
		break
	}

	if responseBody == nil || len(respBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBytes, responseBody); err != nil {
		return fmt.Errorf("failed to parse media upload response: %w", err)
	}
	return nil
}

func mediaUploadRequestError(command string, statusCode int, respBytes []byte) error {
	detail := previewBody(respBytes)
	if command == "" {
		command = "request"
	}
	if statusCode == http.StatusForbidden {
		return fmt.Errorf("media upload %s failed with status %d: %s; verify the Twitter OAuth 2.0 user token was authorized with media.write, tweet.write, and offline.access scopes and that the developer app has Media API access", command, statusCode, detail)
	}
	return fmt.Errorf("media upload %s failed with status %d: %s", command, statusCode, detail)
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

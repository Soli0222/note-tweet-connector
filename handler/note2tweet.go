package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/dghubble/oauth1"
)

const (
	UploadMediaEndpoint = "https://upload.twitter.com/1.1/media/upload.json"
	ManageTweetEndpoint = "https://api.twitter.com/2/tweets"
)

// RTと@記号の検出用正規表現
var rtAtPattern = regexp.MustCompile(`^RT\s*@`)

type payloadNoteData struct {
	Server string `json:"server"`
	Body   struct {
		Note struct {
			ID         string        `json:"id"`
			Visibility string        `json:"visibility"`
			LocalOnly  bool          `json:"localOnly"`
			Files      []interface{} `json:"files"`
			Cw         string        `json:"cw"`
			Text       string        `json:"text"`
			Renote     struct {
				ID   string `json:"id"`
				URI  string `json:"uri"`
				Text string `json:"text"`
				User struct {
					Host     string `json:"host"`
					Username string `json:"username"`
				}
			} `json:"renote"`
		} `json:"note"`
	} `json:"body"`
}

func Note2TweetHandler(data []byte, tracker *ContentTracker) error {
	payload, err := parseNotePayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		return err
	}

	noteText := payload.Body.Note.Text
	noteURI := payload.Server + "/notes/" + payload.Body.Note.ID

	if payload.Body.Note.Cw != "" {
		circles := strings.Repeat("○", len(payload.Body.Note.Text))
		noteText = payload.Body.Note.Cw + "\n" + circles + "\n" + noteURI
	}

	if noteText == "" || noteText == "null" {
		if len(payload.Body.Note.Files) == 0 {
			renoteHost := payload.Body.Note.Renote.User.Host
			if renoteHost == "" {
				renoteHost = os.Getenv("MISSKEY_HOST")
			}
			noteText = "RN [at]" + payload.Body.Note.Renote.User.Username + "[at]" + renoteHost + "\n\n" + payload.Body.Note.Renote.Text + "\n\n" + payload.Body.Note.Renote.URI
		}
	}

	// "RT @" で始まるノートをスキップ
	if rtAtPattern.MatchString(noteText) {
		escapedText := strings.ReplaceAll(noteText, "\n", "\\n")
		slog.Info("Skipping RT @ note",
			slog.String("note_id", payload.Body.Note.ID),
			slog.String("text_preview", escapedText[:min(50, len(escapedText))]))
		return nil
	}

	// Check if this content has already been processed
	if tracker.IsProcessed(noteText) {
		slog.Info("Note already processed, skipping",
			slog.String("note_id", payload.Body.Note.ID))
		return nil
	}

	if payload.Body.Note.Visibility != "public" {
		slog.Info("Note is not public, skipping",
			slog.String("note_id", payload.Body.Note.ID),
			slog.String("visibility", payload.Body.Note.Visibility))
		return nil
	}

	var fileURLs []string
	for _, f := range payload.Body.Note.Files {
		if m, ok := f.(map[string]interface{}); ok {
			typeStr, _ := m["type"].(string)
			if !strings.Contains(typeStr, "image") {
				continue
			}
			if urlStr, ok := m["url"].(string); ok {
				fileURLs = append(fileURLs, urlStr)
			}
		}
	}

	if len(fileURLs) == 0 {
		err = Post(noteText)
	} else {
		err = PostWithMedia(noteText, fileURLs)
	}

	if err == nil {
		// Mark as processed only if posting was successful
		tracker.MarkProcessed(noteText)
		escapedText := strings.ReplaceAll(noteText, "\n", "\\n")
		slog.Info("Successfully posted note to tweet",
			slog.String("note_id", payload.Body.Note.ID),
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.Bool("has_media", len(fileURLs) > 0),
			slog.Int("media_count", len(fileURLs)))
	} else {
		slog.Error("Failed to post note to tweet",
			slog.String("note_id", payload.Body.Note.ID),
			slog.Any("error", err))
		return err
	}

	return nil
}

func parseNotePayload(data []byte) (*payloadNoteData, error) {
	var payload payloadNoteData
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
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

func Post(text string) error {
	IFTTT_EVENT := os.Getenv("IFTTT_EVENT")
	if IFTTT_EVENT == "" {
		slog.Error("IFTTT event name not set")
		return fmt.Errorf("IFTTT event name not set")
	}

	IFTTT_KEY := os.Getenv("IFTTT_KEY")
	if IFTTT_KEY == "" {
		slog.Error("IFTTT key not set")
		return fmt.Errorf("IFTTT key not set")
	}

	IFTTTEndpoint := "https://maker.ifttt.com/trigger/" + IFTTT_EVENT + "/with/key/" + IFTTT_KEY

	payload := map[string]string{
		"value1": text,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Error marshaling IFTTT payload", slog.Any("error", err))
		return err
	}

	resp, err := http.Post(IFTTTEndpoint, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		slog.Error("Error sending POST request to IFTTT", slog.Any("error", err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Non-OK response from IFTTT", slog.Int("status", resp.StatusCode))
		return fmt.Errorf("IFTTT POST request failed with status %d", resp.StatusCode)
	}

	escapedText := strings.ReplaceAll(text, "\n", "\\n")
	slog.Info("Successfully posted note to tweet via IFTTT",
		slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
		slog.String("endpoint", IFTTT_EVENT))

	return nil
}

func PostWithMedia(text string, fileURLs []string) error {
	ak, aks, at, ats, err := loadTwitterEnv()
	if err != nil {
		slog.Error("Error loading Twitter API keys", slog.Any("error", err))
		return err
	}

	config := oauth1.NewConfig(ak, aks)
	token := oauth1.NewToken(at, ats)
	httpClient := config.Client(oauth1.NoContext, token)

	limit := len(fileURLs)
	if limit > 4 {
		limit = 4
	}

	var mediaIDs []string
	for i := 0; i < limit; i++ {
		mediaID, err := uploadMediaFromURL(httpClient, fileURLs[i])
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

	req, err := http.NewRequest("POST", ManageTweetEndpoint, bytes.NewBuffer(tweetBody))
	if err != nil {
		slog.Error("Error creating tweet request", slog.Any("error", err))
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Error("Error sending tweet request", slog.Any("error", err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Non-OK response from Twitter", slog.Int("status", resp.StatusCode))
		return err
	}

	escapedText := strings.ReplaceAll(text, "\n", "\\n")
	slog.Info("Successfully posted note to tweet with media",
		slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
		slog.Int("media_count", len(mediaIDs)))

	return nil
}

func uploadMediaFromURL(httpClient *http.Client, fileURL string) (string, error) {
	resp, err := http.Get(fileURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

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

	req, err := http.NewRequest("POST", UploadMediaEndpoint, bodyBuffer)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer uploadResp.Body.Close()

	respBytes, err := io.ReadAll(uploadResp.Body)
	if err != nil {
		return "", err
	}

	return extractMediaID(string(respBytes))
}

type UploadMediaResponse struct {
	MediaIDString string `json:"media_id_string"`
}

func extractMediaID(respBody string) (string, error) {
	var uploadResponse UploadMediaResponse
	if err := json.Unmarshal([]byte(respBody), &uploadResponse); err != nil {
		return "", fmt.Errorf("failed to parse JSON response: %w", err)
	}
	return uploadResponse.MediaIDString, nil
}

package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
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

func Note2TweetHandler(ctx context.Context, data []byte, contentTracker *tracker.ContentTracker, m *metrics.Metrics) error {
	m.Note2TweetTotal.Inc()

	payload, err := parseNotePayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		m.Note2TweetErrors.Inc()
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
		m.Note2TweetSkipped.WithLabelValues("rt_pattern").Inc()
		return nil
	}

	if payload.Body.Note.Visibility != "public" {
		slog.Info("Note is not public, skipping",
			slog.String("note_id", payload.Body.Note.ID),
			slog.String("visibility", payload.Body.Note.Visibility))
		m.Note2TweetSkipped.WithLabelValues("not_public").Inc()
		return nil
	}

	// Atomically check and mark as processed to prevent race conditions
	if !contentTracker.MarkProcessedIfNotExists(noteText) {
		slog.Info("Note already processed, skipping",
			slog.String("note_id", payload.Body.Note.ID))
		m.Note2TweetSkipped.WithLabelValues("duplicate").Inc()
		m.TrackerDuplicatesHit.Inc()
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
		err = twitter.Post(ctx, noteText)
	} else {
		err = twitter.PostWithMedia(ctx, noteText, fileURLs)
	}

	if err == nil {
		escapedText := strings.ReplaceAll(noteText, "\n", "\\n")
		slog.Info("Successfully posted note to tweet",
			slog.String("note_id", payload.Body.Note.ID),
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.Bool("has_media", len(fileURLs) > 0),
			slog.Int("media_count", len(fileURLs)))
		m.Note2TweetSuccess.Inc()
	} else {
		slog.Error("Failed to post note to tweet",
			slog.String("note_id", payload.Body.Note.ID),
			slog.Any("error", err))
		m.Note2TweetErrors.Inc()
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

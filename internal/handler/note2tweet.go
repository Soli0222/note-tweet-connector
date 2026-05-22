package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
)

// RTと@記号の検出用正規表現
var rtAtPattern = regexp.MustCompile(`^RT\s*@`)

var postTweet = twitter.Post
var postTweetWithMedia = twitter.PostWithMedia
var postTweetWithOptions = twitter.PostWithOptions

type payloadNoteData struct {
	Server string `json:"server"`
	Body   struct {
		Note struct {
			ID         string        `json:"id"`
			UserID     string        `json:"userId"`
			Visibility string        `json:"visibility"`
			LocalOnly  bool          `json:"localOnly"`
			Files      []interface{} `json:"files"`
			Cw         string        `json:"cw"`
			Text       string        `json:"text"`
			RenoteID   string        `json:"renoteId"`
			ReplyID    string        `json:"replyId"`
			User       struct {
				ID       string `json:"id"`
				Host     string `json:"host"`
				Username string `json:"username"`
			} `json:"user"`
			Reply struct {
				ID string `json:"id"`
			} `json:"reply"`
			Renote struct {
				ID     string `json:"id"`
				UserID string `json:"userId"`
				URI    string `json:"uri"`
				Text   string `json:"text"`
				User   struct {
					ID       string `json:"id"`
					Host     string `json:"host"`
					Username string `json:"username"`
				}
			} `json:"renote"`
		} `json:"note"`
	} `json:"body"`
}

func Note2TweetHandler(ctx context.Context, data []byte, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) error {
	return Note2TweetHandlerWithConfig(ctx, Config{}, data, crossPostTracker, m)
}

func Note2TweetHandlerWithConfig(ctx context.Context, cfg Config, data []byte, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) error {
	m.Note2TweetTotal.Inc()

	payload, err := parseNotePayload(data)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		m.Note2TweetErrors.Inc()
		return err
	}

	noteID := payload.Body.Note.ID
	if noteID == "" {
		slog.Warn("Note ID is missing, skipping")
		m.Note2TweetSkipped.WithLabelValues("missing_id").Inc()
		return nil
	}

	tracked, err := crossPostTracker.HasMisskeyNote(ctx, noteID)
	if err != nil {
		slog.Error("Failed to check cross-post tracker",
			slog.String("note_id", noteID),
			slog.Any("error", err))
		m.Note2TweetErrors.Inc()
		return err
	}
	if tracked {
		slog.Info("Known cross-posted note, skipping",
			slog.String("note_id", noteID))
		m.Note2TweetSkipped.WithLabelValues("crosspost").Inc()
		m.TrackerDuplicatesHit.Inc()
		return nil
	}

	if payload.Body.Note.Visibility != "public" {
		slog.Info("Note is not public, skipping",
			slog.String("note_id", noteID),
			slog.String("visibility", payload.Body.Note.Visibility))
		m.Note2TweetSkipped.WithLabelValues("not_public").Inc()
		return nil
	}

	if payload.Body.Note.LocalOnly {
		slog.Info("Note is local only, skipping",
			slog.String("note_id", noteID),
			slog.Bool("local_only", payload.Body.Note.LocalOnly))
		m.Note2TweetSkipped.WithLabelValues("local_only").Inc()
		return nil
	}

	if replyID := noteReplyID(payload); replyID != "" {
		slog.Info("Note is a reply, skipping",
			slog.String("note_id", noteID),
			slog.String("reply_id", replyID))
		m.Note2TweetSkipped.WithLabelValues("reply").Inc()
		return nil
	}

	if renoteID := noteRenoteID(payload); renoteID != "" && !isOwnQuoteRenote(payload) {
		slog.Info("Note is a renote, skipping",
			slog.String("note_id", noteID),
			slog.String("renote_id", renoteID))
		m.Note2TweetSkipped.WithLabelValues("renote").Inc()
		return nil
	}

	noteText := payload.Body.Note.Text
	noteURI := payload.Server + "/notes/" + payload.Body.Note.ID
	quoteTweetID := ""

	if payload.Body.Note.Cw != "" {
		circles := strings.Repeat("○", len(payload.Body.Note.Text))
		noteText = payload.Body.Note.Cw + "\n" + circles + "\n" + noteURI
	} else if isOwnQuoteRenote(payload) {
		renoteID := noteRenoteID(payload)
		resolvedTweetID, ok, err := resolveTweetIDForMisskeyNote(ctx, crossPostTracker, renoteID)
		if err != nil {
			slog.Error("Failed to resolve quote renote source from tracker",
				slog.String("note_id", noteID),
				slog.String("renote_id", renoteID),
				slog.Any("error", err))
			m.Note2TweetErrors.Inc()
			return err
		}
		if ok {
			quoteTweetID = resolvedTweetID
		} else {
			slog.Info("Quote renote source not found in tracker",
				slog.String("note_id", noteID),
				slog.String("renote_id", renoteID))
		}
	}

	if noteText == "" || noteText == "null" {
		if len(payload.Body.Note.Files) == 0 {
			noteText = noteURI
		}
	}

	// "RT @" で始まるノートをスキップ
	if rtAtPattern.MatchString(noteText) {
		escapedText := strings.ReplaceAll(noteText, "\n", "\\n")
		slog.Info("Skipping RT @ note",
			slog.String("note_id", noteID),
			slog.String("text_preview", escapedText[:min(50, len(escapedText))]))
		m.Note2TweetSkipped.WithLabelValues("rt_pattern").Inc()
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

	var tweetID string
	options := twitter.PostOptions{
		Text:         noteText,
		MediaURLs:    fileURLs,
		QuoteTweetID: quoteTweetID,
	}
	if cfg.Twitter != (twitter.Config{}) {
		tweetID, err = twitter.PostWithOptionsConfig(ctx, cfg.Twitter, options)
	} else if quoteTweetID != "" {
		tweetID, err = postTweetWithOptions(ctx, options)
	} else if len(fileURLs) == 0 {
		tweetID, err = postTweet(ctx, options.Text)
	} else {
		tweetID, err = postTweetWithMedia(ctx, options.Text, options.MediaURLs)
	}

	if err == nil {
		if tweetID == "" {
			m.Note2TweetErrors.Inc()
			return errMissingPostedID("tweet")
		}
		if err := crossPostTracker.RememberMisskeyToTweet(ctx, noteID, tweetID); err != nil {
			slog.Error("Posted tweet but failed to record cross-post",
				slog.String("note_id", noteID),
				slog.String("tweet_id", tweetID),
				slog.Any("error", err))
			m.Note2TweetErrors.Inc()
			return err
		}
		escapedText := strings.ReplaceAll(noteText, "\n", "\\n")
		slog.Info("Successfully posted note to tweet",
			slog.String("note_id", noteID),
			slog.String("tweet_id", tweetID),
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.String("quote_tweet_id", quoteTweetID),
			slog.Bool("has_media", len(fileURLs) > 0),
			slog.Int("media_count", len(fileURLs)))
		m.Note2TweetSuccess.Inc()
	} else {
		slog.Error("Failed to post note to tweet",
			slog.String("note_id", noteID),
			slog.Any("error", err))
		notifyTwitterFailure(ctx, cfg, noteID, err, len(fileURLs), quoteTweetID)
		m.Note2TweetErrors.Inc()
		return err
	}

	return nil
}

func noteReplyID(payload *payloadNoteData) string {
	if payload.Body.Note.ReplyID != "" {
		return payload.Body.Note.ReplyID
	}
	return payload.Body.Note.Reply.ID
}

func noteRenoteID(payload *payloadNoteData) string {
	if payload.Body.Note.RenoteID != "" {
		return payload.Body.Note.RenoteID
	}
	return payload.Body.Note.Renote.ID
}

func isOwnQuoteRenote(payload *payloadNoteData) bool {
	if payload.Body.Note.Text == "" || payload.Body.Note.Text == "null" {
		return false
	}
	if noteRenoteID(payload) == "" {
		return false
	}
	if payload.Body.Note.UserID != "" && payload.Body.Note.Renote.UserID != "" {
		return payload.Body.Note.UserID == payload.Body.Note.Renote.UserID
	}
	if payload.Body.Note.User.ID != "" && payload.Body.Note.Renote.User.ID != "" {
		return payload.Body.Note.User.ID == payload.Body.Note.Renote.User.ID
	}
	return false
}

func resolveTweetIDForMisskeyNote(ctx context.Context, crossPostTracker tracker.CrossPostTracker, noteID string) (string, bool, error) {
	record, ok, err := crossPostTracker.FindByMisskeyNoteID(ctx, noteID)
	if err != nil {
		return "", false, err
	}
	if !ok || record.TweetID == "" {
		return "", false, nil
	}
	return record.TweetID, true, nil
}

func parseNotePayload(data []byte) (*payloadNoteData, error) {
	var payload payloadNoteData
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

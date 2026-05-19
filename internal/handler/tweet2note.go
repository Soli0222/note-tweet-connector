package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/misskey"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
)

type IncomingTweet struct {
	ID               string
	Text             string
	UserID           string
	Username         string
	URL              string
	MediaURLs        []string
	IsRetweet        bool
	QuotedTweetID    string
	QuotedUserID     string
	QuotedUsername   string
	InReplyToTweetID string
}

type Config struct {
	MisskeyHost              string
	MisskeyToken             string
	TwitterUsername          string
	TwitterMediaAllowedHosts []string
	Twitter                  twitter.Config
}

type filteredStreamPayload struct {
	Data     filteredStreamTweet    `json:"data"`
	Includes filteredStreamIncludes `json:"includes"`
}

type filteredStreamTweet struct {
	ID               string                    `json:"id"`
	Text             string                    `json:"text"`
	AuthorID         string                    `json:"author_id"`
	Attachments      filteredStreamAttachment  `json:"attachments"`
	ReferencedTweets []filteredStreamReference `json:"referenced_tweets"`
	InReplyToUserID  string                    `json:"in_reply_to_user_id"`
}

type filteredStreamAttachment struct {
	MediaKeys []string `json:"media_keys"`
}

type filteredStreamReference struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type filteredStreamIncludes struct {
	Tweets []filteredStreamTweet `json:"tweets"`
	Users  []filteredStreamUser  `json:"users"`
	Media  []filteredStreamMedia `json:"media"`
}

type filteredStreamUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type filteredStreamMedia struct {
	MediaKey        string `json:"media_key"`
	Type            string `json:"type"`
	URL             string `json:"url"`
	PreviewImageURL string `json:"preview_image_url"`
}

// RNとat記号の検出用正規表現
var rnAtPattern = regexp.MustCompile(`^RN\s*\[at\]`)

var createMisskeyNoteWithOptions = misskey.CreateNoteWithOptions
var uploadMisskeyDriveFileFromURL = misskey.UploadDriveFileFromURLWithAllowedHosts

func Tweet2NoteHandler(ctx context.Context, data []byte, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) error {
	return Tweet2NoteHandlerWithConfig(ctx, Config{}, data, crossPostTracker, m)
}

func Tweet2NoteHandlerWithConfig(ctx context.Context, cfg Config, data []byte, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) error {
	m.Tweet2NoteTotal.Inc()

	tweets, err := parseFilteredStreamPayloadWithConfig(data, cfg)
	if err != nil {
		slog.Error("Failed to parse payload", slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}
	if len(tweets) == 0 {
		slog.Warn("No eligible tweet in Twitter stream payload")
		m.Tweet2NoteSkipped.WithLabelValues("no_eligible_tweets").Inc()
		return nil
	}

	for _, tweet := range tweets {
		if err := HandleIncomingTweetWithConfig(ctx, cfg, tweet, crossPostTracker, m); err != nil {
			return err
		}
	}

	return nil
}

func HandleIncomingTweet(ctx context.Context, tweet IncomingTweet, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) error {
	return HandleIncomingTweetWithConfig(ctx, Config{}, tweet, crossPostTracker, m)
}

func HandleIncomingTweetWithConfig(ctx context.Context, cfg Config, tweet IncomingTweet, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) error {
	if tweet.ID == "" {
		slog.Warn("Tweet ID is missing, skipping")
		m.Tweet2NoteSkipped.WithLabelValues("missing_id").Inc()
		return nil
	}

	tracked, err := crossPostTracker.HasTweet(ctx, tweet.ID)
	if err != nil {
		slog.Error("Failed to check cross-post tracker",
			slog.String("tweet_id", tweet.ID),
			slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}
	if tracked {
		slog.Info("Known cross-posted tweet, skipping",
			slog.String("tweet_id", tweet.ID))
		m.Tweet2NoteSkipped.WithLabelValues("crosspost").Inc()
		m.TrackerDuplicatesHit.Inc()
		return nil
	}

	if tweet.InReplyToTweetID != "" {
		slog.Info("Tweet is a reply, skipping",
			slog.String("tweet_id", tweet.ID),
			slog.String("in_reply_to_tweet_id", tweet.InReplyToTweetID))
		m.Tweet2NoteSkipped.WithLabelValues("reply").Inc()
		return nil
	}

	tweetText := tweet.Text
	renoteID := ""

	if rtAtPattern.MatchString(tweetText) {
		tweetText = tweetText + "\n\n" + tweet.URL
	}

	// "RN [at]" で始まるツイートをスキップ
	if rnAtPattern.MatchString(tweetText) {
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Skipping RN [at] tweet",
			slog.String("text_preview", escapedText[:min(50, len(escapedText))]))
		m.Tweet2NoteSkipped.WithLabelValues("rn_pattern").Inc()
		return nil
	}

	if tweet.QuotedTweetID != "" {
		if tweetQuoteSameAuthor(tweet) {
			resolvedNoteID, ok, err := resolveMisskeyNoteIDForTweet(ctx, crossPostTracker, tweet.QuotedTweetID)
			if err != nil {
				slog.Error("Failed to resolve quote tweet source from tracker",
					slog.String("tweet_id", tweet.ID),
					slog.String("quoted_tweet_id", tweet.QuotedTweetID),
					slog.Any("error", err))
				m.Tweet2NoteErrors.Inc()
				return err
			}
			if ok {
				renoteID = resolvedNoteID
			} else {
				slog.Info("Quote tweet source not found in tracker",
					slog.String("tweet_id", tweet.ID),
					slog.String("quoted_tweet_id", tweet.QuotedTweetID))
			}
		} else {
			slog.Info("Quote tweet author mismatch, falling back to text",
				slog.String("tweet_id", tweet.ID),
				slog.String("quoted_tweet_id", tweet.QuotedTweetID))
		}
	}

	if cfg.MisskeyHost == "" {
		slog.Error("MISSKEY_HOST is not set")
		m.Tweet2NoteErrors.Inc()
		return fmt.Errorf("misskey host is not configured")
	}

	if cfg.MisskeyToken == "" {
		slog.Error("MISSKEY_TOKEN is not set")
		m.Tweet2NoteErrors.Inc()
		return fmt.Errorf("misskey token is not configured")
	}

	var fileIDs []string
	if !tweet.IsRetweet {
		fileIDs = make([]string, 0, min(len(tweet.MediaURLs), 4))
		for i := 0; i < len(tweet.MediaURLs) && i < 4; i++ {
			fileID, err := uploadMisskeyDriveFileFromURL(ctx, cfg.MisskeyHost, cfg.MisskeyToken, tweet.MediaURLs[i], cfg.TwitterMediaAllowedHosts)
			if err != nil {
				slog.Error("Failed to upload tweet media to Misskey Drive",
					slog.String("media_url", tweet.MediaURLs[i]),
					slog.Any("error", err))
				m.Tweet2NoteErrors.Inc()
				return err
			}
			fileIDs = append(fileIDs, fileID)
		}
	}

	noteID, err := createMisskeyNoteWithOptions(ctx, cfg.MisskeyHost, cfg.MisskeyToken, misskey.CreateNoteOptions{
		Text:     tweetText,
		FileIDs:  fileIDs,
		RenoteID: renoteID,
	})

	if err == nil {
		if noteID == "" {
			m.Tweet2NoteErrors.Inc()
			return errMissingPostedID("misskey note")
		}
		if err := crossPostTracker.RememberTweetToMisskey(ctx, tweet.ID, noteID); err != nil {
			slog.Error("Posted note but failed to record cross-post",
				slog.String("tweet_id", tweet.ID),
				slog.String("note_id", noteID),
				slog.Any("error", err))
			m.Tweet2NoteErrors.Inc()
			return err
		}
		escapedText := strings.ReplaceAll(tweetText, "\n", "\\n")
		slog.Info("Successfully forwarded tweet to note",
			slog.String("tweet_id", tweet.ID),
			slog.String("note_id", noteID),
			slog.String("text_preview", escapedText[:min(100, len(escapedText))]),
			slog.String("tweet_url", tweet.URL),
			slog.String("renote_id", renoteID),
			slog.Bool("has_media", len(fileIDs) > 0),
			slog.Int("media_count", len(fileIDs)))
		m.Tweet2NoteSuccess.Inc()
	} else {
		slog.Error("Failed to post tweet to note", slog.Any("error", err))
		m.Tweet2NoteErrors.Inc()
		return err
	}

	return nil
}

func parseFilteredStreamPayload(data []byte) ([]IncomingTweet, error) {
	return parseFilteredStreamPayloadWithConfig(data, Config{})
}

func parseFilteredStreamPayloadWithConfig(data []byte, cfg Config) ([]IncomingTweet, error) {
	var payload filteredStreamPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Data.ID == "" && payload.Data.Text == "" && len(payload.Data.Attachments.MediaKeys) == 0 {
		return nil, nil
	}

	text := payload.Data.Text
	isRetweet := filteredStreamRetweetedTweetID(payload) != ""
	var mediaURLs []string
	if !isRetweet {
		mediaURLs = filteredStreamMediaURLs(payload)
	}
	if text == "" && len(mediaURLs) == 0 {
		return nil, nil
	}

	username := filteredStreamUsername(payload, payload.Data.AuthorID)
	if username == "" {
		username = cfg.TwitterUsername
	}
	quotedTweetID, quotedUserID, quotedUsername := filteredStreamQuote(payload)
	tweetURL := buildTweetURL(username, payload.Data.ID)
	if retweetURL := filteredStreamRetweetURL(payload); retweetURL != "" {
		tweetURL = retweetURL
	}

	return []IncomingTweet{{
		ID:               payload.Data.ID,
		Text:             text,
		UserID:           payload.Data.AuthorID,
		Username:         username,
		URL:              tweetURL,
		MediaURLs:        mediaURLs,
		IsRetweet:        isRetweet,
		QuotedTweetID:    quotedTweetID,
		QuotedUserID:     quotedUserID,
		QuotedUsername:   quotedUsername,
		InReplyToTweetID: filteredStreamReplyTweetID(payload.Data),
	}}, nil
}

func filteredStreamUsername(payload filteredStreamPayload, userID string) string {
	for _, user := range payload.Includes.Users {
		if user.ID == userID {
			return user.Username
		}
	}
	return ""
}

func tweetQuoteSameAuthor(tweet IncomingTweet) bool {
	if tweet.UserID != "" && tweet.QuotedUserID != "" {
		return tweet.UserID == tweet.QuotedUserID
	}
	if tweet.Username != "" && tweet.QuotedUsername != "" {
		return tweet.Username == tweet.QuotedUsername
	}
	return false
}

func resolveMisskeyNoteIDForTweet(ctx context.Context, crossPostTracker tracker.CrossPostTracker, tweetID string) (string, bool, error) {
	record, ok, err := crossPostTracker.FindByTweetID(ctx, tweetID)
	if err != nil {
		return "", false, err
	}
	if !ok || record.MisskeyNoteID == "" {
		return "", false, nil
	}
	return record.MisskeyNoteID, true, nil
}

func filteredStreamMediaURLs(payload filteredStreamPayload) []string {
	mediaByKey := make(map[string]filteredStreamMedia, len(payload.Includes.Media))
	for _, media := range payload.Includes.Media {
		mediaByKey[media.MediaKey] = media
	}

	seen := map[string]struct{}{}
	mediaURLs := make([]string, 0, len(payload.Data.Attachments.MediaKeys))
	for _, mediaKey := range payload.Data.Attachments.MediaKeys {
		media, ok := mediaByKey[mediaKey]
		if !ok || media.Type != "photo" || media.URL == "" {
			continue
		}
		if _, ok := seen[media.URL]; ok {
			continue
		}
		seen[media.URL] = struct{}{}
		mediaURLs = append(mediaURLs, media.URL)
	}
	return mediaURLs
}

func filteredStreamQuote(payload filteredStreamPayload) (tweetID, userID, username string) {
	for _, ref := range payload.Data.ReferencedTweets {
		if ref.Type == "quoted" {
			tweetID = ref.ID
			break
		}
	}
	if tweetID == "" {
		return "", "", ""
	}
	for _, tweet := range payload.Includes.Tweets {
		if tweet.ID == tweetID {
			userID = tweet.AuthorID
			username = filteredStreamUsername(payload, userID)
			break
		}
	}
	return tweetID, userID, username
}

func filteredStreamRetweetURL(payload filteredStreamPayload) string {
	retweetedTweetID := filteredStreamRetweetedTweetID(payload)
	if retweetedTweetID == "" {
		return ""
	}
	for _, tweet := range payload.Includes.Tweets {
		if tweet.ID == retweetedTweetID {
			return buildTweetURL(filteredStreamUsername(payload, tweet.AuthorID), retweetedTweetID)
		}
	}
	return ""
}

func filteredStreamRetweetedTweetID(payload filteredStreamPayload) string {
	for _, ref := range payload.Data.ReferencedTweets {
		if ref.Type == "retweeted" {
			return ref.ID
		}
	}
	return ""
}

func filteredStreamReplyTweetID(tweet filteredStreamTweet) string {
	for _, ref := range tweet.ReferencedTweets {
		if ref.Type == "replied_to" {
			return ref.ID
		}
	}
	if tweet.InReplyToUserID != "" {
		return tweet.InReplyToUserID
	}
	return ""
}

func buildTweetURL(username, tweetID string) string {
	if username == "" || tweetID == "" {
		return ""
	}
	return "https://twitter.com/" + username + "/status/" + tweetID
}

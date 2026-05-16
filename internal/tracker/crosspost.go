package tracker

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// CrossPostRecord stores the relationship between a Misskey note and a Twitter tweet.
type CrossPostRecord struct {
	MisskeyNoteID string
	TweetID       string
	Direction     string
	CreatedAt     time.Time
}

// CrossPostTracker tracks cross-posted note/tweet IDs to prevent loops.
type CrossPostTracker struct {
	byMisskeyNoteID sync.Map
	byTweetID       sync.Map
	expiryDuration  time.Duration
}

const (
	DirectionMisskeyToTweet = "misskey_to_tweet"
	DirectionTweetToMisskey = "tweet_to_misskey"
)

// NewCrossPostTracker creates a new cross-post tracker with expiring entries.
func NewCrossPostTracker(ctx context.Context, expiryDuration time.Duration) *CrossPostTracker {
	tracker := &CrossPostTracker{
		expiryDuration: expiryDuration,
	}

	go tracker.periodicCleanup(ctx)

	return tracker
}

func (t *CrossPostTracker) periodicCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping cross-post tracker cleanup")
			return
		case <-ticker.C:
			t.cleanupExpired(time.Now())
		}
	}
}

func (t *CrossPostTracker) cleanupExpired(now time.Time) {
	t.byMisskeyNoteID.Range(func(key, value interface{}) bool {
		record, ok := value.(CrossPostRecord)
		if !ok || now.Sub(record.CreatedAt) > t.expiryDuration {
			t.byMisskeyNoteID.Delete(key)
			if ok && record.TweetID != "" {
				t.byTweetID.Delete(record.TweetID)
			}
		}
		return true
	})

	t.byTweetID.Range(func(key, value interface{}) bool {
		record, ok := value.(CrossPostRecord)
		if !ok || now.Sub(record.CreatedAt) > t.expiryDuration {
			t.byTweetID.Delete(key)
			if ok && record.MisskeyNoteID != "" {
				t.byMisskeyNoteID.Delete(record.MisskeyNoteID)
			}
		}
		return true
	})
}

// RememberMisskeyToTweet records a Misskey note that was cross-posted to Twitter.
func (t *CrossPostTracker) RememberMisskeyToTweet(noteID, tweetID string) {
	t.remember(noteID, tweetID, DirectionMisskeyToTweet)
}

// RememberTweetToMisskey records a Twitter tweet that was cross-posted to Misskey.
func (t *CrossPostTracker) RememberTweetToMisskey(tweetID, noteID string) {
	t.remember(noteID, tweetID, DirectionTweetToMisskey)
}

func (t *CrossPostTracker) remember(noteID, tweetID, direction string) {
	if noteID == "" || tweetID == "" {
		slog.Warn("Skipping cross-post record with empty ID",
			slog.String("misskey_note_id", noteID),
			slog.String("tweet_id", tweetID),
			slog.String("direction", direction))
		return
	}

	record := CrossPostRecord{
		MisskeyNoteID: noteID,
		TweetID:       tweetID,
		Direction:     direction,
		CreatedAt:     time.Now(),
	}

	t.byMisskeyNoteID.Store(noteID, record)
	t.byTweetID.Store(tweetID, record)

	slog.Debug("Cross-post recorded",
		slog.String("misskey_note_id", noteID),
		slog.String("tweet_id", tweetID),
		slog.String("direction", direction))
}

// HasMisskeyNote reports whether the Misskey note ID belongs to a known cross-post.
func (t *CrossPostTracker) HasMisskeyNote(noteID string) bool {
	if noteID == "" {
		return false
	}
	_, ok := t.byMisskeyNoteID.Load(noteID)
	return ok
}

// HasTweet reports whether the Twitter tweet ID belongs to a known cross-post.
func (t *CrossPostTracker) HasTweet(tweetID string) bool {
	if tweetID == "" {
		return false
	}
	_, ok := t.byTweetID.Load(tweetID)
	return ok
}

// FindByMisskeyNoteID returns the record for a Misskey note ID.
func (t *CrossPostTracker) FindByMisskeyNoteID(noteID string) (CrossPostRecord, bool) {
	value, ok := t.byMisskeyNoteID.Load(noteID)
	if !ok {
		return CrossPostRecord{}, false
	}
	record, ok := value.(CrossPostRecord)
	return record, ok
}

// FindByTweetID returns the record for a Twitter tweet ID.
func (t *CrossPostTracker) FindByTweetID(tweetID string) (CrossPostRecord, bool) {
	value, ok := t.byTweetID.Load(tweetID)
	if !ok {
		return CrossPostRecord{}, false
	}
	record, ok := value.(CrossPostRecord)
	return record, ok
}

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
type CrossPostTracker interface {
	RememberMisskeyToTweet(ctx context.Context, noteID, tweetID string) error
	RememberTweetToMisskey(ctx context.Context, tweetID, noteID string) error
	HasMisskeyNote(ctx context.Context, noteID string) (bool, error)
	HasTweet(ctx context.Context, tweetID string) (bool, error)
	FindByMisskeyNoteID(ctx context.Context, noteID string) (CrossPostRecord, bool, error)
	FindByTweetID(ctx context.Context, tweetID string) (CrossPostRecord, bool, error)
	Prune(ctx context.Context, now time.Time) (int64, error)
	Count(ctx context.Context) (int64, error)
	Close() error
}

// MemoryCrossPostTracker tracks cross-posted note/tweet IDs in memory.
type MemoryCrossPostTracker struct {
	byMisskeyNoteID sync.Map
	byTweetID       sync.Map
	retention       time.Duration
}

const (
	DirectionMisskeyToTweet = "misskey_to_tweet"
	DirectionTweetToMisskey = "tweet_to_misskey"
)

// NewCrossPostTracker creates a new in-memory cross-post tracker.
func NewCrossPostTracker(ctx context.Context, retention time.Duration) *MemoryCrossPostTracker {
	tracker := &MemoryCrossPostTracker{
		retention: retention,
	}

	go tracker.periodicPrune(ctx)

	return tracker
}

func (t *MemoryCrossPostTracker) periodicPrune(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping cross-post tracker prune")
			return
		case <-ticker.C:
			if _, err := t.Prune(ctx, time.Now()); err != nil {
				slog.Error("Failed to prune cross-post tracker", slog.Any("error", err))
			}
		}
	}
}

// Prune removes records older than the configured retention. A non-positive
// retention keeps records indefinitely.
func (t *MemoryCrossPostTracker) Prune(_ context.Context, now time.Time) (int64, error) {
	if t.retention <= 0 {
		return 0, nil
	}

	var deleted int64
	cutoff := now.Add(-t.retention)
	t.byMisskeyNoteID.Range(func(key, value interface{}) bool {
		record, ok := value.(CrossPostRecord)
		if !ok || record.CreatedAt.Before(cutoff) {
			t.byMisskeyNoteID.Delete(key)
			deleted++
			if ok && record.TweetID != "" {
				t.byTweetID.Delete(record.TweetID)
			}
		}
		return true
	})

	t.byTweetID.Range(func(key, value interface{}) bool {
		record, ok := value.(CrossPostRecord)
		if !ok || record.CreatedAt.Before(cutoff) {
			t.byTweetID.Delete(key)
			deleted++
			if ok && record.MisskeyNoteID != "" {
				t.byMisskeyNoteID.Delete(record.MisskeyNoteID)
			}
		}
		return true
	})

	return deleted, nil
}

// RememberMisskeyToTweet records a Misskey note that was cross-posted to Twitter.
func (t *MemoryCrossPostTracker) RememberMisskeyToTweet(ctx context.Context, noteID, tweetID string) error {
	return t.remember(ctx, noteID, tweetID, DirectionMisskeyToTweet)
}

// RememberTweetToMisskey records a Twitter tweet that was cross-posted to Misskey.
func (t *MemoryCrossPostTracker) RememberTweetToMisskey(ctx context.Context, tweetID, noteID string) error {
	return t.remember(ctx, noteID, tweetID, DirectionTweetToMisskey)
}

func (t *MemoryCrossPostTracker) remember(ctx context.Context, noteID, tweetID, direction string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if noteID == "" || tweetID == "" {
		slog.Warn("Skipping cross-post record with empty ID",
			slog.String("misskey_note_id", noteID),
			slog.String("tweet_id", tweetID),
			slog.String("direction", direction))
		return nil
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

	return nil
}

// HasMisskeyNote reports whether the Misskey note ID belongs to a known cross-post.
func (t *MemoryCrossPostTracker) HasMisskeyNote(ctx context.Context, noteID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if noteID == "" {
		return false, nil
	}
	_, ok := t.byMisskeyNoteID.Load(noteID)
	return ok, nil
}

// HasTweet reports whether the Twitter tweet ID belongs to a known cross-post.
func (t *MemoryCrossPostTracker) HasTweet(ctx context.Context, tweetID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if tweetID == "" {
		return false, nil
	}
	_, ok := t.byTweetID.Load(tweetID)
	return ok, nil
}

// FindByMisskeyNoteID returns the record for a Misskey note ID.
func (t *MemoryCrossPostTracker) FindByMisskeyNoteID(ctx context.Context, noteID string) (CrossPostRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrossPostRecord{}, false, err
	}
	value, ok := t.byMisskeyNoteID.Load(noteID)
	if !ok {
		return CrossPostRecord{}, false, nil
	}
	record, ok := value.(CrossPostRecord)
	return record, ok, nil
}

// FindByTweetID returns the record for a Twitter tweet ID.
func (t *MemoryCrossPostTracker) FindByTweetID(ctx context.Context, tweetID string) (CrossPostRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrossPostRecord{}, false, err
	}
	value, ok := t.byTweetID.Load(tweetID)
	if !ok {
		return CrossPostRecord{}, false, nil
	}
	record, ok := value.(CrossPostRecord)
	return record, ok, nil
}

// Count returns the number of records in the tracker.
func (t *MemoryCrossPostTracker) Count(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	var count int64
	t.byMisskeyNoteID.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count, nil
}

// Close releases tracker resources.
func (t *MemoryCrossPostTracker) Close() error {
	return nil
}

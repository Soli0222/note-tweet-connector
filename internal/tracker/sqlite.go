package tracker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteCrossPostTracker tracks cross-posted note/tweet IDs in sqlite.
type SQLiteCrossPostTracker struct {
	db        *sql.DB
	retention time.Duration
}

// NewSQLiteCrossPostTracker creates a sqlite-backed cross-post tracker.
func NewSQLiteCrossPostTracker(ctx context.Context, dbPath string, retention time.Duration) (*SQLiteCrossPostTracker, error) {
	if dbPath == "" {
		return nil, errors.New("tracker db path is empty")
	}

	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create tracker db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open tracker db: %w", err)
	}
	db.SetMaxOpenConns(1)

	tracker := &SQLiteCrossPostTracker{
		db:        db,
		retention: retention,
	}

	if err := tracker.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := tracker.Prune(ctx, time.Now()); err != nil {
		_ = db.Close()
		return nil, err
	}

	go tracker.periodicPrune(ctx)

	return tracker, nil
}

func (t *SQLiteCrossPostTracker) init(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS cross_posts (
			misskey_note_id TEXT NOT NULL,
			tweet_id TEXT NOT NULL,
			direction TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (misskey_note_id, tweet_id)
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cross_posts_misskey_note_id
			ON cross_posts (misskey_note_id);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cross_posts_tweet_id
			ON cross_posts (tweet_id);`,
		`CREATE INDEX IF NOT EXISTS idx_cross_posts_created_at
			ON cross_posts (created_at);`,
	}

	for _, statement := range statements {
		if _, err := t.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize tracker db: %w", err)
		}
	}
	return nil
}

func (t *SQLiteCrossPostTracker) periodicPrune(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping cross-post tracker prune")
			return
		case <-ticker.C:
			deleted, err := t.Prune(ctx, time.Now())
			if err != nil {
				slog.Error("Failed to prune cross-post tracker", slog.Any("error", err))
				continue
			}
			if deleted > 0 {
				slog.Info("Pruned old cross-post tracker records", slog.Int64("deleted", deleted))
			}
		}
	}
}

// RememberMisskeyToTweet records a Misskey note that was cross-posted to Twitter.
func (t *SQLiteCrossPostTracker) RememberMisskeyToTweet(ctx context.Context, noteID, tweetID string) error {
	return t.remember(ctx, noteID, tweetID, DirectionMisskeyToTweet)
}

// RememberTweetToMisskey records a Twitter tweet that was cross-posted to Misskey.
func (t *SQLiteCrossPostTracker) RememberTweetToMisskey(ctx context.Context, tweetID, noteID string) error {
	return t.remember(ctx, noteID, tweetID, DirectionTweetToMisskey)
}

func (t *SQLiteCrossPostTracker) remember(ctx context.Context, noteID, tweetID, direction string) error {
	if noteID == "" || tweetID == "" {
		slog.Warn("Skipping cross-post record with empty ID",
			slog.String("misskey_note_id", noteID),
			slog.String("tweet_id", tweetID),
			slog.String("direction", direction))
		return nil
	}

	const query = `
INSERT INTO cross_posts (misskey_note_id, tweet_id, direction, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(misskey_note_id, tweet_id) DO UPDATE SET
	direction = excluded.direction,
	created_at = excluded.created_at;`

	if _, err := t.db.ExecContext(ctx, query, noteID, tweetID, direction, time.Now().Unix()); err != nil {
		return fmt.Errorf("remember cross-post: %w", err)
	}

	slog.Debug("Cross-post recorded",
		slog.String("misskey_note_id", noteID),
		slog.String("tweet_id", tweetID),
		slog.String("direction", direction))

	return nil
}

// HasMisskeyNote reports whether the Misskey note ID belongs to a known cross-post.
func (t *SQLiteCrossPostTracker) HasMisskeyNote(ctx context.Context, noteID string) (bool, error) {
	if noteID == "" {
		return false, nil
	}
	return t.exists(ctx, "misskey_note_id", noteID)
}

// HasTweet reports whether the Twitter tweet ID belongs to a known cross-post.
func (t *SQLiteCrossPostTracker) HasTweet(ctx context.Context, tweetID string) (bool, error) {
	if tweetID == "" {
		return false, nil
	}
	return t.exists(ctx, "tweet_id", tweetID)
}

func (t *SQLiteCrossPostTracker) exists(ctx context.Context, column, id string) (bool, error) {
	query := fmt.Sprintf("SELECT 1 FROM cross_posts WHERE %s = ? LIMIT 1", column)
	var one int
	err := t.db.QueryRowContext(ctx, query, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query cross-post existence: %w", err)
	}
	return true, nil
}

// FindByMisskeyNoteID returns the record for a Misskey note ID.
func (t *SQLiteCrossPostTracker) FindByMisskeyNoteID(ctx context.Context, noteID string) (CrossPostRecord, bool, error) {
	return t.findBy(ctx, "misskey_note_id", noteID)
}

// FindByTweetID returns the record for a Twitter tweet ID.
func (t *SQLiteCrossPostTracker) FindByTweetID(ctx context.Context, tweetID string) (CrossPostRecord, bool, error) {
	return t.findBy(ctx, "tweet_id", tweetID)
}

func (t *SQLiteCrossPostTracker) findBy(ctx context.Context, column, id string) (CrossPostRecord, bool, error) {
	if id == "" {
		return CrossPostRecord{}, false, nil
	}

	query := fmt.Sprintf(`
SELECT misskey_note_id, tweet_id, direction, created_at
FROM cross_posts
WHERE %s = ?
LIMIT 1`, column)

	var record CrossPostRecord
	var createdAt int64
	err := t.db.QueryRowContext(ctx, query, id).Scan(
		&record.MisskeyNoteID,
		&record.TweetID,
		&record.Direction,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CrossPostRecord{}, false, nil
	}
	if err != nil {
		return CrossPostRecord{}, false, fmt.Errorf("find cross-post: %w", err)
	}

	record.CreatedAt = time.Unix(createdAt, 0)
	return record, true, nil
}

// Prune removes records older than the configured retention. A non-positive
// retention keeps records indefinitely.
func (t *SQLiteCrossPostTracker) Prune(ctx context.Context, now time.Time) (int64, error) {
	if t.retention <= 0 {
		return 0, nil
	}

	result, err := t.db.ExecContext(ctx, `DELETE FROM cross_posts WHERE created_at < ?`, now.Add(-t.retention).Unix())
	if err != nil {
		return 0, fmt.Errorf("prune cross-post tracker: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned cross-post records: %w", err)
	}
	return deleted, nil
}

// Count returns the number of records in the tracker.
func (t *SQLiteCrossPostTracker) Count(ctx context.Context) (int64, error) {
	var count int64
	if err := t.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cross_posts`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count cross-post records: %w", err)
	}
	return count, nil
}

// Close releases tracker resources.
func (t *SQLiteCrossPostTracker) Close() error {
	return t.db.Close()
}

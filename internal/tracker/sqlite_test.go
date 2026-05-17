package tracker

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteCrossPostTracker_PersistsRecords(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tracker.sqlite")

	tracker, err := NewSQLiteCrossPostTracker(ctx, dbPath, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("NewSQLiteCrossPostTracker() error = %v", err)
	}
	if err := tracker.RememberMisskeyToTweet(ctx, "note-1", "tweet-1"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	tracker, err = NewSQLiteCrossPostTracker(ctx, dbPath, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("NewSQLiteCrossPostTracker() reopen error = %v", err)
	}
	defer closeTracker(t, tracker)

	record, ok, err := tracker.FindByMisskeyNoteID(ctx, "note-1")
	if err != nil {
		t.Fatalf("FindByMisskeyNoteID() error = %v", err)
	}
	if !ok {
		t.Fatal("FindByMisskeyNoteID() did not find persisted record")
	}
	if record.MisskeyNoteID != "note-1" || record.TweetID != "tweet-1" {
		t.Fatalf("record = %#v, want note-1/tweet-1", record)
	}
}

func TestSQLiteCrossPostTracker_PruneRetention(t *testing.T) {
	ctx := context.Background()
	tracker, err := NewSQLiteCrossPostTracker(ctx, filepath.Join(t.TempDir(), "tracker.sqlite"), 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSQLiteCrossPostTracker() error = %v", err)
	}
	defer closeTracker(t, tracker)

	if err := tracker.RememberMisskeyToTweet(ctx, "note-old", "tweet-old"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if _, err := tracker.db.ExecContext(ctx, `UPDATE cross_posts SET created_at = ?`, time.Now().Add(-48*time.Hour).Unix()); err != nil {
		t.Fatalf("backdate record: %v", err)
	}

	deleted, err := tracker.Prune(ctx, time.Now())
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("Prune() deleted = %d, want 1", deleted)
	}
	if ok, err := tracker.HasTweet(ctx, "tweet-old"); err != nil || ok {
		t.Fatal("pruned tweet ID should not be tracked")
	}
}

func TestSQLiteCrossPostTracker_NonPositiveRetentionKeepsRecords(t *testing.T) {
	ctx := context.Background()
	tracker, err := NewSQLiteCrossPostTracker(ctx, filepath.Join(t.TempDir(), "tracker.sqlite"), 0)
	if err != nil {
		t.Fatalf("NewSQLiteCrossPostTracker() error = %v", err)
	}
	defer closeTracker(t, tracker)

	if err := tracker.RememberMisskeyToTweet(ctx, "note-old", "tweet-old"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if _, err := tracker.db.ExecContext(ctx, `UPDATE cross_posts SET created_at = ?`, time.Now().Add(-365*24*time.Hour).Unix()); err != nil {
		t.Fatalf("backdate record: %v", err)
	}

	deleted, err := tracker.Prune(ctx, time.Now())
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if deleted != 0 {
		t.Fatalf("Prune() deleted = %d, want 0", deleted)
	}
	if ok, err := tracker.HasTweet(ctx, "tweet-old"); err != nil || !ok {
		t.Fatal("record should be retained with non-positive retention")
	}
}

func TestSQLiteCrossPostTracker_RejectsConflictingIDs(t *testing.T) {
	ctx := context.Background()
	tracker, err := NewSQLiteCrossPostTracker(ctx, filepath.Join(t.TempDir(), "tracker.sqlite"), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("NewSQLiteCrossPostTracker() error = %v", err)
	}
	defer closeTracker(t, tracker)

	if err := tracker.RememberMisskeyToTweet(ctx, "note-1", "tweet-1"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if err := tracker.RememberMisskeyToTweet(ctx, "note-1", "tweet-2"); err == nil {
		t.Fatal("RememberMisskeyToTweet() with conflicting tweet ID succeeded, want error")
	}
	if err := tracker.RememberTweetToMisskey(ctx, "tweet-1", "note-2"); err == nil {
		t.Fatal("RememberTweetToMisskey() with conflicting note ID succeeded, want error")
	}
}

func TestSQLiteCrossPostTracker_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	tracker, err := NewSQLiteCrossPostTracker(ctx, filepath.Join(t.TempDir(), "tracker.sqlite"), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("NewSQLiteCrossPostTracker() error = %v", err)
	}
	defer closeTracker(t, tracker)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			noteID := fmt.Sprintf("note-%d", i)
			tweetID := fmt.Sprintf("tweet-%d", i)
			if err := tracker.RememberMisskeyToTweet(ctx, noteID, tweetID); err != nil {
				t.Errorf("RememberMisskeyToTweet() error = %v", err)
				return
			}
			if ok, err := tracker.HasMisskeyNote(ctx, noteID); err != nil || !ok {
				t.Errorf("HasMisskeyNote(%q) = %v, %v; want true, nil", noteID, ok, err)
			}
		}(i)
	}
	wg.Wait()

	count, err := tracker.Count(ctx)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 100 {
		t.Fatalf("Count() = %d, want 100", count)
	}
}

func closeTracker(t *testing.T, tracker *SQLiteCrossPostTracker) {
	t.Helper()

	if err := tracker.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

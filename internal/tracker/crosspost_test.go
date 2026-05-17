package tracker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewCrossPostTracker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 1*time.Hour)
	if tracker == nil {
		t.Fatal("NewCrossPostTracker() returned nil")
	}
}

func TestCrossPostTracker_RememberMisskeyToTweet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 1*time.Hour)
	if err := tracker.RememberMisskeyToTweet(ctx, "note-1", "tweet-1"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}

	if ok, err := tracker.HasMisskeyNote(ctx, "note-1"); err != nil || !ok {
		t.Fatal("HasMisskeyNote() = false, want true")
	}
	if ok, err := tracker.HasTweet(ctx, "tweet-1"); err != nil || !ok {
		t.Fatal("HasTweet() = false, want true")
	}

	record, ok, err := tracker.FindByMisskeyNoteID(ctx, "note-1")
	if err != nil {
		t.Fatalf("FindByMisskeyNoteID() error = %v", err)
	}
	if !ok {
		t.Fatal("FindByMisskeyNoteID() did not find record")
	}
	if record.MisskeyNoteID != "note-1" || record.TweetID != "tweet-1" {
		t.Fatalf("record = %#v, want note-1/tweet-1", record)
	}
	if record.Direction != DirectionMisskeyToTweet {
		t.Fatalf("Direction = %q, want %q", record.Direction, DirectionMisskeyToTweet)
	}
}

func TestCrossPostTracker_RememberTweetToMisskey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 1*time.Hour)
	if err := tracker.RememberTweetToMisskey(ctx, "tweet-2", "note-2"); err != nil {
		t.Fatalf("RememberTweetToMisskey() error = %v", err)
	}

	if ok, err := tracker.HasTweet(ctx, "tweet-2"); err != nil || !ok {
		t.Fatal("HasTweet() = false, want true")
	}
	if ok, err := tracker.HasMisskeyNote(ctx, "note-2"); err != nil || !ok {
		t.Fatal("HasMisskeyNote() = false, want true")
	}

	record, ok, err := tracker.FindByTweetID(ctx, "tweet-2")
	if err != nil {
		t.Fatalf("FindByTweetID() error = %v", err)
	}
	if !ok {
		t.Fatal("FindByTweetID() did not find record")
	}
	if record.MisskeyNoteID != "note-2" || record.TweetID != "tweet-2" {
		t.Fatalf("record = %#v, want note-2/tweet-2", record)
	}
	if record.Direction != DirectionTweetToMisskey {
		t.Fatalf("Direction = %q, want %q", record.Direction, DirectionTweetToMisskey)
	}
}

func TestCrossPostTracker_EmptyIDsAreIgnored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 1*time.Hour)
	if err := tracker.RememberMisskeyToTweet(ctx, "", "tweet-1"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if err := tracker.RememberMisskeyToTweet(ctx, "note-1", ""); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if err := tracker.RememberTweetToMisskey(ctx, "", "note-2"); err != nil {
		t.Fatalf("RememberTweetToMisskey() error = %v", err)
	}
	if err := tracker.RememberTweetToMisskey(ctx, "tweet-2", ""); err != nil {
		t.Fatalf("RememberTweetToMisskey() error = %v", err)
	}

	if ok, err := tracker.HasMisskeyNote(ctx, "note-1"); err != nil || ok {
		t.Fatal("empty tweet ID record should not be stored")
	}
	if ok, err := tracker.HasTweet(ctx, "tweet-1"); err != nil || ok {
		t.Fatal("empty note ID record should not be stored")
	}
	if ok, err := tracker.HasMisskeyNote(ctx, "note-2"); err != nil || ok {
		t.Fatal("empty tweet ID record should not be stored")
	}
	if ok, err := tracker.HasTweet(ctx, "tweet-2"); err != nil || ok {
		t.Fatal("empty note ID record should not be stored")
	}
}

func TestCrossPostTracker_PruneRetention(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 50*time.Millisecond)
	if err := tracker.RememberMisskeyToTweet(ctx, "note-old", "tweet-old"); err != nil {
		t.Fatalf("RememberMisskeyToTweet() error = %v", err)
	}
	if _, err := tracker.Prune(ctx, time.Now().Add(1*time.Hour)); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	if ok, err := tracker.HasMisskeyNote(ctx, "note-old"); err != nil || ok {
		t.Fatal("expired Misskey note record should be removed")
	}
	if ok, err := tracker.HasTweet(ctx, "tweet-old"); err != nil || ok {
		t.Fatal("expired tweet record should be removed")
	}
}

func TestCrossPostTracker_ConcurrentAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 1*time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			noteID := fmt.Sprintf("note-%d", i)
			tweetID := fmt.Sprintf("tweet-%d", i)
			if err := tracker.RememberMisskeyToTweet(ctx, noteID, tweetID); err != nil {
				t.Errorf("RememberMisskeyToTweet() error = %v", err)
			}
			if ok, err := tracker.HasMisskeyNote(ctx, noteID); err != nil || !ok {
				t.Errorf("HasMisskeyNote(%q) = false, want true", noteID)
			}
			if ok, err := tracker.HasTweet(ctx, tweetID); err != nil || !ok {
				t.Errorf("HasTweet(%q) = false, want true", tweetID)
			}
		}(i)
	}
	wg.Wait()
}

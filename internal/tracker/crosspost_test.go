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
	tracker.RememberMisskeyToTweet("note-1", "tweet-1")

	if !tracker.HasMisskeyNote("note-1") {
		t.Fatal("HasMisskeyNote() = false, want true")
	}
	if !tracker.HasTweet("tweet-1") {
		t.Fatal("HasTweet() = false, want true")
	}

	record, ok := tracker.FindByMisskeyNoteID("note-1")
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
	tracker.RememberTweetToMisskey("tweet-2", "note-2")

	if !tracker.HasTweet("tweet-2") {
		t.Fatal("HasTweet() = false, want true")
	}
	if !tracker.HasMisskeyNote("note-2") {
		t.Fatal("HasMisskeyNote() = false, want true")
	}

	record, ok := tracker.FindByTweetID("tweet-2")
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
	tracker.RememberMisskeyToTweet("", "tweet-1")
	tracker.RememberMisskeyToTweet("note-1", "")
	tracker.RememberTweetToMisskey("", "note-2")
	tracker.RememberTweetToMisskey("tweet-2", "")

	if tracker.HasMisskeyNote("note-1") {
		t.Fatal("empty tweet ID record should not be stored")
	}
	if tracker.HasTweet("tweet-1") {
		t.Fatal("empty note ID record should not be stored")
	}
	if tracker.HasMisskeyNote("note-2") {
		t.Fatal("empty tweet ID record should not be stored")
	}
	if tracker.HasTweet("tweet-2") {
		t.Fatal("empty note ID record should not be stored")
	}
}

func TestCrossPostTracker_CleanupExpired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewCrossPostTracker(ctx, 50*time.Millisecond)
	tracker.RememberMisskeyToTweet("note-old", "tweet-old")
	tracker.cleanupExpired(time.Now().Add(1 * time.Hour))

	if tracker.HasMisskeyNote("note-old") {
		t.Fatal("expired Misskey note record should be removed")
	}
	if tracker.HasTweet("tweet-old") {
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
			tracker.RememberMisskeyToTweet(noteID, tweetID)
			if !tracker.HasMisskeyNote(noteID) {
				t.Errorf("HasMisskeyNote(%q) = false, want true", noteID)
			}
			if !tracker.HasTweet(tweetID) {
				t.Errorf("HasTweet(%q) = false, want true", tweetID)
			}
		}(i)
	}
	wg.Wait()
}

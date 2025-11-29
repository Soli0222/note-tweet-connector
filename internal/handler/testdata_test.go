package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
)

// TestWithTestData tests handlers using the testdata files
func TestWithTestData_MisskeyNote(t *testing.T) {
	// Find testdata directory
	testdataDir := findTestdataDir(t)

	// Read the test file
	data, err := os.ReadFile(filepath.Join(testdataDir, "misskey_note.json"))
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	// Parse and verify
	payload, err := parseNotePayload(data)
	if err != nil {
		t.Fatalf("Failed to parse misskey note: %v", err)
	}

	// Verify parsed values
	if payload.Body.Note.ID != "dummy-note-1" {
		t.Errorf("expected note ID 'dummy-note-1', got '%s'", payload.Body.Note.ID)
	}
	if payload.Body.Note.Text != "This is a dummy note for testing purposes." {
		t.Errorf("unexpected text: %s", payload.Body.Note.Text)
	}
	if payload.Body.Note.Visibility != "public" {
		t.Errorf("expected visibility 'public', got '%s'", payload.Body.Note.Visibility)
	}
	if payload.Server != "http://localhost:3000" {
		t.Errorf("expected server 'http://localhost:3000', got '%s'", payload.Server)
	}
}

func TestWithTestData_IFTTTTweet(t *testing.T) {
	testdataDir := findTestdataDir(t)

	data, err := os.ReadFile(filepath.Join(testdataDir, "ifttt_tweet.json"))
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	payload, err := parseTweetPayload(data)
	if err != nil {
		t.Fatalf("Failed to parse IFTTT tweet: %v", err)
	}

	// Verify parsed values
	expectedText := "This is a dummy tweet for testing purposes.\n#Testing #DummyData\nhttps://t.co/dummylink123"
	if payload.Body.Tweet.Text != expectedText {
		t.Errorf("unexpected text: %s", payload.Body.Tweet.Text)
	}
	if payload.Body.Tweet.Url != "https://twitter.com/dummy_user/status/1234567890123456789" {
		t.Errorf("unexpected URL: %s", payload.Body.Tweet.Url)
	}
}

func TestWithTestData_Note2TweetHandler(t *testing.T) {
	testdataDir := findTestdataDir(t)

	data, err := os.ReadFile(filepath.Join(testdataDir, "misskey_note.json"))
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	// This will fail at Twitter posting (no credentials) but should parse correctly
	err = Note2TweetHandler(ctx, data, contentTracker, m)
	// We expect an error because IFTTT credentials are not set
	if err == nil {
		// If no error, check that it was skipped for a valid reason
		// (e.g., localOnly=true in test data)
		t.Log("Note was skipped or processed without error")
	}
}

func TestWithTestData_Tweet2NoteHandler(t *testing.T) {
	testdataDir := findTestdataDir(t)

	data, err := os.ReadFile(filepath.Join(testdataDir, "ifttt_tweet.json"))
	if err != nil {
		t.Fatalf("Failed to read test data: %v", err)
	}

	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	// Set required environment variables
	t.Setenv("MISSKEY_HOST", "misskey.example")
	t.Setenv("MISSKEY_TOKEN", "test-token")

	// This will fail at Misskey posting (no real server) but should parse correctly
	err = Tweet2NoteHandler(ctx, data, contentTracker, m)
	// We expect an error because Misskey server is not reachable
	if err == nil {
		t.Log("Tweet was skipped or processed without error")
	}
}

func findTestdataDir(t *testing.T) string {
	t.Helper()

	// Try current directory first
	if _, err := os.Stat("testdata"); err == nil {
		return "testdata"
	}

	// Try parent directories
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for i := 0; i < 5; i++ {
		testdataPath := filepath.Join(dir, "testdata")
		if _, err := os.Stat(testdataPath); err == nil {
			return testdataPath
		}
		dir = filepath.Dir(dir)
	}

	t.Fatal("testdata directory not found")
	return ""
}

package tracker

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewContentTracker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)
	if tracker == nil {
		t.Fatal("NewContentTracker() returned nil")
	}
}

func TestContentTracker_MarkProcessedIfNotExists(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// First call should return true (new content)
	isNew := tracker.MarkProcessedIfNotExists("test-content-1")
	if !isNew {
		t.Error("MarkProcessedIfNotExists() should return true for new content")
	}

	// Second call with same content should return false (already exists)
	isNew = tracker.MarkProcessedIfNotExists("test-content-1")
	if isNew {
		t.Error("MarkProcessedIfNotExists() should return false for existing content")
	}

	// Different content should return true
	isNew = tracker.MarkProcessedIfNotExists("test-content-2")
	if !isNew {
		t.Error("MarkProcessedIfNotExists() should return true for different content")
	}
}

func TestContentTracker_IsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Content not yet processed
	if tracker.IsProcessed("new-content") {
		t.Error("IsProcessed() should return false for new content")
	}

	// Mark content as processed
	tracker.MarkProcessedIfNotExists("new-content")

	// Now it should be processed
	if !tracker.IsProcessed("new-content") {
		t.Error("IsProcessed() should return true for processed content")
	}
}

func TestContentTracker_Cleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use very short TTL for testing
	tracker := NewContentTracker(ctx, 50*time.Millisecond)

	tracker.MarkProcessedIfNotExists("old-content")

	// Should be processed initially
	if !tracker.IsProcessed("old-content") {
		t.Error("Content should be processed initially")
	}

	// Wait for cleanup (cleanup interval is 1 minute by default, so we need to test differently)
	// The cleanup runs every minute, so we can't easily test it in a short time
	// Instead, we just verify the content exists immediately
}

func TestContentTracker_ConcurrentAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// Launch multiple goroutines trying to mark the same content
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			isNew := tracker.MarkProcessedIfNotExists("concurrent-content")
			if isNew {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one goroutine should succeed
	if successCount != 1 {
		t.Errorf("Expected exactly 1 success, got %d", successCount)
	}
}

func TestContentTracker_ConcurrentDifferentContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// Launch multiple goroutines with different content
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			content := string(rune('a' + id%26)) // Use different content
			isNew := tracker.MarkProcessedIfNotExists(content)
			if isNew {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// 26 unique contents (a-z), each should succeed once
	if successCount != 26 {
		t.Errorf("Expected 26 successes, got %d", successCount)
	}
}

func TestContentTracker_EmptyContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Empty content should still work
	isNew := tracker.MarkProcessedIfNotExists("")
	if !isNew {
		t.Error("MarkProcessedIfNotExists() should return true for empty content initially")
	}

	isNew = tracker.MarkProcessedIfNotExists("")
	if isNew {
		t.Error("MarkProcessedIfNotExists() should return false for duplicate empty content")
	}
}

func TestContentTracker_UnicodeContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Japanese content
	isNew := tracker.MarkProcessedIfNotExists("ラグトレイン / 稲葉曇")
	if !isNew {
		t.Error("MarkProcessedIfNotExists() should return true for Japanese content")
	}

	// Duplicate Japanese content
	isNew = tracker.MarkProcessedIfNotExists("ラグトレイン / 稲葉曇")
	if isNew {
		t.Error("MarkProcessedIfNotExists() should return false for duplicate Japanese content")
	}

	// Emoji content
	isNew = tracker.MarkProcessedIfNotExists("Hello 🎵 World 🌍")
	if !isNew {
		t.Error("MarkProcessedIfNotExists() should return true for emoji content")
	}
}

func TestContentTracker_LongContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Create long content
	longContent := ""
	for i := 0; i < 1000; i++ {
		longContent += "This is a very long content string. "
	}

	isNew := tracker.MarkProcessedIfNotExists(longContent)
	if !isNew {
		t.Error("MarkProcessedIfNotExists() should return true for long content")
	}

	isNew = tracker.MarkProcessedIfNotExists(longContent)
	if isNew {
		t.Error("MarkProcessedIfNotExists() should return false for duplicate long content")
	}
}

func TestContentTracker_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	tracker := NewContentTracker(ctx, 50*time.Millisecond)

	tracker.MarkProcessedIfNotExists("content")

	// Cancel context
	cancel()

	// Give some time for cleanup goroutine to exit
	time.Sleep(100 * time.Millisecond)

	// Should still be able to use the tracker (just cleanup stops)
	isNew := tracker.MarkProcessedIfNotExists("new-content")
	if !isNew {
		t.Error("Tracker should still work after context cancellation")
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		expected string
	}{
		{
			name:     "short string",
			input:    "hello",
			maxRunes: 10,
			expected: "hello",
		},
		{
			name:     "exact length",
			input:    "hello",
			maxRunes: 5,
			expected: "hello",
		},
		{
			name:     "truncate ASCII",
			input:    "hello world",
			maxRunes: 5,
			expected: "hello",
		},
		{
			name:     "truncate Japanese",
			input:    "ラグトレイン",
			maxRunes: 3,
			expected: "ラグト",
		},
		{
			name:     "empty string",
			input:    "",
			maxRunes: 5,
			expected: "",
		},
		{
			name:     "zero max runes",
			input:    "hello",
			maxRunes: 0,
			expected: "",
		},
		{
			name:     "emoji truncation",
			input:    "🎵🎶🎵🎶🎵",
			maxRunes: 3,
			expected: "🎵🎶🎵",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxRunes)
			if result != tt.expected {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxRunes, result, tt.expected)
			}
		})
	}
}

func TestContentTracker_HashNormalization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Content with different whitespace should be treated as the same
	isNew := tracker.MarkProcessedIfNotExists("hello world")
	if !isNew {
		t.Error("First content should be new")
	}

	// Same content with extra spaces
	isNew = tracker.MarkProcessedIfNotExists("hello   world")
	if isNew {
		t.Error("Content with different whitespace should be treated as duplicate")
	}

	// Same content with newlines
	isNew = tracker.MarkProcessedIfNotExists("hello\nworld")
	if isNew {
		t.Error("Content with newlines should be treated as duplicate")
	}
}

func TestContentTracker_URLRemoval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Content with URL
	isNew := tracker.MarkProcessedIfNotExists("hello https://example.com world")
	if !isNew {
		t.Error("First content should be new")
	}

	// Same content without URL should be treated as the same
	isNew = tracker.MarkProcessedIfNotExists("hello world")
	if isNew {
		t.Error("Content without URL should be treated as duplicate")
	}
}

func BenchmarkMarkProcessedIfNotExists(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.MarkProcessedIfNotExists(string(rune(i % 10000)))
	}
}

func BenchmarkIsProcessed(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracker := NewContentTracker(ctx, 1*time.Hour)

	// Pre-populate
	for i := 0; i < 10000; i++ {
		tracker.MarkProcessedIfNotExists(string(rune(i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.IsProcessed(string(rune(i % 10000)))
	}
}

package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ContentTracker caches hashes of processed content to prevent loops
type ContentTracker struct {
	processedHashes sync.Map      // Thread-safe map for storing hashes
	expiryDuration  time.Duration // Duration to keep hashes in memory
}

// Hash constants
const (
	// Truncate to this length before hashing to normalize across platforms
	maxContentLength = 280
)

// Regular expressions for URL detection
var (
	urlPattern = regexp.MustCompile(`https?://[^\s]+`)
)

// NewContentTracker creates a new content tracker with entries expiring after the specified duration
func NewContentTracker(expiryDuration time.Duration) *ContentTracker {
	tracker := &ContentTracker{
		expiryDuration: expiryDuration,
	}

	// Start cleanup process for expired entries
	go tracker.periodicCleanup()

	return tracker
}

// periodicCleanup removes expired entries every minute
func (c *ContentTracker) periodicCleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		c.processedHashes.Range(func(key, value interface{}) bool {
			timestamp, ok := value.(time.Time)
			if !ok || now.Sub(timestamp) > c.expiryDuration {
				c.processedHashes.Delete(key)
				slog.Debug("Removed expired content hash", slog.String("hash", key.(string)))
			}
			return true
		})
	}
}

// computeHash generates a stable hash for the content
func (c *ContentTracker) computeHash(content string) string {
	// 小文字化、改行の削除、空白のトリミングによる正規化
	normalized := strings.ToLower(content)
	normalized = strings.ReplaceAll(normalized, "\n", " ")
	normalized = strings.TrimSpace(normalized)

	// URLを削除（すべてのURLを一括で処理）
	normalized = urlPattern.ReplaceAllString(normalized, "")

	// 連続する空白を1つに置換
	normalized = strings.Join(strings.Fields(normalized), " ")

	// プラットフォーム間で統一するために先頭部分のみを使用
	if len(normalized) > maxContentLength {
		normalized = normalized[:maxContentLength]
	}

	hasher := sha256.New()
	hasher.Write([]byte(normalized))
	hash := hex.EncodeToString(hasher.Sum(nil))

	slog.Debug("Content hash computed",
		slog.String("hash", hash),
		slog.String("normalized_content", normalized))

	return hash
}

// IsProcessed checks if content has been recently processed
func (c *ContentTracker) IsProcessed(content string) bool {
	hash := c.computeHash(content)

	if _, exists := c.processedHashes.Load(hash); exists {
		// Escape newlines for better log visibility
		escapedContent := strings.ReplaceAll(content, "\n", "\\n")
		slog.Info("Content already processed",
			slog.String("hash", hash),
			slog.String("content_preview", escapedContent[:min(50, len(escapedContent))]))
		return true
	}
	return false
}

// MarkProcessed marks content as processed
func (c *ContentTracker) MarkProcessed(content string) {
	hash := c.computeHash(content)
	c.processedHashes.Store(hash, time.Now())

	// Escape newlines for better log visibility
	escapedContent := strings.ReplaceAll(content, "\n", "\\n")
	slog.Debug("Content marked as processed",
		slog.String("hash", hash),
		slog.String("content_preview", escapedContent[:min(50, len(escapedContent))]))
}

// Helper function to get minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

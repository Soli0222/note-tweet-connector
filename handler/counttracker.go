package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ContentTracker はループ防止のために処理済みコンテンツのハッシュをキャッシュ管理する
type ContentTracker struct {
	processedHashes sync.Map      // スレッドセーフなハッシュ保存用マップ
	expiryDuration  time.Duration // ハッシュをメモリに保持する期間
}

// ハッシュ定数
const (
	// プラットフォーム間で正規化するために、ハッシュ化前にこの長さに切り詰める
	maxContentLength = 280
)

// NewContentTracker は指定した期間後にエントリが期限切れになる新しいコンテンツトラッカーを作成する
func NewContentTracker(expiryDuration time.Duration) *ContentTracker {
	tracker := &ContentTracker{
		expiryDuration: expiryDuration,
	}

	// 期限切れエントリを削除するクリーンアッププロセスを開始
	go tracker.periodicCleanup()

	return tracker
}

// periodicCleanup は1分ごとに期限切れのエントリを削除する
func (c *ContentTracker) periodicCleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		c.processedHashes.Range(func(key, value interface{}) bool {
			timestamp, ok := value.(time.Time)
			if !ok || now.Sub(timestamp) > c.expiryDuration {
				c.processedHashes.Delete(key)
				slog.Debug("期限切れのコンテンツハッシュを削除しました", slog.String("hash", key.(string)))
			}
			return true
		})
	}
}

// computeHash はコンテンツの安定したハッシュを生成する
func (c *ContentTracker) computeHash(content string) string {
	// 空白を削除し、小文字化し、改行を除去することでコンテンツを正規化
	normalized := strings.ToLower(content)
	normalized = strings.ReplaceAll(normalized, "\n", " ")
	normalized = strings.TrimSpace(normalized)

	// 署名を追加する可能性があるプラットフォーム間で正規化するために、先頭部分のみ使用
	if len(normalized) > maxContentLength {
		normalized = normalized[:maxContentLength]
	}

	hasher := sha256.New()
	hasher.Write([]byte(normalized))
	return hex.EncodeToString(hasher.Sum(nil))
}

// IsProcessed はコンテンツが最近処理されたかどうかをチェックする
func (c *ContentTracker) IsProcessed(content string) bool {
	hash := c.computeHash(content)

	if _, exists := c.processedHashes.Load(hash); exists {
		slog.Info("このコンテンツはすでに処理済みです", slog.String("hash", hash))
		return true
	}
	return false
}

// MarkProcessed はコンテンツを処理済みとしてマークする
func (c *ContentTracker) MarkProcessed(content string) {
	hash := c.computeHash(content)
	c.processedHashes.Store(hash, time.Now())
	slog.Debug("コンテンツを処理済みとしてマークしました", slog.String("hash", hash))
}

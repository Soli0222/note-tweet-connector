# CrossPostTracker sqlite永続化方針

## 現状

- `internal/tracker/crosspost.go` の `CrossPostTracker` が `sync.Map` を2つ持ち、Misskey note ID と Twitter tweet ID の対応をオンメモリで管理している。
  - `byMisskeyNoteID`: Misskey note ID -> `CrossPostRecord`
  - `byTweetID`: Twitter tweet ID -> `CrossPostRecord`
- `NewCrossPostTracker(ctx, expiryDuration)` が1分ごとの goroutine を起動し、`cleanupExpired` で期限切れレコードを削除している。
- ただし現在は Misskey note ID / Twitter tweet ID ベースで追跡しているため、同じ内容の別投稿を誤って重複扱いする問題はない。以前の投稿内容ハッシュベース追跡とは異なり、重複判定のためのTTLは不要。
- handler側は `*tracker.CrossPostTracker` に直接依存している。
  - `Note2TweetHandler` は `HasMisskeyNote` で重複判定し、投稿成功後に `RememberMisskeyToTweet` を呼ぶ。
  - 引用リノートでは `FindByMisskeyNoteID` で対応する tweet ID を引く。
  - `Tweet2NoteHandler` / `HandleIncomingTweet` は `HasTweet` で重複判定し、投稿成功後に `RememberTweetToMisskey` を呼ぶ。
  - 引用ツイートでは `FindByTweetID` で対応する Misskey note ID を引く。
- 現在のTracker APIはエラーを返さない同期APIなので、DB永続化時にI/Oエラーを呼び出し元へどう伝えるかが設計上の大きな分岐点になる。
- `tracker_entries_total` メトリクスは定義されているが、現状のTracker実装から値を更新する処理は見当たらない。
- `Dockerfile` は `CGO_ENABLED=0` でLinuxバイナリを作っている。`github.com/mattn/go-sqlite3` を使うならCGO有効化が必要になるため、現状のDocker方針とは相性が悪い。

## 推奨方針

sqlite backed tracker を `internal/tracker` 内に実装し、handlerからはインターフェース越しに使う形へ寄せる。

### 1. Tracker APIをインターフェース化する

今の `*CrossPostTracker` 直接依存をやめ、handlerが必要とする最小APIを定義する。

```go
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
```

理由:

- sqliteでは `INSERT` / `SELECT` / `DELETE` が失敗しうるため、エラーなしAPIのままだと障害時に「見逃して二重投稿」か「記録できていないのに成功扱い」になりやすい。
- handlerにはすでに `context.Context` が渡っているので、DB操作にも同じcontextを渡せる。
- テストではオンメモリ実装や一時sqliteファイルを差し替えやすくなる。

移行負荷を抑えたい場合は、既存のオンメモリ実装を `MemoryCrossPostTracker` にリネームし、同じインターフェースを実装する。sqlite実装が安定するまではテストでメモリ実装も残せる。

### 2. sqliteドライバはpure Go優先

現状のDockerfileを維持するなら `modernc.org/sqlite` を使うのが扱いやすい。

- `CGO_ENABLED=0` のままビルドできる。
- Alpineのビルド環境にCコンパイラやsqlite-devを追加しなくてよい。

`github.com/mattn/go-sqlite3` を選ぶ場合は、DockerfileをCGO有効ビルドへ変える必要がある。

- builderに `gcc` / `musl-dev` / `sqlite-dev` などを追加する。
- `CGO_ENABLED=1` にする。
- 静的リンクやAlpine runtimeとの相性を検証する。

このリポジトリではコンテナを小さく保つ意図が見えるため、まずは `modernc.org/sqlite` が妥当。

### 3. テーブル設計

1レコードに Misskey note ID と Twitter tweet ID の対応を保持する。

```sql
CREATE TABLE IF NOT EXISTS cross_posts (
  misskey_note_id TEXT NOT NULL,
  tweet_id TEXT NOT NULL,
  direction TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (misskey_note_id, tweet_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_cross_posts_misskey_note_id
  ON cross_posts (misskey_note_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_cross_posts_tweet_id
  ON cross_posts (tweet_id);

CREATE INDEX IF NOT EXISTS idx_cross_posts_created_at
  ON cross_posts (created_at);
```

`created_at` はUnix秒またはUnixミリ秒の整数で持つ。Go側の比較とsqliteの削除条件が単純になる。

`direction` は既存定数を維持する。

- `misskey_to_tweet`
- `tweet_to_misskey`

### 4. 保存処理

`Remember...` は空IDを今と同じく無視する。ただしDB操作失敗時はerrorを返す。

```sql
INSERT INTO cross_posts (misskey_note_id, tweet_id, direction, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(misskey_note_id, tweet_id) DO UPDATE SET
  direction = excluded.direction,
  created_at = excluded.created_at;
```

注意点:

- `misskey_note_id` と `tweet_id` は個別にUNIQUEにするため、片側IDが既存で別の対応を持つケースは制約違反になる。
- その場合はログに出してerrorとして扱うのが安全。無理に上書きすると既存の引用解決を壊す可能性がある。
- 重複webhookが来ても同じペアなら冪等に更新できる。

### 5. 検索・重複判定

`HasMisskeyNote` / `HasTweet` はIDだけで判定する。内容ハッシュではなく外部サービスの投稿IDを使うため、同じ本文を時間を空けて投稿してもIDが違えば別投稿として扱える。

```sql
SELECT 1
FROM cross_posts
WHERE misskey_note_id = ?
LIMIT 1;
```

`FindBy...` も期限では絞らず、IDだけで探す。

古い対応を保持しておくと、過去投稿への引用リノート / 引用ツイートでも対応解決に使える。したがって `tracker-retention` は重複判定ロジックではなく、DBサイズを抑えるための保存期間として扱う。

### 6. retention prune

既存の `cleanupExpired` は「重複判定の期限切れ削除」だが、sqlite化後は意味を変える。IDベースでは重複判定用TTLは不要なので、削除処理はDB肥大化対策の `retention prune` として実装する。

デフォルト保持期間は3か月にする。

候補:

- フラグ: `-tracker-retention`
- 環境変数: `TRACKER_RETENTION`
- デフォルト: `2160h`（90日相当）

削除条件:

```sql
DELETE FROM cross_posts
WHERE created_at < ?;
```

`?` には `now - retention` を渡す。`retention <= 0` の場合は無期限保持として削除しない、という逃げ道を用意しておくと運用しやすい。

実行タイミング:

- 起動直後に1回実行する。
- 以後は1日1回程度で十分。Webhook処理の重複判定とは独立しているため、1分ごとに走らせる必要はない。

テストしやすいように `Prune(ctx, now)` のようなメソッドを用意し、現在時刻を引数で渡せるようにする。

### 7. 起動設定

`cmd/note-tweet-connector/main.go` にDBパス設定を追加する。

候補:

- フラグ: `-tracker-db-path`
- フラグ: `-tracker-retention`
- 環境変数: `TRACKER_DB_PATH`
- 環境変数: `TRACKER_RETENTION`
- デフォルト: `/app/data/tracker.sqlite` または `tracker.sqlite`
- retentionデフォルト: `2160h`

Docker Composeでは永続化用volumeを追加する。

```yaml
volumes:
  - './.env:/app/.env:ro'
  - './data:/app/data'
```

デフォルトを `/app/data/tracker.sqlite` にするなら、コンテナ利用時に自然に永続化できる。一方、ローカル実行ではカレントディレクトリに `data` を作る必要があるため、READMEに明記する。

### 8. sqlite接続設定

`database/sql` を使い、起動時に以下を行う。

- DBをopenする。
- `SetMaxOpenConns(1)` を設定する。
- `PRAGMA journal_mode=WAL;`
- `PRAGMA busy_timeout = 5000;`
- schema migrationを実行する。
- 起動直後にretention対象レコードをpruneする。

単一プロセスのWebhookサーバーなので、最初は `SetMaxOpenConns(1)` で十分。WALとbusy timeoutを入れておけば、将来読み取りが増えても詰まりにくい。

### 9. エラー時のhandler挙動

重複判定DBエラーは安全側に倒してリクエストを失敗扱いにする。

- `HasMisskeyNote` / `HasTweet` が失敗したら投稿しないでerrorを返す。
- `FindBy...` が失敗したら投稿処理を続けずerrorを返すか、引用解決だけ諦めるかを決める必要がある。

推奨は以下。

- 重複判定の失敗: errorを返す。
- 引用解決の失敗: errorを返す。
- 投稿成功後の `Remember...` 失敗: errorを返す。ただし外部投稿は既に成功しているため、ログで強く警告する。

投稿成功後のDB記録失敗は完全には巻き戻せない。ここを避けるには「投稿前に処理中状態を記録する」設計もあるが、現状のTrackerは投稿済み対応だけを持つ単純なモデルなので、第一段階ではそこまで広げない。

### 10. メトリクス

`tracker_entries_total` はsqliteの件数から更新する。

候補:

- prune後に `SELECT COUNT(*) FROM cross_posts` してGaugeへ反映する。
- `Remember...` 成功後にも件数更新する。

ただし `tracker` パッケージが `metrics` パッケージに依存すると循環や責務肥大につながる。おすすめは `CrossPostTracker.Count(ctx)` を追加し、main側で定期更新するか、prune後の件数をmainがGaugeへ入れる形。

第一段階では `Count(ctx)` を追加して、mainで1分ごとにGauge更新するのがわかりやすい。

## 実装ステップ

1. `internal/tracker` にインターフェースを追加する。
2. 既存実装を `MemoryCrossPostTracker` として残し、テストを必要に応じて調整する。
3. `SQLiteCrossPostTracker` を追加する。
   - `NewSQLiteCrossPostTracker(ctx, dbPath, retention)` を用意する。
   - open、PRAGMA、schema作成、起動時prune、periodic pruneを実装する。
4. handlerの引数を `*tracker.CrossPostTracker` から `tracker.CrossPostTracker` インターフェースへ変更する。
5. handler内のTracker呼び出しをcontext/error対応に変更する。
6. mainに `-tracker-db-path` / `-tracker-retention` と `TRACKER_DB_PATH` / `TRACKER_RETENTION` overrideを追加し、sqlite trackerを生成する。
7. graceful shutdownで `crossPostTracker.Close()` を呼ぶ。
8. Docker Composeに永続volumeを追加する。
9. READMEのアーキテクチャ、オプション、Docker起動説明、メトリクス説明を更新する。
10. テストを追加・更新する。

## テスト方針

`internal/tracker`:

- 一時ディレクトリ配下のsqliteファイルで保存できること。
- trackerを閉じて作り直しても、レコードを検索できること。
- `HasMisskeyNote` / `HasTweet` がretention期間に関係なく、DBに残っているIDをtrueにすること。
- pruneでretention対象レコードがDBから消えること。
- `retention <= 0` ではpruneしても削除されないこと。
- 空IDが保存されないこと。
- 同じペアの再保存が冪等であること。
- 片側IDだけ同じで別ペアの保存はエラーになること。
- 並行アクセスで `Remember...` と `Has...` が破綻しないこと。

handler:

- Trackerエラー時に投稿処理へ進まないこと。
- 投稿成功後のTracker保存失敗がerrorとして返ること。
- 引用解決でDB永続化済みレコードを使えること。

main:

- `-tracker-db-path` / `TRACKER_DB_PATH` と `-tracker-retention` / `TRACKER_RETENTION` が設定に反映されること。
- `Close()` がshutdown時に呼ばれることをテストできる構造にすること。

## 注意点

- `tracker-expiry` は廃止または非推奨にし、sqlite化後は `tracker-retention` を使う。`tracker-retention` は重複判定の期限ではなく、DB肥大化を抑えるための保存期間。
- IDベース追跡では、DBに残っている限り古いIDも重複判定・引用解決に使う。
- DBファイルの置き場所をコンテナ内だけにすると、コンテナ再作成で消える。Docker ComposeとKubernetesでは永続volumeが必要。
- 外部投稿成功後にDB保存が失敗した場合、その投稿自体は取り消せない。ここはログとメトリクスで検知できるようにする。
- 将来的に複数replicaで動かす場合、各replicaが別sqliteを持つと重複防止にならない。共有ストレージ上のsqliteもロック面で注意が必要なので、複数replica前提ならPostgreSQLなど別DBを検討する。

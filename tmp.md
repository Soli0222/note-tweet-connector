# リプライ転送をスキップする実装方針

## 現状

- Misskey -> Twitter は `internal/handler/note2tweet.go` の `Note2TweetHandlerWithConfig` で処理している。
- Twitter -> Misskey は `internal/handler/tweet2note.go` の `Tweet2NoteHandlerWithConfig` / `HandleIncomingTweetWithConfig` で処理している。
- どちらも現在は「公開投稿か」「localOnlyか」「CrossPostTracker登録済みか」「転送ループっぽい本文か」は見ているが、リプライかどうかは見ていない。

## 方針

Misskey側・Twitter側とも、payloadからリプライ元IDを取り込み、投稿処理やメディアアップロードより前にスキップする。

スキップ理由のmetric labelは両方向とも `reply` にそろえる。

## Misskey -> Twitter

1. `internal/handler/note2tweet.go` の `payloadNoteData.Body.Note` にリプライ判定用フィールドを追加する。

```go
ReplyID string `json:"replyId"`
Reply   struct {
    ID string `json:"id"`
} `json:"reply"`
```

2. `Note2TweetHandlerWithConfig` で `noteID` の空チェック、CrossPostTrackerチェック、公開範囲チェック、`localOnly` チェックの後あたりに、リプライならスキップする条件を入れる。

```go
if payload.Body.Note.ReplyID != "" || payload.Body.Note.Reply.ID != "" {
    slog.Info("Note is a reply, skipping",
        slog.String("note_id", noteID),
        slog.String("reply_id", payload.Body.Note.ReplyID))
    m.Note2TweetSkipped.WithLabelValues("reply").Inc()
    return nil
}
```

3. この位置に置く理由:

- `noteID` が無いpayloadは既存どおり `missing_id` として扱える。
- 非公開・localOnlyは既存理由のまま記録できる。
- リプライ本文の整形、画像抽出、Twitter投稿の前に止められる。

## Twitter -> Misskey

1. `internal/handler/tweet2note.go` の `IncomingTweet` にリプライ元IDを追加する。

```go
InReplyToTweetID string
```

2. 同じファイルの `tweetObject` に Account Activity payload のリプライ元IDを追加する。

```go
InReplyToStatusIDStr string `json:"in_reply_to_status_id_str"`
```

必要ならユーザーIDも後でログ用途に足せる。

```go
InReplyToUserIDStr string `json:"in_reply_to_user_id_str"`
```

3. `parseAccountActivityPayloadWithConfig` で `IncomingTweet` を作るときに詰める。

```go
InReplyToTweetID: event.InReplyToStatusIDStr,
```

4. `HandleIncomingTweetWithConfig` で `tweet.ID` の空チェック、CrossPostTrackerチェックの後あたりに、リプライならスキップする条件を入れる。

```go
if tweet.InReplyToTweetID != "" {
    slog.Info("Tweet is a reply, skipping",
        slog.String("tweet_id", tweet.ID),
        slog.String("in_reply_to_tweet_id", tweet.InReplyToTweetID))
    m.Tweet2NoteSkipped.WithLabelValues("reply").Inc()
    return nil
}
```

5. この位置に置く理由:

- ID欠落と重複は既存理由で扱える。
- `RT @` や `RN [at]` の本文パターン判定より前に止めても問題ない。
- Misskey Driveへの画像アップロードやMisskey投稿の前に止められる。

## テスト

最低限、次のテストを追加する。

1. `internal/handler/note2tweet_test.go`
   - `replyId` がある公開ノートを渡したとき、`postTweet` が呼ばれず `note2tweet_skipped_total{reason="reply"}` が1増えること。
   - できれば `reply.id` だけがあるpayloadでもスキップされること。

2. `internal/handler/tweet2note_test.go`
   - `in_reply_to_status_id_str` があるtweetを渡したとき、`createMisskeyNoteWithOptions` が呼ばれず `tweet2note_skipped_total{reason="reply"}` が1増えること。
   - `parseAccountActivityPayload` が `InReplyToTweetID` を保持すること。

既存テストでは `prometheus/testutil` を使っているので、それに合わせる。

## README更新

`README.md` の動作仕様を更新する。

- MisskeyからTwitter:
  - `replyId` または `reply` があるノートはスキップします。
- TwitterからMisskey:
  - `in_reply_to_status_id_str` があるtweetはスキップします。

このリポジトリの表記ルールに合わせ、サービス名は `Twitter`、投稿は `Tweet` / `tweet` を使う。

## 完了確認

実装後に必ず実行する。

```bash
go test ./...
golangci-lint run
```

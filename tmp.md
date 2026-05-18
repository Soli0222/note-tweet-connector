# Filtered Stream 切り替え実装方針

## 方針

Twitter から Misskey への受信経路を、Account Activity webhook から `GET /2/tweets/search/stream` の Filtered Stream 永続接続へ切り替える。既存の Twitter webhook 受信機能は互換維持せず削除する。

公式ドキュメント上、Filtered Stream は Pay-per-use で 1 connection / 1,000 rules まで使える。一方で Account Activity webhook はドキュメント上の扱いが不安定で、subscription が true でも delivery が止まる現象の切り分けが難しい。運用上は、アプリ側が自分で永続接続を張り、切断を検知して再接続する構成に寄せる。

参照:

- https://docs.x.com/x-api/posts/filtered-stream/introduction
- https://docs.x.com/x-api/posts/filtered-stream/get-posts-search-stream
- https://docs.x.com/x-api/posts/filtered-stream/update-stream-rules

## 非対応にするもの

- `GET /twitter/webhook` の CRC 応答
- `POST /twitter/webhook` の Account Activity payload 受信
- `x-twitter-webhooks-signature` 検証
- Account Activity subscription 作成・確認手順
- Filtered Stream の webhook delivery
- Activity API

## 新しい受信構成

1. 起動時に Twitter Stream client を初期化する。
2. 必要な Filtered Stream rule が登録されているか確認する。
3. 不足していれば rule を追加する。
4. `GET /2/tweets/search/stream` に永続接続する。
5. 1 行ごとに JSON を読み、v2 Tweet payload から `handler.IncomingTweet` に変換する。
6. 既存の `handler.HandleIncomingTweetWithConfig` を呼び出して Misskey へ投稿する。
7. keep-alive が途絶えた場合、HTTP エラー、JSON parse エラー、EOF、context cancel で接続を閉じ、backoff 付きで再接続する。

## 設定

追加するフラグ:

- `-twitter-bearer-token`: Filtered Stream と stream rule 管理に使う Application-Only Bearer Token。
- `-twitter-stream-reconnect-min`: 再接続 backoff の初期値。例: `5s`。
- `-twitter-stream-reconnect-max`: 再接続 backoff の上限。例: `5m`。

既存フラグの扱い:

- `-twitter-webhook-consumer-secret` は削除する。
- `-twitter-username` は Stream rule 生成と Tweet URL 生成の fallback に使うため必須として残す。
- OAuth 2.0 Authorization Code Flow with PKCE は、Media API upload 用として残す。
- OAuth 1.0a API key / access token は、Misskey から Twitter へ Tweet 投稿するため残す。

`compose.yaml` と README の環境変数も同じ方針で更新する。

## パッケージ構成

追加候補:

- `internal/twitter/stream.go`
- `internal/twitter/stream_test.go`
- `internal/handler/filtered_stream.go`
- `internal/handler/filtered_stream_test.go`

`internal/twitter` は Twitter API との HTTP 通信を担当する。

- rule 一覧取得
- rule 追加
- stream 接続
- keep-alive 監視
- reconnect loop

`internal/handler` は Twitter v2 stream payload から `IncomingTweet` への変換を担当する。

既存の `handler.HandleIncomingTweetWithConfig` はそのまま使い、Account Activity 固有の parse 関数は置き換える。

## Stream rule 管理

初回実装では、アプリが所有する固定 tag の rule を 1 つだけ管理する。

- `GET /2/tweets/search/stream/rules` で既存 rule を取得する。
- `tag == note-tweet-connector` かつ `value == from:<twitter-username> -is:retweet -is:reply` の rule があれば何もしない。
- 同じ tag で異なる rule がある場合は、古い rule を delete してから新しい rule を add する。
- 同じ Project に他用途の rule がある可能性があるため、tag が違う rule は触らない。

デフォルト rule:

```text
from:<twitter-username> -is:retweet -is:reply
```

## Stream request のパラメータ

最低限必要な query:

```text
tweet.fields=author_id,attachments,entities,referenced_tweets,in_reply_to_user_id,edit_history_tweet_ids
expansions=author_id,attachments.media_keys,referenced_tweets.id,referenced_tweets.id.author_id
user.fields=username
media.fields=type,url,preview_image_url
```

変換方針:

- `data.id` を `IncomingTweet.ID` にする。
- `data.text` を `IncomingTweet.Text` にする。
- `data.author_id` を `IncomingTweet.UserID` にする。
- `includes.users` の `id == data.author_id` から `username` を引き、`IncomingTweet.Username` にする。
- username が取れない場合は `-twitter-username` を使う。
- `attachments.media_keys` と `includes.media` を突き合わせ、`type == "photo"` の `url` を `IncomingTweet.MediaURLs` に入れる。
- `referenced_tweets.type == "quoted"` を `IncomingTweet.QuotedTweetID` にする。
- quoted Tweet の author は `includes.tweets` と `includes.users` から可能なら解決する。
- `referenced_tweets.type == "replied_to"` または `in_reply_to_user_id` があれば `IncomingTweet.InReplyToTweetID` に入れる。
- Tweet URL は既存の `https://twitter.com/<username>/status/<tweet_id>` 形式を維持する。

## HTTP サーバーの変更

削除するもの:

- `server.twitterWebhookHandler`
- `twitterResponseToken`
- `verifyTwitterSignature`
- `twitterHMAC`
- `/twitter/webhook` route
- Twitter webhook 関連の main tests

残すもの:

- `/` Misskey webhook
- `/twitter/login`
- `/twitter/callback`
- `/healthz`
- `/metrics`

起動処理:

- `main()` で `twitter.StreamClient` を生成する。
- HTTP server と metrics server を起動した後、同じ root context で stream worker goroutine を起動する。
- SIGINT / SIGTERM 時は context cancel で stream 接続も止める。
- stream worker が fatal な設定エラーを返す場合は起動時に失敗させる。
- 一時的な接続エラーはログとメトリクスに出して再接続する。

## メトリクス

既存の `tweet2note_*` は流用する。

追加候補:

- `twitter_stream_connects_total{status}`
- `twitter_stream_disconnects_total{reason}`
- `twitter_stream_messages_total{status}`
- `twitter_stream_last_message_timestamp_seconds`
- `twitter_stream_rule_updates_total{action,status}`

削除または意味変更:

- `webhook_requests_total{source="twitter"}`
- `webhook_requests_total{source="twitter_crc"}`
- `webhook_request_errors_total{source="twitter"}`
- `webhook_request_errors_total{source="twitter_crc"}`

README では、Twitter から Misskey 方向は webhook request metrics ではなく stream metrics を見るように書き換える。

## エラー処理と再接続

- 空行 keep-alive を受信したら last activity を更新する。
- 20 秒以上データも keep-alive も来ない場合は接続を切って再接続する。
- 429 は rate limit として `x-rate-limit-reset` があればその時刻まで待つ。なければ max backoff を使う。
- 5xx / network error / EOF は指数 backoff で再接続する。
- 401 / 403 は bearer token や plan の問題なので error log を強く出す。短時間 retry でログを埋めないよう max backoff に寄せる。
- context cancel は正常終了として扱う。

## 既存仕様との対応

維持する仕様:

- CrossPostTracker に登録済みの tweet は skip。
- reply tweet は skip。
- `RN [at]` で始まる tweet は skip。
- `RT @` で始まる tweet は元 tweet URL を本文末尾に追記。
- photo は最大 4 件まで Misskey Drive へ upload。
- 同一作者の引用 tweet は、tracker に対応があれば Misskey の renote として作成。
- tweet ID が空の payload は skip。

変わる可能性がある仕様:

- Account Activity payload の `extended_tweet.full_text` はなくなる。v2 stream の `data.text` を正とする。
- v2 stream では media URL や quoted author の hydration が query parameter に依存する。
- retweet は rule で除外する方針のため、現行の `RT @` 補正は主に互換・保険として残る。

## README 更新

README は必ず更新する。

削除・置換する内容:

- Account Activity webhook の説明
- Twitter webhook 登録手順
- Account Activity subscription 作成手順
- `-twitter-webhook-consumer-secret`
- `/twitter/webhook` endpoint
- CRC / signature 検証の説明

追加する内容:

- Filtered Stream を使うこと
- `-twitter-bearer-token` の用途
- stream rule の生成仕様
- Pay-per-use では stream 接続 1 本が前提であること
- stream worker の再接続挙動
- 運用時に見るべき stream metrics

## テスト方針

追加する unit test:

- Filtered Stream payload から `IncomingTweet` へ変換できる。
- `includes.users` から username を解決できる。
- username がない場合に `-twitter-username` fallback を使う。
- photo media の URL を抽出できる。
- quoted Tweet ID と quoted author を解決できる。
- reply Tweet を `IncomingTweet.InReplyToTweetID` 付きで渡せる。
- rule 管理が既存一致時に no-op になる。
- 同じ tag の古い rule を delete して新 rule を add する。
- stream keep-alive timeout で reconnect する。
- context cancel で正常終了する。

削除・更新する test:

- CRC response token の test は削除。
- signature verification の test は削除。
- Account Activity parser test は Filtered Stream parser test へ置換。
- `testdata/account_activity_tweet.json` は `testdata/filtered_stream_tweet.json` へ置換。

完了条件:

```bash
go test ./...
golangci-lint run
```

## 実装順

1. Config と README の用語を Account Activity webhook から Filtered Stream に切り替える。
2. `internal/handler` に Filtered Stream payload parser を追加し、既存の tweet2note 処理に接続する。
3. `internal/twitter` に stream rule client を追加する。
4. `internal/twitter` に stream reader と reconnect loop を追加する。
5. `main()` から `/twitter/webhook` と webhook helper を削除し、stream worker を起動する。
6. metrics を stream 向けに追加・README に反映する。
7. Account Activity webhook 関連 test / testdata を削除または置換する。
8. `go test ./...` と `golangci-lint run` を通す。

## 運用移行手順

1. Twitter Developer Portal で Pay-per-use の Project/App の Bearer Token を確認する。
2. 既存の Twitter webhook と Account Activity subscription は削除する。
3. デプロイ設定から `TWITTER_WEBHOOK_CONSUMER_SECRET` を削除する。
4. `TWITTER_BEARER_TOKEN` を追加する。
5. `TWITTER_USERNAME` を設定する。
6. 新版を deploy し、起動ログで rule 同期と stream 接続成功を確認する。
7. テスト tweet を投稿し、`tweet2note_success_total` と Misskey 投稿を確認する。
8. `twitter_stream_last_message_timestamp_seconds` が更新されることを確認する。

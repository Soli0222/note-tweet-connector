# Discord 通知の実装方針

## 目的

Note Tweet Connector が運用者の対応を必要とする状態になったとき、Discord に通知する。

現状は Twitter OAuth 2.0 の再認証要求などがログにだけ出ており、短時間で失効する login URL や継続的な失敗を見逃す可能性がある。今回の変更では、OAuth 再認証だけでなく、投稿・stream・Misskey API の重大な失敗も Discord に通知する。

## 今回のスコープ

実装する通知:

- Twitter OAuth 2.0 の再認証要求。
- Twitter OAuth 2.0 の再認証成功 recovery。
- Twitter POST 失敗。
- Twitter media upload 失敗。
- Twitter stream disconnect loop。
- Misskey API 失敗。

共通方針:

- 既存のログ出力は残す。
- Discord webhook は任意設定にする。未設定なら通知せず、アプリは今まで通り動く。
- access token / refresh token / Misskey token / Discord webhook URL は通知にもログにも出さない。
- Discord の user mention / role mention は使わない。
- Discord 通知に失敗しても、元の処理結果は変えない。

今回やらないこと:

- Grafana Alerting 連携。
- 通知の永続キューや再送機構。
- Discord bot 認証。Incoming Webhook URL だけを使う。

## 設定

追加する flag:

- `-discord-webhook-url`
  - 環境変数例: `DISCORD_WEBHOOK_URL`
  - 未指定なら Discord 通知を無効化する。
- `-discord-notify-timeout`
  - デフォルト: `5s`
  - Discord webhook への HTTP request timeout として使う。
- `-discord-stream-loop-window`
  - デフォルト: `10m`
  - Twitter stream disconnect loop 判定の集計窓。
- `-discord-stream-loop-threshold`
  - デフォルト: `5`
  - 集計窓内にこの回数以上 disconnect したら通知する。
- `-discord-error-dedupe-window`
  - デフォルト: `10m`
  - 同種エラーの Discord 通知を抑制する期間。

更新するドキュメント・設定例:

- `compose.yaml`
- `README.md` の flag 一覧
- `README.md` の環境変数一覧
- `README.md` の Twitter OAuth 2.0 運用説明
- `README.md` の Discord 通知説明

Discord webhook URL は secret として扱う。ログやエラーメッセージに URL 全体を出さない。

## 設計

通知用 package を追加する。候補は `internal/notify`。

想定 interface:

```go
type Notifier interface {
    Notify(ctx context.Context, event Event) error
}

type Event struct {
    Kind      EventKind
    Severity  Severity
    Title     string
    Message   string
    Fields    []Field
    DedupeKey string
}

type Field struct {
    Name  string
    Value string
}
```

実装:

- `NoopNotifier`
  - Discord webhook URL 未設定時に使う。
- `DiscordNotifier`
  - Discord Incoming Webhook に JSON payload を POST する。
- `DedupeNotifier`
  - 同じ `DedupeKey` の通知を一定時間抑制する。

Discord payload は simple embed を使う。

- `title`: 何が起きたか。
- `description`: 短い説明と必要な action。
- `fields`: login URL、有効期限、status code、note ID、tweet ID、reason など。
- `color`: severity ごとに変える。

mention は使わない。通知先 channel 側の設定で気づけるようにする。

## 通知対象

### Twitter OAuth 2.0 再認証要求

発火条件:

- `twitter.ErrAuthorizationRequired` が返った場合。
- 起動時に `tokenManager.AuthorizationRequired()` が true の場合。
- OAuth callback 失敗で login をやり直す必要がある場合。

通知内容:

- login URL。
- login URL の有効期限。
- 発生理由が取れる場合は理由。

重複抑制:

- 同じ login URL に対する通知は、有効期限内に 1 回だけ送る。
- warning log は今まで通りイベントごとに出す。

例:

```text
Title: Twitter OAuth 2.0 の再認証が必要です
Login URL: https://...
有効期限: 2026-05-21T14:22:13Z
```

### Twitter OAuth 2.0 再認証成功 recovery

発火条件:

- `twitterCallbackHandler` で `CompleteLogin` が成功した直後。

通知内容:

- 再認証が完了したこと。
- 完了時刻。

失敗時の扱い:

- Discord 通知に失敗しても OAuth callback は成功扱いにする。
- 失敗は warning log に残す。

例:

```text
Title: Twitter OAuth 2.0 の再認証が完了しました
完了時刻: 2026-05-21T14:18:11Z
```

### Twitter POST 失敗

対象:

- `postTweet` の non-2xx response。
- Tweet 作成 request の送信失敗。
- Tweet 作成 response の parse 失敗。

通知内容:

- status code があれば status code。
- Twitter API response body の短い preview。
- note ID が呼び出し元で取れる場合は note ID。
- media count。
- quote tweet ID があれば quote tweet ID。

出さないもの:

- Tweet 本文全文。
- access token。
- request body 全体。

重複抑制:

- `kind=twitter_post_failed,status=<status>,note_id=<note_id>` を基本に dedupe する。
- note ID が取れない場合は `status` と error class で dedupe する。
- デフォルト dedupe window は 10 分。

実装メモ:

- 現状は `internal/twitter` 内で note ID を知らないため、通知は `handler.Note2TweetHandlerWithConfig` 側で行うのが自然。
- `twitter.PostWithOptionsConfig` の error に status code や preview を取り出せる typed error を追加すると扱いやすい。

### Twitter media upload 失敗

対象:

- media upload の INIT / APPEND / FINALIZE / STATUS / simple upload の non-2xx response。
- media download 失敗。
- media processing failed。
- media processing timeout。

通知内容:

- upload command。
- status code があれば status code。
- note ID が取れる場合は note ID。
- media count または media index。
- error preview。

出さないもの:

- access token。
- media URL 全体。必要なら host のみ。
- request body。

重複抑制:

- `kind=twitter_media_upload_failed,command=<command>,status=<status>,note_id=<note_id>` を基本に dedupe する。

実装メモ:

- media upload 失敗も最終的には `Note2TweetHandlerWithConfig` に error として返る。
- typed error に command / status / preview を持たせ、handler 側で Discord event に変換する。

### Twitter stream disconnect loop

対象:

- `runTwitterStream` 内で disconnect が短時間に連続する状態。

発火条件:

- `-discord-stream-loop-window` 内に disconnect が `-discord-stream-loop-threshold` 回以上発生した場合。
- デフォルトは 10 分以内に 5 回以上。

通知内容:

- 直近 window。
- disconnect 回数。
- 最新 reason。
- 最新 error preview。
- 次回 reconnect backoff。

重複抑制:

- loop 状態が続いている間は `-discord-error-dedupe-window` に従って抑制する。
- 単発 disconnect では通知しない。

実装メモ:

- `runTwitterStream` に小さな tracker を持たせる。
- disconnect timestamp の ring buffer または slice を持ち、window 外を捨てる。
- threshold 到達時に notifier へ event を送る。

### Misskey API 失敗

対象:

- Misskey note 作成失敗。
- Misskey Drive upload 失敗。
- Misskey API response parse 失敗。

通知内容:

- 操作種別: create note / upload drive file。
- status code があれば status code。
- tweet ID が取れる場合は tweet ID。
- media count または media index。
- error preview。

出さないもの:

- Misskey token。
- request body 全体。
- media URL 全体。必要なら host のみ。

重複抑制:

- `kind=misskey_api_failed,operation=<operation>,status=<status>,tweet_id=<tweet_id>` を基本に dedupe する。
- tweet ID が取れない場合は operation / status / error class で dedupe する。

実装メモ:

- `handler.Tweet2NoteHandlerWithConfig` 側で通知する。
- `internal/misskey` の error を typed error にすると status code と response preview を扱いやすい。

## 組み込み方針

`cmd/note-tweet-connector/main.go`:

- Discord 設定 flag を追加する。
- notifier を初期化する。
- server struct に notifier を持たせる。
- `authorizationLoggingTokenSource` に notifier を渡す。
- `runTwitterStream` に notifier と loop 判定設定を渡す。

`internal/handler`:

- `Note2TweetHandlerWithConfig` で Twitter POST / media upload 失敗を通知する。
- `Tweet2NoteHandlerWithConfig` で Misskey API 失敗を通知する。
- handler config に notifier を追加する。

`internal/twitter`:

- Twitter API 失敗を typed error にする。
- status code、operation、response preview を取得できるようにする。
- token や request body を error に含めない。

`internal/misskey`:

- Misskey API 失敗を typed error にする。
- status code、operation、response preview を取得できるようにする。
- token や request body を error に含めない。

## 失敗時の扱い

Discord 通知の失敗で本来の処理を長時間止めない。

挙動:

- Discord が non-2xx を返したら error にする。
- error には status code と短い response body preview だけを含める。
- webhook URL は error に含めない。
- network error はそのまま返す。
- caller は `Failed to send Discord notification` のような warning log を出す。
- 元の OAuth / Twitter / Misskey error は今まで通り呼び出し元へ返す。

## テスト方針

追加する unit test:

- Discord notifier が test server に期待する JSON を送る。
- Discord notifier が non-2xx を error にする。
- Discord notifier の error に webhook URL が漏れない。
- webhook URL 未設定時の no-op が成功する。
- dedupe notifier が同じ key を window 内で抑制する。
- 再認証要求時に login URL と expires_at を含む通知が 1 回送られる。
- 同じ未失効 login URL に対する再認証要求では Discord 通知が重複しない。
- 再認証成功時に recovery 通知が送られる。
- recovery 通知失敗時も OAuth callback は成功 response を返す。
- Twitter POST 失敗時に note ID と status code を含む通知が送られる。
- Twitter media upload 失敗時に command と status code を含む通知が送られる。
- Twitter stream disconnect loop が threshold 到達時だけ通知される。
- Misskey note 作成失敗時に tweet ID と status code を含む通知が送られる。
- Misskey Drive upload 失敗時に operation と status code を含む通知が送られる。

コード変更完了前に必ず実行するもの:

```sh
go test ./...
golangci-lint run
```

## README 更新内容

README には次を追加する。

- Discord webhook URL を設定すると重大な運用イベントが Discord に通知されること。
- 通知対象:
  - Twitter OAuth 2.0 再認証要求。
  - Twitter OAuth 2.0 再認証成功。
  - Twitter POST / media upload 失敗。
  - Twitter stream disconnect loop。
  - Misskey API 失敗。
- OAuth 再認証要求で通知される login URL は短命であること。
- `DISCORD_WEBHOOK_URL` は secret として管理すること。
- Discord 通知が失敗してもアプリ本体の処理は継続すること。
- Discord の user mention / role mention は使わないこと。
- stream disconnect は単発では通知せず、loop 判定時だけ通知すること。

## Rollout 手順

1. `DISCORD_WEBHOOK_URL` 未設定で deploy し、既存挙動が変わらないことを確認する。
2. Kubernetes secret に Discord webhook URL を追加する。
3. Helm values または Deployment args に `-discord-webhook-url=$(DISCORD_WEBHOOK_URL)` を追加する。
4. staging または一時環境で token store がない状態を作り、OAuth 再認証通知を確認する。
5. OAuth callback 完了後に recovery 通知が届くことを確認する。
6. fake Discord webhook または test endpoint で Twitter / Misskey 失敗通知の payload を確認する。
7. stream disconnect loop の threshold を一時的に下げ、loop 通知が単発 disconnect では発火しないことを確認する。

## 決定事項

- Discord メッセージで user mention / role mention は使わない。
- 再認証成功時にも recovery 通知を送る。
- Twitter POST 失敗、Twitter media upload 失敗、Twitter stream disconnect loop、Misskey API 失敗も今回の実装スコープに含める。

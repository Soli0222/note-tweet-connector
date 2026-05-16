# IFTTT撤廃に向けた改修方針

## 背景

現状の `note-tweet-connector` は、IFTTTを以下の2箇所で利用している。

- Note -> Tweet
  - 画像付きノートは既にTwitter APIで投稿している。
  - テキストのみノートは `internal/twitter/client.go` の `Post` でIFTTT Maker Webhookへ送っている。
- Tweet -> Note
  - `cmd/note-tweet-connector/main.go` が `User-Agent: IFTTT-Hooks` と `X-IFTTT-Hook-Secret` を見てIFTTTからのwebhookを受け、`internal/handler/tweet2note.go` へ渡している。

公式にはX APIと呼ばれているが、このアプリケーションでは意図的に `twitter` / `tweet` という旧名を採用する。既存パッケージ名 `internal/twitter`、ハンドラ名 `Note2Tweet` / `Tweet2Note`、メトリクス名 `note2tweet_*` / `tweet2note_*` と整合するため、実装上の識別子・環境変数・メトリクスラベル・エンドポイント名には `x` を使わない。

Twitter APIの以下の機能を使えば、このIFTTT依存は撤廃できる。

- `POST /2/tweets`: テキスト投稿とメディア付き投稿を同じエンドポイントで作成できる。
- Media API: `media_id` を取得し、`POST /2/tweets` の `media.media_ids` に渡せる。
- V2 Webhooks API + Account Activity API: Twitter上の対象アカウントのTweet作成イベントをwebhookで受け取れる。CRCと署名検証が必要。

参照:

- https://docs.x.com/x-api/posts/create-post
- https://docs.x.com/x-api/webhooks/introduction
- https://docs.x.com/x-api/account-activity/introduction
- https://docs.x.com/x-api/activity/introduction
- https://docs.x.com/x-api/webhooks/stream/introduction
- https://docs.x.com/x-api/media/introduction
- https://docs.x.com/x-api/media/quickstart/media-upload-chunked
- https://docs.x.com/x-api/webhooks/quickstart

## 目標

IFTTTを経由せず、このアプリだけで以下を完結させる。

- Misskeyの公開ノートをTwitterへ投稿する。
- Twitterで投稿された対象ユーザーのTweetを、Pay Per Useで利用できるAccount Activity API経由でMisskeyへ保存する。
- 画像付きノートもテキストのみノートも同じ投稿処理に統一する。
- IFTTT用の環境変数、受信分岐、テストデータ、README記述を削除または移行する。

## 改修案

### 1. Note -> Tweet投稿をTwitter API v2に統一する

`internal/twitter/client.go` の `Post` をIFTTT実装からTwitter API実装へ置き換える。

現在:

- `Post(ctx, text)` は `IFTTT_EVENT` と `IFTTT_KEY` を読み、IFTTTへ `{"value1": text}` をPOSTする。
- `PostWithMedia(ctx, text, fileURLs)` はOAuth 1.0aでmedia upload後、`POST /2/tweets` に投稿する。

変更後:

- `Post(ctx, text)` は `POST /2/tweets` に `{"text": text}` を送る。
- `PostWithMedia(ctx, text, fileURLs)` はメディアアップロード後、同じ `POST /2/tweets` に `{"text": text, "media": {"media_ids": [...]}}` を送る。
- さらに `Post(ctx, text)` と `PostWithMedia(ctx, text, fileURLs)` の内部を共通化し、最終的には `Post(ctx, text, fileURLs)` のような1本の関数に寄せる。

Create Post APIは、リクエスト本文に少なくとも `text` または `media` を持たせる。メディア付き投稿は `media.media_ids` を1から4件渡せるため、現行の最大4枚制限と相性が良い。

### 2. Media APIをv2 uploadへ寄せる

現状は `https://upload.twitter.com/1.1/media/upload.json` のsimple uploadを使っている。現在のMedia APIでは、`POST https://api.x.com/2/media/upload` のchunked uploadが推奨されている。

変更方針:

- まずは現行の画像のみ対応を維持するなら、既存のsimple uploadを短期的に残してもよい。
- IFTTT撤廃を機に整理するなら、v2 Media APIへ移行する。
- v2 chunked uploadは `INIT` -> `APPEND` -> `FINALIZE` -> 必要なら `STATUS` の順で処理する。
- `media_category` は画像なら `tweet_image`、GIFなら `tweet_gif`、動画なら `tweet_video` を使う。
- Misskey添付の `type` を見て、当面は `image/*` のみ投稿対象にする。GIF/動画は別タスクで対応してもよい。

### 3. Tweet -> Note受信をTwitter Webhooks + Account Activity APIへ置き換える

IFTTTからのPOST受信分岐を、Twitter Webhooks用のエンドポイントに置き換える。Pay Per Use前提では、対象ユーザーの投稿イベントはAccount Activity APIで受ける。

確認したこと:

- V2 Webhooks APIが対応する配信元は `X Activity API (XAA)`、`Account Activity API (AAA)`、`Filtered Stream Webhooks` の3つ。
- Account Activity APIは、特定ユーザーアカウントを事前登録したwebhookにsubscribeし、そのユーザーに関するTweet、Tweet削除、mentions、replies、reposts、likes、follows、DMなどを配信する。
- Account Activity APIのFeature summaryでは、Pay Per UseはUnique Subscriptionsが3、Webhooksが1。
- Account Activity APIのpayloadには `tweet_create_events` があり、Tweets、Retweets、Replies、mentions、Quote Tweetsなどが入る。
- X Activity APIはprofile/follow/space/DM/chat/news系イベントが中心で、ドキュメント上もTweet配信はしないため、この用途では採用しない。
- Filtered Stream Webhooksはpublic Tweetsをルールで絞ってwebhook配信する仕組みだが、該当ページはEnterprise扱いで、今回のPay Per Use前提では採用しない。

したがって、Tweet -> Noteの本線は以下にする。

1. V2 Webhooks APIでwebhook URLを登録する。
2. Account Activity APIで対象Twitterアカウントをそのwebhookへsubscribeする。
3. `tweet_create_events` から対象ユーザー自身の通常Tweetを抽出し、Misskeyへ保存する。
4. Replies、mentions、reposts、quotesなど、現在の運用で不要なイベントはparserまたはhandlerで除外する。

追加する処理:

- `GET /twitter/webhook`
  - `crc_token` クエリを受け取る。
  - Twitter AppのConsumer SecretをキーにHMAC-SHA256を計算する。
  - base64エンコードし、`{"response_token":"sha256=..."}` を返す。
- `POST /twitter/webhook`
  - 生のリクエストbodyを読み取る。
  - `x-twitter-webhooks-signature` ヘッダーを検証する。
  - 検証成功後、Account Activity APIのwebhook payloadを `Tweet2NoteHandler` に渡せる内部形式へ変換する。
  - 10秒以内に `200 OK` を返せるよう、Misskey投稿が重い場合はジョブ化も検討する。

Webhook要件:

- HTTPSで公開されていること。
- URLにポート番号を含めないこと。
- CRCへ正しく応答すること。
- POSTイベントの署名検証を行うこと。

### 4. Tweet2Noteの入力形式をIFTTT形式からTwitter形式へ変更する

現状の `payloadTweetData` はIFTTTの整形済みJSONに依存している。

```json
{
  "body": {
    "tweet": {
      "text": "...",
      "url": "https://twitter.com/user/status/..."
    }
  }
}
```

Account Activity APIでは `tweet_create_events` にTweet Objectが入るため、以下を行う。

- Account Activity API payload用の構造体を追加する。
- Tweet ID、本文、ユーザー情報を取り出す。
- URLは `https://twitter.com/{username}/status/{tweet_id}` 形式で組み立てる。
- 既存の `Tweet2NoteHandler` は「外部payloadをparseする責務」と「Misskeyへ保存する責務」が混ざっているため、内部モデルを挟む。

例:

```go
type IncomingTweet struct {
    ID       string
    Text     string
    Username string
    URL      string
}
```

その上で、

- `HandleIncomingTweet(ctx, IncomingTweet, tracker, metrics)` を新設する。
- IFTTT用 `parseTweetPayload` は削除する。
- Account Activity API parserは `IncomingTweet` を返す。

これにより、テストしやすくなり、将来payload形式が変わっても影響範囲を抑えられる。

### 5. Webhook登録・再検証の運用を追加する

Twitter Webhooks APIには登録・一覧・削除・再検証のエンドポイントがある。Account Activity APIでは、このwebhookに対してユーザーsubscriptionを作成する。

- `POST /2/webhooks`: webhook登録
- `GET /2/webhooks`: 登録済みwebhook一覧
- `DELETE /2/webhooks/:webhook_id`: 削除
- `PUT /2/webhooks/:webhook_id`: CRC再検証と再有効化
- `POST /2/webhooks/replay`: replay job作成
- Account Activity APIのsubscription作成・確認・削除

READMEに手順を記載する。

### 6. 環境変数を整理する

削除候補:

- `IFTTT_EVENT`
- `IFTTT_KEY`
- `IFTTT_HOOK_SECRET`

継続利用:

- `API_KEY`
- `API_KEY_SECRET`
- `ACCESS_TOKEN`
- `ACCESS_TOKEN_SECRET`

追加候補:

- `TWITTER_WEBHOOK_CONSUMER_SECRET`
  - CRCと署名検証用。既存の `API_KEY_SECRET` と同じ値を使うなら別名は不要だが、用途が分かる名前にしておくと安全。
- `TWITTER_WEBHOOK_SOURCE`
  - webhookのsource label用。メトリクス上は `ifttt` ではなく `twitter` にする。
- `TWITTER_USERNAME`
  - webhook payloadからusernameが取れない場合にURL組み立てや自己投稿判定で使う。

### 7. メトリクスとログのラベルを変更する

現状:

- `webhook_requests_total{source="ifttt", ...}`
- `webhook_request_duration_seconds{source="ifttt"}`
- `webhook_request_errors_total{source="ifttt", ...}`

変更後:

- `source="twitter"` を使う。
- CRC用GETは通常イベントと分け、`source="twitter_crc"` または `source="twitter", status="crc_success"` のように区別する。
- 署名検証失敗は `error_type="signature"` として記録する。

Grafana dashboard内の `ifttt` 表示や説明文も更新する。

### 8. READMEとデプロイ設定を更新する

READMEからIFTTT前提を削除し、以下へ差し替える。

- Twitter Developer Appの作成
- User Access Tokenの設定
- Twitter Webhook URLの公開要件
- CRC対応済みエンドポイントの説明
- webhook登録手順
- Misskey webhook設定
- Note -> Tweet、Tweet -> Misskey Noteの流れ

Docker ComposeやHelm valuesにIFTTT系環境変数があれば削除し、Twitter webhook用のsecretへ置き換える。

### 9. テスト方針

追加・更新するテスト:

- `twitter.Post` が `POST /2/tweets` に正しいJSONを送ること。
- メディアなしでもOAuth付きTwitter API投稿になること。
- メディアありで `media.media_ids` が最大4件になること。
- CRCレスポンスが `sha256=<base64 hmac>` 形式になること。
- POST webhookの署名検証が成功・失敗を正しく判定すること。
- Account Activity APIの `tweet_create_events` から `IncomingTweet` へ変換できること。
- 既存の重複排除、`RT @`/`RN [at]` スキップ挙動が維持されること。

削除・置換するテスト:

- `testdata/account_activity_tweet.json` にAccount Activity API payloadのfixtureを置く。
- IFTTTのUser-Agentやsecretを前提にしたmain handlerテストがあればTwitter webhook用へ変更する。

## 実装ステップ

1. `internal/twitter/client.go` の `Post` をTwitter API v2投稿へ変更し、`IFTTT_EVENT`/`IFTTT_KEY` 依存を削除する。
2. `Note2TweetHandler` からは、テキストのみ・画像付きのどちらもTwitter API投稿処理を呼ぶように整理する。
3. `cmd/note-tweet-connector/main.go` にTwitter webhook用のGET CRC処理とPOST署名検証処理を追加する。
4. `internal/handler/tweet2note.go` を `IncomingTweet` ベースに分割し、IFTTT payload依存を外す。
5. Account Activity APIの `tweet_create_events` fixtureを追加し、Tweet -> Noteのテストを更新する。
6. README、Docker Compose、Helm向けドキュメント、Grafana dashboardのIFTTT表記を更新する。
7. 必要に応じてMedia API v2 chunked uploadへ移行する。

## 注意点

- Pay Per Use前提では、Tweet -> Noteの受信はAccount Activity APIを採用する。Feature summary上、Pay Per UseはUnique Subscriptions 3、Webhooks 1なので、このアプリの「自分のTwitterアカウントを1つsubscribeする」用途には収まる。
- X Activity APIはTweet配信をしないため採用しない。Filtered Stream WebhooksはEnterprise向けなので、Pay Per Use運用では本線にしない。
- Create Postの認証はユーザーコンテキストのアクセストークンが必要。Webhook管理エンドポイントはApp Only Bearer Tokenが必要とされているため、投稿用とwebhook管理用で認証方式を分ける。
- Webhook POSTは10秒以内に200を返す必要がある。Misskey APIが遅い場合は、先に検証とキュー投入だけ行い、非同期でMisskeyへ投稿する構成を検討する。

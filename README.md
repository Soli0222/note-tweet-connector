# Note Tweet Connector

Note Tweet Connectorは、MisskeyとTwitter間で投稿を双方向に連携するためのWebhookサーバーです。Misskeyで公開されたノートをTweetとして投稿し、TwitterのAccount Activity webhookで受け取ったtweetをMisskeyのノートとして作成します。

## 機能

- Misskeyの公開ノートをTwitterへ自動投稿
- TwitterのtweetをMisskeyのノートとして自動作成
- 画像付き投稿の連携（最大4枚）
- CW付きMisskeyノートの本文マスクと元ノートURLの付与
- 同一作者の引用renote / 引用tweetを、可能な範囲でTwitterの引用TweetまたはMisskeyのrenoteとして復元
- CrossPostTrackerによるMisskey note IDとTwitter tweet IDの記録、転送ループと重複投稿の抑止
- Twitter webhookのCRC応答と`x-twitter-webhooks-signature`検証
- SSRF対策として、Misskeyメディア取得元とTwitterメディア取得元の許可ホストを制限
- Prometheusメトリクスとヘルスチェック
- Graceful Shutdown
- Docker ComposeおよびKubernetesでのデプロイ

## アーキテクチャ

- **Webhookサーバー**: Misskey webhookとTwitter webhookを受け付けます。
- **メトリクスサーバー**: Prometheusメトリクスを公開します。
- **CrossPostTracker**: sqliteでMisskey note IDとTwitter tweet IDの対応を保持します。古いレコードは保持期間に応じて削除されます。
- **Note2Tweet**: Misskeyノートのpayloadを検証し、Twitter APIでTweetを投稿します。
- **Tweet2Note**: Twitter Account Activity payloadを検証し、Misskey APIでノートを作成します。

## 前提条件

- Go 1.26.3（ローカルでビルド・テストする場合）
- DockerおよびDocker Compose（コンテナ起動する場合）
- KubernetesおよびHelm（Kubernetesへデプロイする場合）
- Misskey APIトークン
- Twitter APIキー、アクセストークン、OAuth 2.0 User Access Token
- Twitter WebhooksおよびAccount Activity APIの設定

Misskey APIトークンには`write:notes`が必要です。Twitter画像をMisskey Driveへ添付する場合は`write:drive`も必要です。

## 起動設定

このアプリケーションは設定をすべてコマンドラインフラグで受け取ります。クレデンシャルを環境変数で管理する場合も、シェル、Docker Compose、またはKubernetes側で展開してフラグとして渡してください。

```bash
note-tweet-connector \
  -misskey-hook-secret="${MISSKEY_HOOK_SECRET}" \
  -misskey-host="${MISSKEY_HOST}" \
  -misskey-token="${MISSKEY_TOKEN}" \
  -misskey-media-host="${MISSKEY_MEDIA_HOST}" \
  -twitter-media-hosts="${TWITTER_MEDIA_HOSTS:-pbs.twimg.com,video.twimg.com}" \
  -twitter-api-key="${TWITTER_API_KEY}" \
  -twitter-api-key-secret="${TWITTER_API_KEY_SECRET}" \
  -twitter-access-token="${TWITTER_ACCESS_TOKEN}" \
  -twitter-access-token-secret="${TWITTER_ACCESS_TOKEN_SECRET}" \
  -twitter-user-access-token="${TWITTER_USER_ACCESS_TOKEN}" \
  -twitter-webhook-consumer-secret="${TWITTER_WEBHOOK_CONSUMER_SECRET}" \
  -twitter-username="${TWITTER_USERNAME}"
```

### コマンドラインオプション

| フラグ | デフォルト | 説明 |
|--------|-----------|------|
| `-port` | `8080` | Webhookサーバーのポート |
| `-metrics-port` | `9090` | メトリクスサーバーのポート |
| `-tracker-db-path` | `data/tracker.sqlite` | CrossPostTrackerのsqlite DBファイルパス |
| `-tracker-retention` | `2160h` | Trackerレコードの保持期間。0以下で無期限 |
| `-read-timeout` | `15s` | HTTP読み取りタイムアウト |
| `-write-timeout` | `15s` | HTTP書き込みタイムアウト |
| `-idle-timeout` | `60s` | HTTPアイドルタイムアウト |
| `-shutdown-timeout` | `30s` | Graceful Shutdownのタイムアウト |
| `-log-level` | `info` | ログレベル（`debug`, `info`, `warn`, `error`） |
| `-misskey-hook-secret` | なし | Misskey webhookを認証するための秘密キー |
| `-misskey-host` | なし | Misskeyインスタンスのホスト名（例: `example.tld`） |
| `-misskey-token` | なし | Misskey APIトークン |
| `-misskey-media-host` | なし | TwitterへアップロードするMisskeyメディアの許可ホスト（例: `s3.example.tld`） |
| `-twitter-media-hosts` | `pbs.twimg.com,video.twimg.com` | MisskeyへアップロードするTwitterメディアの許可ホスト。カンマ区切り |
| `-twitter-api-key` | なし | Twitter APIキー |
| `-twitter-api-key-secret` | なし | Twitter APIキーシークレット |
| `-twitter-access-token` | なし | Twitterアクセストークン |
| `-twitter-access-token-secret` | なし | Twitterアクセストークンシークレット |
| `-twitter-user-access-token` | なし | Media API v2 chunked uploadとWebhook / Account Activity APIの操作に使うOAuth 2.0 User Access Token |
| `-twitter-webhook-consumer-secret` | `-twitter-api-key-secret` | Twitter WebhookのCRC応答と署名検証に使うConsumer Secret |
| `-twitter-username` | なし | payloadからユーザー名を取得できない場合に使うTwitterユーザー名 |
| `-version` | - | バージョンを表示して終了 |

`-misskey-hook-secret`、`-misskey-host`、`-misskey-token`、`-misskey-media-host`、`-twitter-api-key`、`-twitter-api-key-secret`、`-twitter-access-token`、`-twitter-access-token-secret`、`-twitter-user-access-token`は必須です。

## Docker Compose

`compose.yaml`は環境変数をフラグに展開して起動します。

```bash
docker compose up -d
```

主な環境変数:

| 環境変数 | 必須 | 説明 |
|----------|------|------|
| `MISSKEY_HOOK_SECRET` | はい | Misskey webhookの`X-Misskey-Hook-Secret`と一致させる値 |
| `MISSKEY_HOST` | はい | Misskeyインスタンスのホスト名 |
| `MISSKEY_TOKEN` | はい | Misskey APIトークン |
| `MISSKEY_MEDIA_HOST` | はい | Misskeyメディアの許可ホスト |
| `TWITTER_MEDIA_HOSTS` | いいえ | Twitterメディアの許可ホスト。未指定時は`pbs.twimg.com,video.twimg.com` |
| `TWITTER_API_KEY` | はい | Twitter APIキー |
| `TWITTER_API_KEY_SECRET` | はい | Twitter APIキーシークレット |
| `TWITTER_ACCESS_TOKEN` | はい | Twitterアクセストークン |
| `TWITTER_ACCESS_TOKEN_SECRET` | はい | Twitterアクセストークンシークレット |
| `TWITTER_USER_ACCESS_TOKEN` | はい | Twitter OAuth 2.0 User Access Token |
| `TWITTER_WEBHOOK_CONSUMER_SECRET` | いいえ | Twitter webhook署名検証用。未指定時は`TWITTER_API_KEY_SECRET` |
| `TWITTER_USERNAME` | いいえ | fallback用Twitterユーザー名 |

CrossPostTrackerのsqlite DBはコンテナ内の`/app/data/tracker.sqlite`に作成されます。`compose.yaml`では`./data:/app/data`をマウントしているため、コンテナを再作成してもTrackerの対応関係は保持されます。

## Kubernetes

Helm Chartは https://github.com/Soli0222/helm-charts で公開されています。

```bash
helm repo add soli0222 https://soli0222.github.io/helm-charts
helm repo update
helm install note-tweet-connector soli0222/note-tweet-connector -f values.yaml
```

## Webhook設定

1. MisskeyとTwitterのAPIキー・トークンを取得します。
2. 起動時フラグ、Docker Composeの環境変数、またはHelm valuesに設定します。
3. サーバーがポート`8080`でWebhook、ポート`9090`でメトリクスを公開します。
4. Misskeyの管理画面でwebhookを設定します。User-Agentは`Misskey-Hooks`を含む必要があり、`X-Misskey-Hook-Secret`は`-misskey-hook-secret`と同じ値にします。
5. Twitter Webhooks APIで`https://your-domain.example/twitter/webhook`を登録します。
6. Account Activity APIで対象Twitterアカウントをwebhookへsubscribeします。

### Twitter Webhookの登録

Webhook URLはHTTPSで公開し、URLにポート番号を含めないでください。登録時にTwitterから`GET /twitter/webhook?crc_token=...`が呼ばれ、このアプリがCRC応答を返します。

```bash
curl --request POST \
  --url https://api.x.com/2/webhooks \
  --header "Authorization: Bearer ${TWITTER_USER_ACCESS_TOKEN}" \
  --header "Content-Type: application/json" \
  --data '{"url":"https://your-domain.example/twitter/webhook"}'
```

登録済みWebhookの確認:

```bash
curl --request GET \
  --url https://api.x.com/2/webhooks \
  --header "Authorization: Bearer ${TWITTER_USER_ACCESS_TOKEN}"
```

Account Activity subscriptionの作成:

```bash
curl --request POST \
  --url https://api.x.com/2/account_activity/webhooks/${WEBHOOK_ID}/subscriptions/all \
  --header "Authorization: Bearer ${TWITTER_USER_ACCESS_TOKEN}" \
  --header "Content-Type: application/json" \
  --data '{}'
```

subscriptionの確認:

```bash
curl --request GET \
  --url https://api.x.com/2/account_activity/webhooks/${WEBHOOK_ID}/subscriptions/all/list \
  --header "Authorization: Bearer ${TWITTER_USER_ACCESS_TOKEN}"
```

## 動作仕様

### MisskeyからTwitter

- `POST /`にMisskey webhookを受け付けます。
- `User-Agent`に`Misskey-Hooks`を含まないリクエストは拒否します。
- `X-Misskey-Hook-Secret`が`-misskey-hook-secret`と一致しないリクエストは拒否します。
- `visibility`が`public`ではないノート、`localOnly`のノート、CrossPostTrackerに登録済みのノートはスキップします。
- `RT @`で始まるノートは転送ループ抑止のためスキップします。
- CW付きノートはCW、本文長に応じたマスク、元ノートURLをTweet本文にします。
- 画像ファイルは最大4件までTwitterへアップロードします。取得元URLはHTTPSかつ`-misskey-media-host`と一致する必要があります。
- 同一作者の引用renoteで、引用元note IDに対応するtweet IDがTrackerにある場合は、Twitterの引用Tweetとして投稿します。

### TwitterからMisskey

- `POST /twitter/webhook`にTwitter Account Activity payloadを受け付けます。
- `x-twitter-webhooks-signature`を`-twitter-webhook-consumer-secret`で検証します。
- CrossPostTrackerに登録済みのtweetはスキップします。
- `RN [at]`で始まるtweetは転送ループ抑止のためスキップします。
- `RT @`で始まるtweetは元tweet URLを本文末尾に追記します。
- photoメディアは最大4件までMisskey Driveへアップロードし、ノートに添付します。取得元URLはHTTPSかつ`-twitter-media-hosts`に含まれる必要があります。
- 同一作者の引用tweetで、引用元tweet IDに対応するMisskey note IDがTrackerにある場合は、Misskeyのrenoteとして作成します。

## エンドポイント

### メインサーバー（デフォルト: ポート8080）

| エンドポイント | 説明 |
|---------------|------|
| `POST /` | Misskey webhookリクエストを受け付け |
| `GET /twitter/webhook` | Twitter WebhookのCRCリクエストを受け付け |
| `POST /twitter/webhook` | Twitter Account Activity payloadを受け付け |
| `GET /healthz` | ヘルスチェック |

### メトリクスサーバー（デフォルト: ポート9090）

| エンドポイント | 説明 |
|---------------|------|
| `GET /metrics` | Prometheusメトリクス |

## メトリクス

| メトリクス | 型 | 説明 |
|-----------|-----|------|
| `build_info` | Gauge | バージョン情報 |
| `webhook_requests_total` | Counter | リクエスト総数（`source`, `status`別） |
| `webhook_request_duration_seconds` | Histogram | リクエスト処理時間 |
| `webhook_request_errors_total` | Counter | エラー数（`source`, `error_type`別） |
| `note2tweet_total` | Counter | Note to Tweet変換試行数 |
| `note2tweet_success_total` | Counter | 成功数 |
| `note2tweet_errors_total` | Counter | エラー数 |
| `note2tweet_skipped_total` | Counter | スキップ数（`reason`別） |
| `tweet2note_total` | Counter | Tweet to Note変換試行数 |
| `tweet2note_success_total` | Counter | 成功数 |
| `tweet2note_errors_total` | Counter | エラー数 |
| `tweet2note_skipped_total` | Counter | スキップ数（`reason`別） |
| `tracker_entries_total` | Gauge | トラッカー内エントリ数 |
| `tracker_duplicates_hit_total` | Counter | 重複検出数 |

標準の`go_*`、`process_*`メトリクスも公開されます。

## 開発

```bash
go test ./...
golangci-lint run
```

## ライセンス

このプロジェクトはMITライセンスの下で公開されています。詳細は[LICENSE](LICENSE)をご覧ください。

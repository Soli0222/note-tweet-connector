# Note Tweet Connector

Note Tweet Connectorは、MisskeyとTwitter間でノートを自動的に連携するためのコネクタです。Misskeyで公開されたノートをTwitterに投稿し、逆にTwitterのツイートをMisskeyに記録します。

## 機能

- Misskeyで公開されたノートを自動的にTwitterに投稿
- TwitterのツイートをMisskeyにノートとして保存
- 画像付きノートのサポート（最大4枚まで）
- リノートの内容をツイートとして投稿
- 重複投稿を防ぐためのコンテンツトラッキング機能
- コマンドラインフラグによる柔軟な設定
- Prometheusメトリクスの公開
- Graceful Shutdown対応
- DockerおよびKubernetesを利用した簡単なデプロイメント

## アーキテクチャ

このアプリケーションは以下のコンポーネントで構成されています：

- **Webサーバー**: Webhookリクエストを受け付け、MisskeyとTwitter間で内容を転送
- **メトリクスサーバー**: Prometheusメトリクスを公開
- **CrossPostTracker**: Misskey note ID と Twitter tweet ID の対応をsqliteで保持し、転送ループを防ぐトラッキングシステム
- **Note2Tweet**: Misskeyのノートを受信してTwitterに投稿する機能
- **Tweet2Note**: Twitterのツイートを受信してMisskeyに投稿する機能

## 前提条件

- DockerおよびDocker Composeのインストール
- KubernetesおよびHelmのセットアップ（オプション）
- Twitter APIおよびMisskeyのAPIキー
- Twitter WebhooksおよびAccount Activity APIの設定

## インストール

### フラグの設定

このアプリケーションは設定をすべてコマンドラインフラグで受け取ります。クレデンシャルを環境変数で管理する場合も、アプリケーションにはシェルやDocker Compose側で展開してフラグとして渡してください。

```bash
note-tweet-connector \
  -misskey-hook-secret="${MISSKEY_HOOK_SECRET}" \
  -misskey-host="${MISSKEY_HOST}" \
  -misskey-token="${MISSKEY_TOKEN}" \
  -misskey-media-host="${MISSKEY_MEDIA_HOST}" \
  -twitter-api-key="${TWITTER_API_KEY}" \
  -twitter-api-key-secret="${TWITTER_API_KEY_SECRET}" \
  -twitter-access-token="${TWITTER_ACCESS_TOKEN}" \
  -twitter-access-token-secret="${TWITTER_ACCESS_TOKEN_SECRET}" \
  -twitter-user-access-token="${TWITTER_USER_ACCESS_TOKEN}"
```

### コマンドラインオプション

以下のフラグで設定をカスタマイズできます：

| フラグ | デフォルト | 説明 |
|--------|-----------|------|
| `-port` | `8080` | Webhookサーバーのポート |
| `-metrics-port` | `9090` | メトリクスサーバーのポート |
| `-tracker-db-path` | `data/tracker.sqlite` | CrossPostTrackerのsqlite DBファイルパス |
| `-tracker-retention` | `2160h` | Trackerレコードの保持期間（0以下で無期限） |
| `-read-timeout` | `15s` | HTTP読み取りタイムアウト |
| `-write-timeout` | `15s` | HTTP書き込みタイムアウト |
| `-idle-timeout` | `60s` | HTTPアイドルタイムアウト |
| `-shutdown-timeout` | `30s` | Graceful Shutdownのタイムアウト |
| `-log-level` | `info` | ログレベル（debug, info, warn, error） |
| `-misskey-hook-secret` | なし | Misskeyからのwebhookを認証するための秘密キー |
| `-misskey-host` | なし | Misskeyインスタンスのホスト名（例: example.tld） |
| `-misskey-token` | なし | MisskeyのAPIトークン（`write:notes`、Twitter画像をMisskeyへ添付する場合は`write:drive`も必要） |
| `-misskey-media-host` | なし | Misskeyのメディアストレージホスト（例: s3.example.tld）※SSRF対策用 |
| `-twitter-media-hosts` | `pbs.twimg.com,video.twimg.com` | Twitterのメディア取得を許可するホストのカンマ区切りリスト ※SSRF対策用 |
| `-twitter-api-key` | なし | Twitter APIキー |
| `-twitter-api-key-secret` | なし | Twitter APIキーシークレット |
| `-twitter-access-token` | なし | Twitterアクセストークン |
| `-twitter-access-token-secret` | なし | Twitterアクセストークンシークレット |
| `-twitter-user-access-token` | なし | Media API v2 chunked uploadとWebhook/Account Activity APIの操作に使うOAuth 2.0 User Access Token |
| `-twitter-webhook-consumer-secret` | `-twitter-api-key-secret` | Twitter WebhookのCRC・署名検証に使うConsumer Secret |
| `-twitter-username` | なし | Twitterのユーザー名（payloadから取得できない場合の補助用） |
| `-version` | - | バージョンを表示して終了 |

### Dockerを使用した起動

Docker Composeを使用してコンテナを起動します。

```bash
docker compose up -d
```

CrossPostTrackerのsqlite DBはデフォルトでコンテナ内の`/app/data/tracker.sqlite`に作成されます。`docker-compose.yaml`では`./data:/app/data`をマウントしているため、コンテナを再作成してもTrackerの対応関係は保持されます。

###　Kubernetesを使用した起動

Helm Chartを使用してKubernetesクラスターにデプロイします。  
Chartは https://github.com/Soli0222/helm-charts でホストされています。

```bash
helm repo add soli0222 https://soli0222.github.io/helm-charts
helm repo update
```

```bash
helm install note-tweet-connector soli0222/note-tweet-connector -f values.yaml
```

## 使用方法

1. MisskeyとTwitterのAPIキーを取得し、起動時フラグに設定します
2. Docker ComposeまたはKubernetesを使用してサービスを起動します
3. サーバーがポート8080でWebhookリクエストを、ポート9090でメトリクスを公開します
4. Misskeyの管理画面でwebhookを設定します（User-Agent: "Misskey-Hooks"、X-Misskey-Hook-Secret: `-misskey-hook-secret` と同じ値）
5. Twitter Webhooks APIで `https://your-domain.example/twitter/webhook` を登録します
6. Account Activity APIで対象Twitterアカウントをwebhookへsubscribeします

### Twitter Webhookの登録

Webhook URLはHTTPSで公開し、URLにポート番号を含めないでください。登録時にTwitterから `GET /twitter/webhook?crc_token=...` が呼ばれ、このアプリがCRC応答を返します。

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

### 動作の流れ

- Misskeyでノートが公開されると、webhookが呼び出され、ノートの内容がTwitterに投稿されます
  - 画像付きのノートもテキストのみのノートもTwitter APIを使用して投稿
- Twitterでツイートが投稿されると、Twitter Webhookを通じてAccount Activity API payloadが呼び出され、Misskeyにノートとして保存されます
  - 画像付きツイートは画像をMisskey Driveへアップロードし、ノートに添付します

## エンドポイント

### メインサーバー（デフォルト: ポート8080）

| エンドポイント | 説明 |
|---------------|------|
| `POST /` | Webhookリクエストを受け付け |
| `GET /twitter/webhook` | Twitter WebhookのCRCリクエストを受け付け |
| `POST /twitter/webhook` | Twitter WebhookのAccount Activity payloadを受け付け |
| `GET /healthz` | ヘルスチェック |

### メトリクスサーバー（デフォルト: ポート9090）

| エンドポイント | 説明 |
|---------------|------|
| `GET /metrics` | Prometheusメトリクス |

## メトリクス

以下のメトリクスが公開されます：

### アプリケーションメトリクス

| メトリクス | 型 | 説明 |
|-----------|-----|------|
| `build_info` | Gauge | バージョン情報 |
| `webhook_requests_total` | Counter | リクエスト総数（source, status別） |
| `webhook_request_duration_seconds` | Histogram | リクエスト処理時間 |
| `webhook_request_errors_total` | Counter | エラー数（source, error_type別） |
| `note2tweet_total` | Counter | Note→Tweet変換試行数 |
| `note2tweet_success_total` | Counter | 成功数 |
| `note2tweet_errors_total` | Counter | エラー数 |
| `note2tweet_skipped_total` | Counter | スキップ数（reason別） |
| `tweet2note_total` | Counter | Tweet→Note変換試行数 |
| `tweet2note_success_total` | Counter | 成功数 |
| `tweet2note_errors_total` | Counter | エラー数 |
| `tweet2note_skipped_total` | Counter | スキップ数（reason別） |
| `tracker_entries_total` | Gauge | トラッカー内エントリ数 |
| `tracker_duplicates_hit_total` | Counter | 重複検出数 |

### 標準メトリクス

- `go_*` - Goランタイムメトリクス（goroutine数、メモリ使用量など）
- `process_*` - プロセスメトリクス（CPU時間、ファイルディスクリプタなど）

## ライセンス

このプロジェクトはMITライセンスの下で公開されています。詳細はLICENSEファイルをご覧ください。

# Note Tweet Connector

Note Tweet Connectorは、MisskeyとTwitter間で投稿を双方向に連携するためのサーバーです。Misskeyで公開されたノートをTweetとして投稿し、TwitterのFiltered Streamで受け取ったtweetをMisskeyのノートとして作成します。

## 機能

- Misskeyの公開ノートをTwitterへ自動投稿
- TwitterのtweetをMisskeyのノートとして自動作成
- 画像付き投稿の連携（最大4枚）
- CW付きMisskeyノートの本文マスクと元ノートURLの付与
- Misskeyの通常renoteと他者ノートの引用renoteはスキップし、自分自身のノートを引用した引用renoteは可能な範囲でTwitterの引用Tweetとして投稿
- 同一作者の引用tweetは、可能な範囲でMisskeyのrenoteとして復元
- CrossPostTrackerによるMisskey note IDとTwitter tweet IDの記録、転送ループと重複投稿の抑止
- Twitter Filtered Streamの永続接続と自動再接続
- SSRF対策として、Misskeyメディア取得元とTwitterメディア取得元の許可ホストを制限
- Prometheusメトリクスとヘルスチェック
- Graceful Shutdown
- Docker ComposeおよびKubernetesでのデプロイ

## アーキテクチャ

- **Webhookサーバー**: Misskey webhookとTwitter OAuth 2.0 callbackを受け付けます。
- **Twitter Stream worker**: Twitter Filtered Streamに接続し、受信したtweetをMisskeyへ転送します。
- **メトリクスサーバー**: Prometheusメトリクスを公開します。
- **CrossPostTracker**: sqliteでMisskey note IDとTwitter tweet IDの対応を保持します。古いレコードは保持期間に応じて削除されます。
- **Note2Tweet**: Misskeyノートのpayloadを検証し、Twitter APIでTweetを投稿します。
- **Tweet2Note**: Twitter Filtered Stream payloadを検証し、Misskey APIでノートを作成します。

## 前提条件

- Go 1.26.3（ローカルでビルド・テストする場合）
- DockerおよびDocker Compose（コンテナ起動する場合）
- KubernetesおよびHelm（Kubernetesへデプロイする場合）
- Misskey APIトークン
- Twitter OAuth 2.0 Client ID
- Twitter Filtered Stream用Bearer Token

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
  -twitter-oauth2-client-id="${TWITTER_OAUTH2_CLIENT_ID}" \
  -twitter-oauth2-redirect-url="${TWITTER_OAUTH2_REDIRECT_URL}" \
  -twitter-token-store-path="${TWITTER_TOKEN_STORE_PATH:-data/twitter_oauth2_token.json}" \
  -twitter-bearer-token="${TWITTER_BEARER_TOKEN}" \
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
| `-twitter-oauth2-client-id` | なし | OAuth 2.0 Authorization Code Flow with PKCEに使うClient ID |
| `-twitter-oauth2-redirect-url` | なし | OAuth 2.0 callback URL。Twitter Developer Portalのcallback URLと完全一致させる |
| `-twitter-token-store-path` | `data/twitter_oauth2_token.json` | 更新済みOAuth 2.0 tokenを保存するJSONファイル |
| `-twitter-bearer-token` | なし | Twitter Filtered Streamとrule管理に使うApplication-Only Bearer Token |
| `-twitter-stream-keep-alive-timeout` | `90s` | Twitter streamのデータまたはkeep-aliveが途絶えたと判断するまでの時間 |
| `-twitter-stream-reconnect-min` | `5s` | Twitter stream再接続backoffの初期値 |
| `-twitter-stream-reconnect-max` | `5m` | Twitter stream再接続backoffの上限 |
| `-twitter-username` | なし | stream rule生成とpayloadからユーザー名を取得できない場合に使うTwitterユーザー名 |
| `-version` | - | バージョンを表示して終了 |

`-misskey-hook-secret`、`-misskey-host`、`-misskey-token`、`-misskey-media-host`、`-twitter-oauth2-client-id`、`-twitter-oauth2-redirect-url`、`-twitter-bearer-token`、`-twitter-username`は必須です。

Tweet投稿とMedia API v2 uploadにはOAuth 2.0 User Access Tokenが必要です。このアプリはAuthorization Code Flow with PKCEで初回認可を行い、取得したaccess token / refresh tokenを`-twitter-token-store-path`に保存します。`client_secret`、固定のUser Access Token、固定のrefresh tokenは設定しません。必要scopeは`tweet.read tweet.write users.read media.write offline.access`です。5MB以下の通常画像は単発uploadを使い、GIFや大きいメディアはchunked uploadを使います。投稿や画像付き投稿だけが403になる場合は、OAuth 2.0 User Access Tokenのscopeとdeveloper appのTweet投稿・Media APIアクセスを確認してください。

`-twitter-oauth2-redirect-url`には、外部から到達できるcallback URLを指定します。Twitter Developer Portalにも同じURLを登録してください。

```text
TWITTER_OAUTH2_REDIRECT_URL=https://your-domain.example/twitter/callback
```

初回起動時やtoken storeを失った場合、サービスは起動し続けます。Tweet投稿などOAuth 2.0 tokenが必要な処理は失敗し、ログに短命のlogin URLが出ます。

```text
Twitter OAuth 2.0 authorization required login_url=https://your-domain.example/twitter/login?auth=... expires_at=...
```

運用者はログのURLをブラウザで開き、Twitterの認可画面で連携します。callback完了後はtoken storeに保存されたrefresh tokenで自動更新します。`-twitter-token-store-path`は書き込み可能で、Docker ComposeやKubernetesでは永続volume上に置いてください。refresh tokenは更新時にローテーションされるため、複数replica運用とは相性が悪く、基本はreplica 1で動かします。

Twitter Filtered Streamとstream rule管理にはOAuth 2.0 Application-Only Bearer Tokenを使います。これはTweet投稿とMedia API v2 upload用のOAuth 2.0 User Context tokenとは別物です。

## ビルド

バージョンはビルド時にGitタグから注入します。注入されない場合は`dev`として動作します。

```bash
VERSION="$(git describe --tags --dirty --always)"
CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o note-tweet-connector ./cmd/note-tweet-connector
```

Docker imageをローカルでビルドする場合は、同じ値をbuild argで渡します。

```bash
docker build --build-arg VERSION="$(git describe --tags --dirty --always)" -t note-tweet-connector .
```

GitHub Actionsのリリース用Docker imageでは、Docker tagを生成している`docker/metadata-action`のversionを同じbuild argとして渡し、アプリ内の`-version`表示、ログ、PrometheusメトリクスのversionラベルとDocker tagを揃えます。

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
| `TWITTER_OAUTH2_CLIENT_ID` | はい | Twitter OAuth 2.0 Client ID |
| `TWITTER_OAUTH2_REDIRECT_URL` | はい | Twitter OAuth 2.0 callback URL。例: `https://your-domain.example/twitter/callback` |
| `TWITTER_TOKEN_STORE_PATH` | いいえ | 更新済みOAuth 2.0 tokenの保存先。未指定時は`data/twitter_oauth2_token.json` |
| `TWITTER_BEARER_TOKEN` | はい | Twitter Filtered Streamとrule管理に使うApplication-Only Bearer Token |
| `TWITTER_STREAM_KEEP_ALIVE_TIMEOUT` | いいえ | Twitter stream keep-alive timeout。未指定時は`90s` |
| `TWITTER_USERNAME` | はい | stream rule生成とfallback用Twitterユーザー名 |

CrossPostTrackerのsqlite DBはコンテナ内の`/app/data/tracker.sqlite`に作成されます。OAuth 2.0 token storeはデフォルトで`/app/data/twitter_oauth2_token.json`に作成されます。`compose.yaml`では`./data:/app/data`をマウントしているため、コンテナを再作成してもTrackerの対応関係と更新済みtokenは保持されます。

コンテナは非rootユーザー（UID `10001`）で実行されます。bind mountする`./data`は、このUIDから書き込める権限にしてください。

## Kubernetes

Helm Chartは https://github.com/Soli0222/helm-charts で公開されています。

```bash
helm repo add soli0222 https://soli0222.github.io/helm-charts
helm repo update
helm install note-tweet-connector soli0222/note-tweet-connector -f values.yaml
```

## 連携設定

1. Misskey APIトークン、Twitter OAuth 2.0 Client ID、Twitter Application-Only Bearer Tokenを取得します。
2. 起動時フラグ、Docker Composeの環境変数、またはHelm valuesに設定します。
3. サーバーがポート`8080`でWebhook、ポート`9090`でメトリクスを公開します。
4. Misskeyの管理画面でwebhookを設定します。User-Agentは`Misskey-Hooks`を含む必要があり、`X-Misskey-Hook-Secret`は`-misskey-hook-secret`と同じ値にします。
5. Twitter Developer PortalでApplication-Only Bearer Tokenを取得し、`-twitter-bearer-token`に設定します。
6. `-twitter-username`に同期対象のTwitterユーザー名を設定します。

### Twitter Filtered Streamの設定

起動時にアプリが`GET /2/tweets/search/stream/rules`で既存ruleを確認し、tagが`note-tweet-connector`のruleを管理します。同じtagで異なるruleがある場合は削除し、`-twitter-username`から生成したruleを追加します。tagが異なるruleは触りません。

デフォルトのruleは次の形式です。

```text
from:${TWITTER_USERNAME} -is:reply
```

## 動作仕様

### MisskeyからTwitter

- `POST /`にMisskey webhookを受け付けます。
- `User-Agent`に`Misskey-Hooks`を含まないリクエストは拒否します。
- `X-Misskey-Hook-Secret`が`-misskey-hook-secret`と一致しないリクエストは拒否します。
- `visibility`が`public`ではないノート、`localOnly`のノート、CrossPostTrackerに登録済みのノートはスキップします。
- `replyId`または`reply`があるリプライノートはスキップします。
- 通常renoteと他者ノートの引用renoteはスキップします。
- 自分自身のノートを引用した引用renoteで、引用元note IDに対応するtweet IDがTrackerにある場合は、Twitterの引用Tweetとして投稿します。
- `RT @`で始まるノートは転送ループ抑止のためスキップします。
- CW付きノートはCW、本文長に応じたマスク、元ノートURLをTweet本文にします。
- 画像ファイルは最大4件までTwitterへアップロードします。取得元URLはHTTPSかつ`-misskey-media-host`と一致する必要があります。

### TwitterからMisskey

- Twitter Filtered Streamに永続接続し、受信したpayloadを処理します。
- `-twitter-stream-keep-alive-timeout`以上streamのデータまたはkeep-aliveが来ない場合は接続を切り、backoff付きで再接続します。
- 転送対象のtweetがないpayloadはログと`tweet2note_skipped_total{reason="no_eligible_tweets"}`で記録します。
- CrossPostTrackerに登録済みのtweetはスキップします。
- `referenced_tweets.type == "replied_to"`があるリプライtweetはスキップします。
- `RN [at]`で始まるtweetは転送ループ抑止のためスキップします。
- `RT @`で始まるtweetは元tweet URLを本文末尾に追記します。リツイートの場合、画像はMisskey Driveへアップロードしません。Filtered Stream ruleではretweetを除外しません。
- 通常のtweetのphotoメディアは最大4件までMisskey Driveへアップロードし、ノートに添付します。取得元URLはHTTPSかつ`-twitter-media-hosts`に含まれる必要があります。
- 同一作者の引用tweetで、引用元tweet IDに対応するMisskey note IDがTrackerにある場合は、Misskeyのrenoteとして作成します。

## エンドポイント

### メインサーバー（デフォルト: ポート8080）

| エンドポイント | 説明 |
|---------------|------|
| `POST /` | Misskey webhookリクエストを受け付け |
| `GET /twitter/login` | ログに出力された短命auth tokenを検証し、TwitterのOAuth 2.0認可画面へredirect |
| `GET /twitter/callback` | Twitter OAuth 2.0 callbackを受け取り、token storeへUser Access Token / refresh tokenを保存 |
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
| `twitter_stream_connects_total` | Counter | Twitter stream接続試行数（`status`別） |
| `twitter_stream_disconnects_total` | Counter | Twitter stream切断数（`reason`別） |
| `twitter_stream_messages_total` | Counter | Twitter stream message処理数（`status`別） |
| `twitter_stream_last_message_timestamp_seconds` | Gauge | 最後にTwitter stream messageを受信したUnix timestamp |
| `twitter_stream_rule_updates_total` | Counter | Twitter stream rule更新試行数（`action`, `status`別） |
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

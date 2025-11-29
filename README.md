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
- **ContentTracker**: 重複投稿を防ぐためのハッシュベースのトラッキングシステム
- **Note2Tweet**: Misskeyのノートを受信してTwitterに投稿する機能
- **Tweet2Note**: Twitterのツイートを受信してMisskeyに投稿する機能

## 前提条件

- DockerおよびDocker Composeのインストール
- KubernetesおよびHelmのセットアップ（オプション）
- Twitter APIおよびMisskeyのAPIキー
- IFTTT Webhookの設定（テキストのみのツイート用）

## インストール

### 環境変数の設定

.env.exampleファイルをコピーして`.env`ファイルを作成し、必要な環境変数を設定します。

```bash
cp .env.example .env
```

編集した`.env`ファイルに以下の情報を入力してください：

| 環境変数 | 説明 |
|----------|------|
| `MISSKEY_HOOK_SECRET` | Misskeyからのwebhookを認証するための秘密キー |
| `IFTTT_HOOK_SECRET` | IFTTTからのwebhookを認証するための秘密キー |
| `MISSKEY_HOST` | Misskeyインスタンスのホスト名（例: example.tld） |
| `MISSKEY_TOKEN` | MisskeyのAPIトークン |
| `MISSKEY_MEDIA_HOST` | Misskeyのメディアストレージホスト（例: s3.example.tld）※SSRF対策用 |
| `API_KEY` | Twitter APIキー |
| `API_KEY_SECRET` | Twitter APIキーシークレット |
| `ACCESS_TOKEN` | Twitterアクセストークン |
| `ACCESS_TOKEN_SECRET` | Twitterアクセストークンシークレット |
| `IFTTT_EVENT` | IFTTTイベント名 |
| `IFTTT_KEY` | IFTTTキー |

### コマンドラインオプション

以下のフラグで設定をカスタマイズできます：

| フラグ | デフォルト | 説明 |
|--------|-----------|------|
| `-port` | `8080` | Webhookサーバーのポート |
| `-metrics-port` | `9090` | メトリクスサーバーのポート |
| `-tracker-expiry` | `5h` | トラッカーの有効期限 |
| `-read-timeout` | `15s` | HTTP読み取りタイムアウト |
| `-write-timeout` | `15s` | HTTP書き込みタイムアウト |
| `-idle-timeout` | `60s` | HTTPアイドルタイムアウト |
| `-shutdown-timeout` | `30s` | Graceful Shutdownのタイムアウト |
| `-log-level` | `info` | ログレベル（debug, info, warn, error） |
| `-version` | - | バージョンを表示して終了 |

### Dockerを使用した起動

Docker Composeを使用してコンテナを起動します。

```bash
docker compose up -d
```

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

1. MisskeyとTwitterのAPIキーを取得し、`.env`ファイルに設定します
2. Docker ComposeまたはKubernetesを使用してサービスを起動します
3. サーバーがポート8080でWebhookリクエストを、ポート9090でメトリクスを公開します
4. Misskeyの管理画面でwebhookを設定します（User-Agent: "Misskey-Hooks"、X-Misskey-Hook-Secret: 環境変数と同じ値）
5. IFTTTでwebhookトリガーを設定します（User-Agent: "IFTTT-Hooks"、X-IFTTT-Hook-Secret: 環境変数と同じ値）

### 動作の流れ

- Misskeyでノートが公開されると、webhookが呼び出され、ノートの内容がTwitterに投稿されます
  - 画像付きのノートは直接Twitter APIを使用して投稿
  - テキストのみのノートはIFTTTを経由して投稿
- Twitterでツイートが投稿されると、IFTTTを通じてwebhookが呼び出され、Misskeyにノートとして保存されます

## エンドポイント

### メインサーバー（デフォルト: ポート8080）

| エンドポイント | 説明 |
|---------------|------|
| `POST /` | Webhookリクエストを受け付け |
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
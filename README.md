# Note Tweet Connector

Note Tweet Connectorは、MisskeyとTwitter間でノートを自動的に連携するためのコネクタです。Misskeyで公開されたノートをTwitterに投稿し、逆にTwitterのツイートをMisskeyに記録します。

## 機能

- Misskeyで公開されたノートを自動的にTwitterに投稿
- TwitterのツイートをMisskeyにノートとして保存
- 画像付きノートのサポート（最大4枚まで）
- リノートの内容をツイートとして投稿
- 重複投稿を防ぐためのコンテンツトラッキング機能
- 環境変数を使用した柔軟な設定
- DockerおよびKubernetesを利用した簡単なデプロイメント

## アーキテクチャ

このアプリケーションは以下のコンポーネントで構成されています：

- **Webサーバー**: Webhookリクエストを受け付け、MisskeyとTwitter間で内容を転送
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

- MISSKEY_HOOK_SECRET: Misskeyからのwebhookを認証するための秘密キー
- IFTTT_HOOK_SECRET: IFTTTからのwebhookを認証するための秘密キー
- MISSKEY_HOST: Misskeyインスタンスのホスト名（例: example.tld）
- MISSKEY_TOKEN: MisskeyのAPIトークン
- API_KEY: Twitter APIキー
- API_KEY_SECRET: Twitter APIキーシークレット
- ACCESS_TOKEN: Twitterアクセストークン
- ACCESS_TOKEN_SECRET: Twitterアクセストークンシークレット
- IFTTT_EVENT: IFTTTイベント名
- IFTTT_KEY: IFTTTキー

### Dockerを使用した起動

Docker Composeを使用してコンテナを起動します。

```bash
docker compose up -d
```

### Kubernetesを使用したデプロイメント

Helmチャートを使用してKubernetesクラスターにデプロイします。

```bash
helm install note-tweet-connector ./helm/note-tweet-connector -f ./helm/note-tweet-connector/values.yaml
```

## 使用方法

1. MisskeyとTwitterのAPIキーを取得し、`.env`ファイルに設定します
2. Docker ComposeまたはKubernetesを使用してサービスを起動します
3. サーバーがポート8080でリッスンを開始し、Webhookリクエストを受け付けます
4. Misskeyの管理画面でwebhookを設定します（User-Agent: "Misskey-Hooks"、X-Misskey-Hook-Secret: 環境変数と同じ値）
5. IFTTTでwebhookトリガーを設定します（User-Agent: "IFTTT-Hooks"、X-IFTTT-Hook-Secret: 環境変数と同じ値）

### 動作の流れ

- Misskeyでノートが公開されると、webhookが呼び出され、ノートの内容がTwitterに投稿されます
  - 画像付きのノートは直接TwitterAPIを使用して投稿
  - テキストのみのノートはIFTTTを経由して投稿
- Twitterでツイートが投稿されると、IFTTTを通じてwebhookが呼び出され、Misskeyにノートとして保存されます

## ヘルスチェック

アプリケーションの稼働状況を確認するには、`/healthz`エンドポイントにアクセスします。

```bash
curl http://localhost:8080/healthz
```

## ライセンス

このプロジェクトはMITライセンスの下で公開されています。詳細はLICENSEファイルをご覧ください。
# Note Tweet Connector

Note Tweet Connectorは、MisskeyとTwitter間でノートを自動的に連携するためのコネクタです。Misskeyで公開されたノートをTwitterに投稿し、逆にTwitterのツイートをMisskeyに記録します。

## 機能

- Misskeyで公開されたノートを自動的にTwitterに投稿
- TwitterのツイートをMisskeyにノートとして保存
- 環境変数を使用した柔軟な設定
- DockerおよびKubernetesを利用した簡単なデプロイメント

## 前提条件

- DockerおよびDocker Composeのインストール
- KubernetesおよびHelmのセットアップ（オプション）
- Twitter APIおよびMisskeyのAPIキー

## インストール

### 環境変数の設定

`.env.example`ファイルをコピーして`.env`ファイルを作成し、必要な環境変数を設定します。

```bash
cp .env.example .env
```

編集した`.env`ファイルに以下の情報を入力してください：

- MISSKEY_HOOK_SECRET
- IFTTT_HOOK_SECRET
- MISSKEY_HOST
- MISSKEY_TOKEN
- API_KEY
- API_KEY_SECRET
- ACCESS_TOKEN
- ACCESS_TOKEN_SECRET

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

- MisskeyとTwitterのAPIキーを取得し、`.env`ファイルに設定します。
- Docker ComposeまたはKubernetesを使用してサービスを起動します。
- サーバーがポート8080でリッスンを開始し、Webhookリクエストを受け付けます。
- Misskeyでノートが公開されると、自動的にTwitterにツイートされます。
- Twitterにツイートが投稿されると、Misskeyにノートとして保存されます。

## ライセンス

このプロジェクトはMITライセンスの下で公開されています。詳細はLICENSEファイルをご覧ください。

# すべてのオプションをフラグ化する方針

## ゴール

現在は一部の設定がコマンドラインフラグ、一部の設定が環境変数から直接読まれている。これを、アプリケーションの設定値はすべて起動時フラグで受け取り、内部パッケージでは `os.Getenv` を直接呼ばない形に統一する。

クレデンシャルも環境変数をアプリが直接読むのではなく、シェルやコンテナ起動設定側で次のように展開して渡す。

```bash
note-tweet-connector \
  -twitter-user-access-token="${TWITTER_USER_ACCESS_TOKEN}" \
  -twitter-api-key="${TWITTER_API_KEY}" \
  -twitter-api-key-secret="${TWITTER_API_KEY_SECRET}" \
  -twitter-access-token="${TWITTER_ACCESS_TOKEN}" \
  -twitter-access-token-secret="${TWITTER_ACCESS_TOKEN_SECRET}"
```

## 対象にする現在の環境変数

`cmd/note-tweet-connector/main.go`

- `PORT`
- `TRACKER_DB_PATH`
- `TRACKER_RETENTION`
- `MISSKEY_HOOK_SECRET`
- `TWITTER_WEBHOOK_CONSUMER_SECRET`
- `API_KEY_SECRET`

`internal/twitter/client.go`

- `MISSKEY_MEDIA_HOST`
- `API_KEY`
- `API_KEY_SECRET`
- `ACCESS_TOKEN`
- `ACCESS_TOKEN_SECRET`
- `TWITTER_USER_ACCESS_TOKEN`

`internal/handler/tweet2note.go`

- `MISSKEY_HOST`
- `MISSKEY_TOKEN`
- `TWITTER_USERNAME`

`internal/handler/note2tweet.go`

- `MISSKEY_HOST`

`internal/misskey/client.go`

- `TWITTER_MEDIA_HOSTS`

## 追加・整理するフラグ

既存フラグは維持する。

- `-port`
- `-metrics-port`
- `-tracker-db-path`
- `-tracker-retention`
- `-read-timeout`
- `-write-timeout`
- `-idle-timeout`
- `-shutdown-timeout`
- `-log-level`
- `-version`

新規に追加するフラグ。

- `-misskey-hook-secret`
- `-misskey-host`
- `-misskey-token`
- `-misskey-media-host`
- `-twitter-media-hosts` default: `pbs.twimg.com,video.twimg.com`
- `-twitter-api-key`
- `-twitter-api-key-secret`
- `-twitter-access-token`
- `-twitter-access-token-secret`
- `-twitter-user-access-token`
- `-twitter-webhook-consumer-secret`
- `-twitter-username`

命名は `API_KEY` のような汎用名を避け、Twitter 用の値であることが分かる `twitter-*` に寄せる。互換用の環境変数読み取りは削除する。

## 設計方針

1. `cmd/note-tweet-connector/main.go` の `Config` に全設定値を集約する。
2. `parseFlags` はフラグだけを読む。`PORT`、`TRACKER_DB_PATH`、`TRACKER_RETENTION` の環境変数上書きは削除する。
3. `godotenv.Load()` は削除する。`.env` を読む責務はアプリではなく起動環境側に寄せる。
4. 必須値の検証を `Config.Validate()` のような関数に集める。
5. `server` に必要な設定を持たせ、Webhook secret や Twitter webhook secret は `server` のフィールド経由で使う。
6. `handler` パッケージには実行時設定を渡す構造体を追加する。
   - 例: `handler.Config`
   - `MisskeyHost`
   - `MisskeyToken`
   - `TwitterUsername`
7. `twitter` パッケージにはクライアント設定を渡す構造体を追加する。
   - 例: `twitter.Config`
   - `APIKey`
   - `APIKeySecret`
   - `AccessToken`
   - `AccessTokenSecret`
   - `UserAccessToken`
   - `MisskeyMediaHost`
8. `misskey` パッケージでは Twitter メディア許可ホストを引数またはクライアント設定として渡す。
   - 既存の `UploadDriveFileFromURL(ctx, host, token, fileURL)` はテストと呼び出し箇所が多いので、`UploadDriveFileFromURLWithAllowedHosts` のような関数を追加して段階的に移行する案が扱いやすい。
9. パッケージ内部から `os` import を消し、設定取得はすべて `main` からの注入にする。

## 実装ステップ

1. `Config` に新規フラグ項目を追加し、`parseFlags` をフラグ専用にする。
2. 必須設定の検証を追加する。
   - Misskey webhook を受けるため `misskey-hook-secret` は必須。
   - Tweet -> Note には `misskey-host` と `misskey-token` が必須。
   - Note -> Tweet には Twitter API credential と `twitter-user-access-token`、`misskey-media-host` が必須。
   - `twitter-webhook-consumer-secret` は未指定なら `twitter-api-key-secret` を使う。
3. `server` に `Config` 由来の値を持たせ、`MISSKEY_HOOK_SECRET` と Twitter HMAC secret の `os.Getenv` を置き換える。
4. `handler` のエントリポイントを設定つきに変更する。
   - 既存テストを壊しにくくするため、必要なら薄い互換関数ではなくテスト側を新しい API に合わせる。
5. `twitter` の投稿処理を設定つきに変更する。
   - `loadTwitterEnv` と `loadTwitterUserAccessToken` は削除する。
   - `validateMediaURL` は `misskeyMediaHost` を引数で受け取る。
6. `misskey` の Twitter メディア URL 検証を設定つきに変更する。
   - `allowedTwitterMediaHosts` の `os.Getenv` を削除する。
7. テストの `t.Setenv` を設定構造体のセットアップへ置き換える。
8. README と Docker Compose の例を、環境変数をアプリが読む説明から「フラグへシェル展開して渡す」説明へ更新する。

## 互換性について

環境変数の直接読み取りは残さない。既存の `.env` 運用はそのままでは動かなくなるため、Docker Compose では `env_file` もしくはホスト側環境変数を使い、`command:` にフラグとして展開する形へ変更する。

例:

```yaml
services:
  webhook-server:
    build: .
    env_file:
      - .env
    command:
      - -misskey-hook-secret=${MISSKEY_HOOK_SECRET}
      - -misskey-host=${MISSKEY_HOST}
      - -misskey-token=${MISSKEY_TOKEN}
      - -misskey-media-host=${MISSKEY_MEDIA_HOST}
      - -twitter-media-hosts=${TWITTER_MEDIA_HOSTS}
      - -twitter-api-key=${TWITTER_API_KEY}
      - -twitter-api-key-secret=${TWITTER_API_KEY_SECRET}
      - -twitter-access-token=${TWITTER_ACCESS_TOKEN}
      - -twitter-access-token-secret=${TWITTER_ACCESS_TOKEN_SECRET}
      - -twitter-user-access-token=${TWITTER_USER_ACCESS_TOKEN}
      - -twitter-webhook-consumer-secret=${TWITTER_WEBHOOK_CONSUMER_SECRET}
      - -twitter-username=${TWITTER_USERNAME}
```

## 完了条件

- `rg 'os\\.Getenv|LookupEnv|godotenv'` でアプリ本体に設定読み取りが残っていない。
- README の設定説明がフラグ中心になっている。
- Docker Compose の起動例がフラグ渡しになっている。
- `go test ./...` が成功する。
- `golangci-lint run` が成功する。

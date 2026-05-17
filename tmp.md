# OAuth2アクセストークンリフレッシュ実装メモ

## 結論

必要です。

現状の `-twitter-user-access-token` は OAuth 2.0 User Access Token として `internal/twitter/client.go` の Media API v2 chunked upload、Webhook登録、Account Activity API 操作例で Bearer token として使われています。OAuth 2.0 user access token は期限切れするため、長時間稼働するWebhookサーバーでは refresh token を使って更新できるようにする必要があります。

refresh token があるなら、OAuth 2.0 user access token を起動設定として必須にする必要はありません。初回は refresh token から access token を取得すればよいです。`-twitter-user-access-token` は移行用または手動検証用の任意設定に下げ、通常運用では `-twitter-user-refresh-token` と `-twitter-oauth2-client-id` を信頼する構成にします。

一方、Tweet投稿本体の `POST https://api.twitter.com/2/tweets` は現在 OAuth 1.0a の `-twitter-access-token` / `-twitter-access-token-secret` で署名しているため、ここは今回のリフレッシュ対象ではありません。対象は `UserAccessToken` を Bearer として送っている処理です。

公式ドキュメント上、refresh token を得るには認可時に `offline.access` scope が必要です。更新リクエストは `POST https://api.x.com/2/oauth2/token` に `application/x-www-form-urlencoded` で `grant_type=refresh_token`、`refresh_token`、`client_id` を送ります。confidential client の場合は client secret も必要になる想定で実装しておくのが安全です。

## 追加する設定

既存設定に以下を追加します。

- `-twitter-oauth2-client-id`
- `-twitter-oauth2-client-secret`
- `-twitter-user-refresh-token`
- `-twitter-token-store-path`

`-twitter-oauth2-client-secret` は public client では空を許可します。confidential client では必須にしますが、アプリ側から public/confidential を判定できないため、空の場合は Basic 認証なしで更新を試し、401/400 のエラー本文をログに出す方針が現実的です。

`-twitter-token-store-path` はデフォルトを `data/twitter_oauth2_token.json` にします。既存の Docker Compose は `./data:/app/data` をマウントしているため、コンテナ再作成後も更新済みトークンを保持できます。

`-twitter-user-access-token` は既存互換のため残しますが、OAuth 2.0 用としては任意にします。保存ファイルにも access token がなく、フラグにも access token がない場合でも、refresh token があれば初回利用時に token endpoint へ refresh request を送り、取得した access token を保存して使います。

環境変数も合わせて追加します。

- `TWITTER_OAUTH2_CLIENT_ID`
- `TWITTER_OAUTH2_CLIENT_SECRET`
- `TWITTER_USER_REFRESH_TOKEN`
- `TWITTER_TOKEN_STORE_PATH`

README.md と compose.yaml も更新します。

## トークン保存形式

保存ファイルは JSON にします。

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "expires_at": "2026-05-17T12:34:56Z",
  "token_type": "bearer",
  "scope": "tweet.read tweet.write users.read offline.access"
}
```

Twitter側の refresh token はローテーションされる可能性があるため、更新レスポンスに `refresh_token` が含まれていたら必ず保存済み refresh token を差し替えます。`expires_in` が返ったら `expires_at = now + expires_in` として保存します。時計ずれと実行中のAPI失敗を避けるため、期限の5分前を期限切れ扱いにします。

初回起動時は、保存ファイルがあれば保存ファイルを優先します。保存ファイルがなければフラグの `-twitter-user-refresh-token` から初期状態を作ります。`-twitter-user-access-token` が指定されていれば期限不明の暫定 access token として保持できますが、指定されていなくても問題ありません。初回の `BearerToken(ctx)` 呼び出しで refresh token flow を実行し、取得した access token / refresh token / expires_at を保存します。

## 実装方針

`internal/twitter` に OAuth2 token manager を追加します。

候補ファイル:

- `internal/twitter/oauth2_token.go`
- `internal/twitter/oauth2_token_test.go`

主な型:

```go
type OAuth2Config struct {
	ClientID     string
	ClientSecret string
	AccessToken  string // optional bootstrap token
	RefreshToken string
	TokenStorePath string
}

type TokenManager struct {
	mu sync.Mutex
	cfg OAuth2Config
	store TokenStore
	httpClient *http.Client
	token OAuth2Token
}

func (m *TokenManager) BearerToken(ctx context.Context) (string, error)
func (m *TokenManager) Refresh(ctx context.Context) error
```

`BearerToken(ctx)` は以下の動きにします。

1. mutexを取る
2. 保存済み token があり、`expires_at` が5分以上先なら access token を返す
3. access token がない、期限切れ、または期限不明なら refresh token flow を実行する
4. レスポンスの access token / refresh token / expires_in を保存する
5. 新しい access token を返す

複数のWebhook処理が同時にメディアアップロードを始めても、refresh token を同時に使って競合しないよう、refresh は必ず `TokenManager` 内の mutex で直列化します。

## 既存コードへの組み込み

`twitter.Config` に OAuth2 設定を追加します。

```go
type Config struct {
	APIKey            string
	APIKeySecret      string
	AccessToken       string
	AccessTokenSecret string
	UserAccessToken   string // optional OAuth 2.0 bootstrap token
	UserRefreshToken  string
	OAuth2ClientID    string
	OAuth2ClientSecret string
	TokenStorePath    string
	MisskeyMediaHost  string
}
```

ただし、`Config` から毎回 `TokenManager` を作るとリフレッシュ状態を共有できません。起動時に `TokenManager` を1つ作って `handler.Config` または `twitter.Config` に渡す形がよいです。

推奨は `twitter.Config` に interface を持たせる形です。

```go
type BearerTokenSource interface {
	BearerToken(ctx context.Context) (string, error)
}
```

`initMediaUpload` / `appendMediaUpload` / `finalizeMediaUpload` / `waitForMediaProcessing` / `postMediaForm` は `bearerToken string` ではなく `BearerTokenSource` または `func(context.Context) (string, error)` を受け取るようにします。

各HTTPリクエスト直前に `BearerToken(ctx)` を呼び、最新の token を `Authorization: Bearer ...` に設定します。401が返った場合は1回だけ強制 refresh して同じリクエストを再試行します。これで、期限情報がない初期トークンや、保存済み期限と実際の期限がずれた場合にも復旧できます。

## HTTP refresh request

refresh request は以下の形にします。

- method: `POST`
- URL: `https://api.x.com/2/oauth2/token`
- header: `Content-Type: application/x-www-form-urlencoded`
- body:
  - `grant_type=refresh_token`
  - `refresh_token=<current refresh token>`
  - `client_id=<client id>`

`client_secret` は body に入れず、confidential client 用には `Authorization: Basic base64(client_id:client_secret)` を付ける実装がよいです。public client では Basic 認証を付けません。

レスポンス例として以下を扱います。

```json
{
  "token_type": "bearer",
  "expires_in": 7200,
  "access_token": "...",
  "scope": "tweet.read tweet.write users.read offline.access",
  "refresh_token": "..."
}
```

エラー時は access token / refresh token の値をログに出さず、status code とレスポンス本文の短いpreviewだけを出します。

## 後方互換

メディアなしTweet投稿だけなら OAuth2 token refresh は不要です。そのため `-twitter-user-refresh-token` と `-twitter-oauth2-client-id` が未設定でも起動は許可する選択肢があります。

ただし現状の `cfg.validate()` は `-twitter-user-access-token` を必須にしており、READMEも Media API v2 chunked upload と Webhook / Account Activity API 操作に使うと説明しています。リフレッシュ対応後は、OAuth 2.0 user access token ではなく refresh token と client id を必須寄りにします。

- `-twitter-user-refresh-token`
- `-twitter-oauth2-client-id`

`-twitter-user-access-token` は必須から外します。移行を緩くするなら、refresh token 未設定かつ user access token 指定ありの場合だけ警告ログを出して既存の access token をそのまま使う fallback を残します。その場合、期限切れ時には従来通り失敗します。

最終的な validation 方針は以下です。

- OAuth 1.0a投稿用の `-twitter-access-token` / `-twitter-access-token-secret` は従来通り必須
- OAuth 2.0メディアアップロード用は `-twitter-user-refresh-token` と `-twitter-oauth2-client-id` を推奨必須
- `-twitter-user-access-token` は任意
- `-twitter-user-refresh-token` がない場合は `-twitter-user-access-token` だけで起動可能にしてもよいが、期限切れ時に失敗する警告を出す

## テスト観点

追加する単体テスト:

- 保存ファイルがない場合、フラグ由来の access token / refresh token を使える
- 保存ファイルがなく、フラグ由来の access token もない場合、refresh token から初回 access token を取得する
- `expires_at` が十分未来なら refresh request を送らない
- access token が空なら refresh request を送る
- `expires_at` が5分以内なら refresh request を送る
- refresh response の `refresh_token` で保存ファイルを更新する
- refresh response に `refresh_token` がない場合は既存 refresh token を保持する
- refresh が同時に呼ばれてもHTTP request が1回だけになる
- 401を受けたメディアアップロード処理が1回だけ強制 refresh して再試行する
- refresh失敗時に token値をエラーメッセージへ含めない

完了前に必ず実行するコマンド:

```bash
go test ./...
golangci-lint run
```

## 実装順序

1. `Config`、flag、compose.yaml、README.md に OAuth2 refresh 用設定を追加する
2. `internal/twitter/oauth2_token.go` に `TokenManager` と JSON file store を実装する
3. `-twitter-user-access-token` を必須 validation から外す
4. 起動時に `TokenManager` を作り、`handler.Config` 経由で `twitter` パッケージへ渡す
5. Media API v2 chunked upload の Bearer token 指定を `TokenManager` 経由に変更する
6. access token が空でも初回 `BearerToken(ctx)` で refresh できるようにする
7. 401時の1回だけ強制refresh + retryを入れる
8. 単体テストを追加する
9. README.md に refresh token の取得条件として `offline.access` scope が必要なことを書く
10. `go test ./...` と `golangci-lint run` を通す

# OAuth 2.0 PKCE Setup Flow 方針メモ

## 背景

Misskey から Twitter へ画像付きで投稿するには、Twitter Media API v2 の upload が必要になる。
この upload には OAuth 2.0 User Access Token が必要で、少なくとも次の scope が必要になる。

- `tweet.read`
- `tweet.write`
- `users.read`
- `media.write`
- `offline.access`

`media.write` がない token では `/2/media/upload` が `403 Forbidden` になる。
`offline.access` は refresh token を得るために必要。

## 採用する OAuth 方針

Authorization Code Flow with PKCE に寄せる。
public client として扱い、`client_secret` は使わない。

理由:

- Kubernetes / Docker 運用で `client_secret` を保持しても、この用途では運用コストに対する利点が薄い。
- PKCE により authorization code の横取り対策ができる。
- 初回認可後は `offline.access` で得た refresh token により自動更新できる。
- ブラウザ認可が必要なのは、初回、scope 変更時、ユーザーがアプリ連携を解除した時、token store を失った時に限られる。

## 現状設計の問題点

現在の設計は `TWITTER_USER_ACCESS_TOKEN` / `TWITTER_USER_REFRESH_TOKEN` を設定値として渡す前提が強い。
これは Kubernetes / Docker 運用では扱いづらい。

- access token は短命で、起動設定として持たせる意味が薄い。
- refresh token はローテーションされるため、初期値を Secret に固定しても古くなる。
- token store JSON と env の refresh token が二重管理になり、どちらが正か分かりづらい。
- token store を削除すると、古い env refresh token に戻って refresh 失敗することがある。
- 初回 OAuth 認可を手元バイナリで行う前提は、Kubernetes / Docker の運用感に合わない。

## 新しい全体方針

アプリ本体が OAuth 2.0 PKCE の入口と callback を提供する。

- `GET /twitter/login?auth=<short-lived-admin-token>`
- `GET /twitter/callback`

有効な OAuth 2.0 token がない場合、または refresh token で復旧できない場合は、アプリが短命の管理用 auth token を生成してログに出す。
運用者はログに出た token を使って `/twitter/login?auth=...` にアクセスし、Twitter の認可画面へ進む。
Twitter から `/twitter/callback` に戻ったら、アプリが authorization code を `code_verifier` 付きで token endpoint に交換し、access token / refresh token を token store に保存する。

アプリは Kubernetes API を叩かない。
Kubernetes Secret / PVC / volume mount は外側のデプロイ責務であり、アプリはローカルファイルとして `TWITTER_TOKEN_STORE_PATH` を読む/書くだけにする。

## 設定方針

残す設定:

- `TWITTER_OAUTH2_CLIENT_ID`
- `TWITTER_OAUTH2_REDIRECT_URL`
- `TWITTER_TOKEN_STORE_PATH`

廃止する設定:

- `TWITTER_OAUTH2_CLIENT_SECRET`
- `TWITTER_USER_ACCESS_TOKEN`
- `TWITTER_USER_REFRESH_TOKEN`

後方互換は考えない。

`TWITTER_OAUTH2_REDIRECT_URL` は Twitter Developer Portal の callback URL と完全一致させる。

例:

```text
TWITTER_OAUTH2_REDIRECT_URL=https://<host>/twitter/callback
```

## Runtime の動き

### 正常時

1. `TWITTER_TOKEN_STORE_PATH` から token store を読む。
2. access token が有効ならそれを使う。
3. access token が期限切れに近い場合、refresh token で更新する。
4. refresh 成功時は新しい access token / refresh token を token store に保存する。

### token がない、または refresh できない時

1. サービス自体は起動し続ける。
2. 画像付き投稿など OAuth 2.0 token が必要な処理は「認可が必要」として失敗する。
3. ログに短命の login URL を出す。
4. 運用者が `/twitter/login?auth=...` にアクセスする。
5. アプリが PKCE 用の `code_verifier` / `code_challenge` と `state` を生成する。
6. アプリが Twitter authorize URL に redirect する。
7. Twitter が `/twitter/callback` に `code` と `state` を返す。
8. アプリが `state` を検証する。
9. アプリが `code_verifier` を使って code を token endpoint で交換する。
10. アプリが token store を保存する。
11. 以後は refresh token で自動更新する。

ここで「初回起動時に token store がない」ことは起動エラーにしない。
サービス自体は起動し、認可が必要になった時点で login URL をログに出す。

## 管理用 auth token

`/twitter/login` を誰でも使える状態にはしない。

方針:

- token はサーバー内部で生成するランダム値。
- 有効期限は短くする。例: 5分。
- 1回使ったら無効化する。
- token はログにだけ出す。
- `/twitter/login?auth=...` で一致した場合だけ Twitter authorize URL に redirect する。

注意:

- ログ閲覧権限を持つ人は Twitter 連携の再認可ができる。
- これは運用上の管理権限として扱う。
- 必要なら追加で固定の管理 secret を要求する設計も検討する。

## PKCE / state

login 開始時に次を生成する。

- `code_verifier`
- `code_challenge`
- `state`

保存:

- 短時間だけメモリに保持する。
- 保存期限は短くする。例: 5分。
- callback 成功/失敗後は削除する。

callback 時:

- `state` を検証する。
- 対応する `code_verifier` で token exchange する。
- `client_secret` は使わない。

Pod 再起動で state が失われた場合は callback を失敗させ、ログに新しい login token を出してやり直す。
これは許容する。

## 必要な新規エンドポイント

### `GET /twitter/login`

入力:

- `auth`

処理:

- auth token を検証する。
- auth token を無効化する。
- state / PKCE verifier / challenge を生成する。
- Twitter authorize URL に redirect する。

authorize URL の scope:

```text
tweet.read tweet.write users.read media.write offline.access
```

### `GET /twitter/callback`

入力:

- `code`
- `state`
- `error` / `error_description`

処理:

- `error` があれば表示して終了。
- `state` を検証する。
- 対応する `code_verifier` を取り出す。
- code を token endpoint に交換する。
- token store に保存する。
- 成功ページを返す。

## TokenManager の変更方針

- env access token / env refresh token に依存しない。
- `client_secret` に依存しない。
- token store が無くても `NewTokenManager` は作れる。
- `BearerToken(ctx)` は token store が無い場合に「認可が必要」という typed error を返す。
- refresh token が無い場合も「認可が必要」という typed error を返す。
- refresh 失敗時も「認可が必要」として扱える typed error を返す。
- token exchange 成功後に token store を保存し、以後 `BearerToken(ctx)` が使えるようにする。

## ログ方針

OAuth 2.0 token が必要だが取得できない場合:

- `Twitter OAuth 2.0 authorization required`
- `login_url=https://<host>/twitter/login?auth=<token>`
- `expires_at=...`

ログに出さないもの:

- access token
- refresh token
- authorization code
- `code_verifier`
- `state`

ログに出すもの:

- 短命の login auth token を含む URL

## README 更新方針

書くべきこと:

- OAuth 2.0 は Authorization Code Flow with PKCE を使う。
- `client_secret` は使わない。
- 必要 scope は `tweet.read tweet.write users.read media.write offline.access`。
- `TWITTER_USER_ACCESS_TOKEN` / `TWITTER_USER_REFRESH_TOKEN` / `TWITTER_OAUTH2_CLIENT_SECRET` は設定しない。
- `TWITTER_TOKEN_STORE_PATH` は writable かつ永続化された場所に置く。
- Twitter Developer Portal の callback URL に `https://<host>/twitter/callback` を登録する。
- 初回または token 失効時はログに出る `/twitter/login?auth=...` にアクセスして再認可する。

## Kubernetes / Docker 運用メモ

- アプリは Kubernetes API を叩かない。
- token store は通常ファイルとして扱う。
- `TWITTER_TOKEN_STORE_PATH` は永続 volume 上に置く。
- Secret に access token / refresh token を貼り付ける設計にはしない。
- 複数 replica は refresh token ローテーションと相性が悪いので、基本は replica 1 とする。

## 未決事項

- login URL の host は明示設定にするか、request から推定するか。
  - 推奨は `TWITTER_OAUTH2_REDIRECT_URL` を明示設定し、そこから login URL の origin も組み立てる。
- login auth token を自動生成するタイミング。
  - 起動時に token store が無ければ生成。
  - OAuth 2.0 が必要な処理で token 不足が分かった時にも生成。
- token store 更新の排他。
  - 基本は replica 1 を前提にする。
  - 同一 Pod 内では mutex で守る。
- 認可成功後に失敗していた投稿を自動 retry するか。
  - 初期案では retry しない。
  - 次の webhook / 手動再送で処理する。

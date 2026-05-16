# Twitter画像をMisskeyノートへ添付する実装方針

## 現状

- Misskey -> Twitter は `internal/handler/note2tweet.go` でノート添付ファイルを拾い、`internal/twitter/client.go` の `PostWithMedia` でTwitterへアップロードしている。
- Twitter -> Misskey は `internal/handler/tweet2note.go` で Account Activity payload から本文だけを取り出し、`internal/misskey/client.go` の `CreateNote` で `/api/notes/create` に投稿している。
- そのため、Twitter webhook payload に画像情報が含まれていても、Misskey Drive へのアップロードと `notes/create.fileIds` 指定が無い。

## Misskey APIの使い方

`api-1.json` から、この実装で使うエンドポイントは以下。

- `POST /api/drive/files/create`
  - multipart/form-data
  - 必須: `file`
  - 任意: `name`, `comment`, `isSensitive`, `force`, `folderId`
  - レスポンスは `DriveFile`。ノート添付には返却された `id` を使う。
  - 必要権限: `write:drive`
- `POST /api/notes/create`
  - application/json
  - 添付ファイルは `fileIds` または `mediaIds`
  - `fileIds` は最大16件だが、Twitter画像由来なら最大4件に制限するのが自然。
  - 必要権限: `write:notes`

既存の `CreateNote` は JSON body に `i` を入れて認証しているが、手元のcurl確認で `drive/files/create` と `notes/create` のどちらも `Authorization: Bearer <MISSKEY_TOKEN>` で成功した。そのため、今回のスコープで Misskey API 呼び出しは Bearer ヘッダー認証へ統一する。

確認済み:

- `POST /api/drive/files/create`
  - `Authorization: Bearer <MISSKEY_TOKEN>`
  - multipart `file=@./sample.png`
  - `200 OK` で `DriveFile.id` が返った。
- `POST /api/notes/create`
  - `Authorization: Bearer <MISSKEY_TOKEN>`
  - JSON body は `text`, `visibility`, `localOnly` のみで、`i` なし。
  - `200 OK` で `createdNote` が返った。

実装では `i` を送らない。Misskey API token は HTTP header にだけ乗せる。

## Twitter payloadの取り出し

`tweetObject` にメディア用フィールドを追加する。

```go
type tweetObject struct {
    IDStr            string           `json:"id_str"`
    Text             string           `json:"text"`
    FullText         string           `json:"full_text"`
    Truncated        bool             `json:"truncated"`
    User             twitterUser      `json:"user"`
    Entities         twitterEntities  `json:"entities"`
    ExtendedEntities twitterEntities  `json:"extended_entities"`
    ExtendedTweet    extendedTweet    `json:"extended_tweet"`
}

type extendedTweet struct {
    FullText         string          `json:"full_text"`
    Entities         twitterEntities `json:"entities"`
    ExtendedEntities twitterEntities `json:"extended_entities"`
}

type twitterEntities struct {
    Media []twitterMedia `json:"media"`
}

type twitterMedia struct {
    Type          string `json:"type"`
    MediaURLHTTPS string `json:"media_url_https"`
    MediaURL      string `json:"media_url"`
}
```

本文は `extended_tweet.full_text` を最優先し、無ければ既存通り `full_text`、最後に `text` を使う。X公式の Account Activity API では、longform post の場合に `text` が先頭140文字で `truncated: true` になり、全文は `extended_tweet.full_text` に入ると説明されているため。

メディアURLの取り出し順は以下にする。

1. `extended_tweet.extended_entities.media`
2. `extended_tweet.entities.media`
3. `extended_entities.media`
4. `entities.media`

`type == "photo"` のみ対象にし、URL は `media_url_https` を優先する。

公式の Account Activity API リファレンスは `tweet_create_events` の中身を `<Tweet Object>` としていて、画像付きpayloadの具体例までは載せていない。そのため、実装時は手元の実payloadを1件保存して `extended_entities.media` / `entities.media` / `extended_tweet` のどこに画像情報が入るかを確認する。payloadに画像URLが含まれない契約だった場合の代替案は、`tweet.id_str` を使って Post Lookup API を追加で呼び、`tweet.fields=attachments&expansions=attachments.media_keys&media.fields=url,preview_image_url,alt_text` で media URL を取得すること。

`IncomingTweet` には `MediaURLs []string` を追加する。既存テストへの影響を小さくするため、本文やURLの組み立てロジックはそのままにして、画像URLだけを追加で保持する。

## Misskeyクライアントの変更

`internal/misskey/client.go` に以下を追加する。

- `CreateNoteWithFiles(ctx, host, token, text string, fileIDs []string) error`
  - `fileIDs` が空なら既存 `CreateNote` と同じ挙動。
  - `fileIDs` がある場合は `/api/notes/create` の JSON に `fileIds` を含める。
  - 認証は `Authorization: Bearer <token>` を使い、JSON body に `i` は入れない。
  - テキストが空の画像ツイートも投稿できるように、`text` は空なら `nil` 扱いにする。ただし現在の payload では画像URL等が本文に入る可能性が高いので、まずは既存本文を維持する。
- `UploadDriveFileFromURL(ctx, host, token, fileURL string) (string, error)`
  - Twitter画像URLをHTTP GETで取得する。
  - bodyが空、非2xx、Content-Typeが `image/` 以外ならエラー。
  - multipart/form-data で `/api/drive/files/create` にアップロードする。
  - 認証は `Authorization: Bearer <token>` を使い、multipart field に `i` は入れない。
  - レスポンスJSONから `id` を返す。

既存の `CreateNote` も内部的には `CreateNoteWithFiles(ctx, host, token, text, nil)` に寄せ、認証方式を Bearer に変更する。これで `notes/create` のテキストのみ投稿とファイル付き投稿のコードパスを揃える。

実装上は、Twitter側の `uploadMediaFromURL` と似た処理になるため、まずは Misskey クライアント内に閉じて実装する。将来、ダウンロード処理の重複が気になる場合だけ共通化する。

## URL検証

Twitter webhook は署名検証済みだが、外部URLをサーバーから取得するため SSRF 対策を入れる。

- HTTPSのみ許可。
- 許可ホストは環境変数 `TWITTER_MEDIA_HOSTS` で指定する。
- 未設定時のデフォルトは `pbs.twimg.com,video.twimg.com`。
- 今回は画像のみ対象なので、実質 `pbs.twimg.com` が主対象。

Misskey -> Twitter で既に `MISSKEY_MEDIA_HOST` を使った検証があるため、逆方向も同じ思想で揃える。

## Tweet2Noteの処理フロー

`HandleIncomingTweet` の投稿部分を以下に変更する。

1. RNパターン、環境変数、重複判定は現状通り。
2. `tweet.MediaURLs` を最大4件まで処理する。
3. 各URLを `misskey.UploadDriveFileFromURL` でDriveへアップロードし、`fileID` を集める。
4. `fileID` が空なら `misskey.CreateNote`、空でなければ `misskey.CreateNoteWithFiles` を呼ぶ。
5. ログに `has_media` と `media_count` を追加する。

重要な挙動として、画像アップロードが1件でも失敗した場合はノート投稿全体を失敗扱いにする。部分添付で投稿するとTwitter側との対応が崩れ、リトライ時の状態も読みにくくなるため。

## テスト方針

追加・更新するテストは以下。

- `parseAccountActivityPayload`
  - `extended_tweet.full_text` が `full_text` / `text` より優先される。
  - `extended_entities.media` の `photo` を `IncomingTweet.MediaURLs` に入れる。
  - `extended_tweet.extended_entities.media` を最優先で読む。
  - `entities.media` fallback を確認する。
  - `video`, `animated_gif` は今回対象外として無視する。
  - `media_url_https` が空の場合は `media_url` を使うか、HTTPSでなければ後段検証で落とす。
- `misskey` package
  - `CreateNoteWithFiles` が `fileIds` をJSONに含める。
  - `UploadDriveFileFromURL` が画像をダウンロードして multipart で `drive/files/create` に送る。
  - 非画像Content-Type、空body、非2xx、許可外ホストでエラーになる。
- handler
  - 画像付きtweetでMisskey Drive upload後に `fileIds` 付きノート作成へ進む。

既存の `Tweet2NoteHandler` は現在、実API呼び出しを直接行うため handler の単体テストが書きにくい。実装時に最小限の差し替えポイントを入れる。

例:

```go
var createMisskeyNoteWithFiles = misskey.CreateNoteWithFiles
var uploadMisskeyDriveFileFromURL = misskey.UploadDriveFileFromURL
```

既存設計に合わせた軽い関数変数に留め、インターフェース導入のような大きい変更は避ける。

## 実装順

1. `IncomingTweet` と Account Activity payload 構造体にメディア情報を追加する。
2. payload parsing の単体テストを追加する。
3. `internal/misskey/client.go` に `CreateNoteWithFiles` と Drive upload を追加する。
4. 既存 `CreateNote` の認証を JSON body の `i` から `Authorization: Bearer` へ変更する。
5. URL検証 helper とテストを追加する。
6. `HandleIncomingTweet` でDrive upload -> `fileIds` 付きノート投稿を呼ぶ。
7. READMEに `TWITTER_MEDIA_HOSTS`、Misskey token に `write:drive` が必要なこと、Misskey API は Bearer ヘッダーで呼ぶことを追記する。
8. `go test ./...` で確認する。

## 注意点

- Twitter画像付き投稿の本文には `https://t.co/...` が含まれる場合がある。初回実装では本文加工を増やさず、既存挙動を保つ。
- Misskeyの `notes/create.fileIds` は最大16件だが、Twitter画像は最大4件までに制限する。
- `api-1.json` では `notes/create` に `fileIds` と `mediaIds` の両方がある。既存Misskey APIの慣例と webhook payload の `fileIds` に合わせ、まずは `fileIds` を使う。
- 動画やGIFは今回の対象外。必要になったら Misskey Drive upload 自体は流用し、Twitter payload の `video_info.variants` 選択を別途実装する。

## 参照したX公式ドキュメント

- Account Activity API: `tweet_create_events` は webhook でリアルタイム配信される。longform post は `extended_tweet.full_text` に全文が入る。
- Webhooks Quickstart: CRC は `crc_token` と consumer secret でHMAC SHA-256を作り、POST署名検証も raw body と consumer secret で行う。現行実装の方向性は合っている。
- Media API: APIで投稿へ添付する場合は media upload -> `media_id` -> `POST /2/tweets.media.media_ids` の流れ。今回の逆方向では upload API は直接使わないが、既存 Misskey -> Twitter 実装の前提確認として有効。
- Fields: v2 Post Lookup で media を取る場合は `tweet.fields=attachments`, `expansions=attachments.media_keys`, `media.fields=url,preview_image_url,alt_text` を使う。

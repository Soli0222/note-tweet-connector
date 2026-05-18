# Twitter OAuth 2.0 Unification Plan

## Goal

Twitter API authentication for user-context actions should use OAuth 2.0 only.
There is no migration period: remove OAuth 1.0a support and stop accepting OAuth 1.0a credentials.

Keep two OAuth 2.0 token types conceptually separate:

- User Context OAuth 2.0 token from Authorization Code Flow with PKCE
  - Used for `POST /2/tweets`
  - Used for Media API v2 upload
  - Requires `tweet.read tweet.write users.read media.write offline.access`
- Application-Only Bearer Token
  - Used for Filtered Stream and stream rule management
  - Keep `-twitter-bearer-token` unless we intentionally decide to use the user token for stream later

## Current State

- Tweet creation currently uses OAuth 1.0a in `internal/twitter/client.go`.
- Media upload already uses OAuth 2.0 via `BearerTokenSource`.
- OAuth 2.0 login, token refresh, token store, and required scopes already exist.
- Runtime configuration still requires OAuth 1.0a fields:
  - `-twitter-api-key`
  - `-twitter-api-key-secret`
  - `-twitter-access-token`
  - `-twitter-access-token-secret`

## Implementation Steps

1. Update Tweet posting to use OAuth 2.0 Bearer auth.
   - Remove `github.com/dghubble/oauth1` usage from `internal/twitter/client.go`.
   - Change `PostWithOptionsConfig` to obtain a token from `cfg.bearerTokenSource()`.
   - Send `POST /2/tweets` with `Authorization: Bearer <token>`.
   - Preserve existing request body behavior for text, media IDs, and `quote_tweet_id`.
   - Reuse the existing refresh-on-401 pattern used by media upload.

2. Remove OAuth 1.0a fields from `twitter.Config`.
   - Remove `APIKey`.
   - Remove `APIKeySecret`.
   - Remove `AccessToken`.
   - Remove `AccessTokenSecret`.
   - Replace `Config.validate()` with validation that ensures a `BearerTokenSource` or OAuth 2.0 token manager configuration is available.

3. Remove OAuth 1.0a CLI flags and config fields.
   - Delete the OAuth 1.0a fields from `cmd/note-tweet-connector/main.go`.
   - Stop registering the corresponding flags.
   - Stop requiring them in `Config.validate()`.
   - Stop passing them into `twitter.Config`.

4. Update deployment configuration.
   - Remove OAuth 1.0a args and environment variables from `compose.yaml`.
   - Update README examples and configuration tables.
   - Keep OAuth 2.0 Client ID, redirect URL, token store path, and Application-Only Bearer Token.

5. Remove dependency.
   - Delete `github.com/dghubble/oauth1` from `go.mod`.
   - Run `go mod tidy`.

6. Update tests.
   - Add or update Tweet posting tests to assert `Authorization: Bearer <token>`.
   - Cover 401 refresh and retry for `POST /2/tweets`.
   - Keep existing media upload OAuth 2.0 tests.
   - Update config validation tests if present.

## Operational Notes

- Existing OAuth 2.0 token stores are usable only if they include the needed scopes.
- If an existing token was authorized before `tweet.write` or `media.write` was added, delete the token store and reauthorize through `/twitter/login`.
- After this change, deployments must remove OAuth 1.0a environment variables; they will no longer be read.

## Verification

Run:

```sh
go test ./...
golangci-lint run
```

Manual verification should include:

- Text-only Tweet from Misskey note.
- Tweet with image media.
- Quote Tweet path from own quote renote.
- Filtered Stream still connects and processes incoming tweets.

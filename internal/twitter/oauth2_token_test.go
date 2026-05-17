package twitter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenManagerRefreshesWithoutBootstrapAccessToken(t *testing.T) {
	ctx := context.Background()
	var refreshRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshRequests, 1)
		if got := r.FormValue("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := r.FormValue("refresh_token"); got != "refresh-1" {
			t.Fatalf("refresh_token = %q, want refresh-1", got)
		}
		if got := r.FormValue("client_id"); got != "client-1" {
			t.Fatalf("client_id = %q, want client-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"bearer","expires_in":3600,"access_token":"access-2","refresh_token":"refresh-2","scope":"tweet.read tweet.write users.read media.write offline.access"}`))
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	storePath := filepath.Join(t.TempDir(), "token.json")
	if err := saveOAuth2Token(storePath, OAuth2Token{
		RefreshToken: "refresh-1",
		TokenType:    "bearer",
	}); err != nil {
		t.Fatalf("saveOAuth2Token() error = %v", err)
	}
	manager, err := NewTokenManager(OAuth2Config{
		ClientID:       "client-1",
		TokenStorePath: storePath,
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	token, err := manager.BearerToken(ctx)
	if err != nil {
		t.Fatalf("BearerToken() error = %v", err)
	}
	if token != "access-2" {
		t.Fatalf("BearerToken() = %q, want access-2", token)
	}
	if got := atomic.LoadInt32(&refreshRequests); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var saved OAuth2Token
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if saved.AccessToken != "access-2" || saved.RefreshToken != "refresh-2" {
		t.Fatalf("saved token = %#v, want refreshed access and refresh tokens", saved)
	}
	if saved.ExpiresAt.IsZero() {
		t.Fatal("saved ExpiresAt is zero")
	}
}

func TestTokenManagerUsesFreshStoredAccessToken(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("refresh endpoint should not be called")
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	storePath := filepath.Join(t.TempDir(), "token.json")
	if err := saveOAuth2Token(storePath, OAuth2Token{
		AccessToken:  "stored-access",
		RefreshToken: "stored-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		TokenType:    "bearer",
	}); err != nil {
		t.Fatalf("saveOAuth2Token() error = %v", err)
	}

	manager, err := NewTokenManager(OAuth2Config{
		ClientID:       "client-1",
		TokenStorePath: storePath,
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	token, err := manager.BearerToken(ctx)
	if err != nil {
		t.Fatalf("BearerToken() error = %v", err)
	}
	if token != "stored-access" {
		t.Fatalf("BearerToken() = %q, want stored-access", token)
	}
}

func TestTokenManagerConcurrentRefreshOnlyOnce(t *testing.T) {
	ctx := context.Background()
	var refreshRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"bearer","expires_in":3600,"access_token":"access-2","refresh_token":"refresh-2"}`))
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	storePath := filepath.Join(t.TempDir(), "token.json")
	if err := saveOAuth2Token(storePath, OAuth2Token{
		RefreshToken: "refresh-1",
		TokenType:    "bearer",
	}); err != nil {
		t.Fatalf("saveOAuth2Token() error = %v", err)
	}
	manager, err := NewTokenManager(OAuth2Config{
		ClientID:       "client-1",
		TokenStorePath: storePath,
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := manager.BearerToken(ctx)
			if err != nil {
				errs <- err
				return
			}
			if token != "access-2" {
				errs <- errUnexpectedToken(token)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&refreshRequests); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
}

func TestPostMediaFormRefreshesAndRetriesOnUnauthorized(t *testing.T) {
	ctx := context.Background()
	source := &recordingTokenSource{token: "old-token"}
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		switch r.Header.Get("Authorization") {
		case "Bearer old-token":
			http.Error(w, "expired", http.StatusUnauthorized)
		case "Bearer new-token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"media-1"}}`))
		default:
			t.Fatalf("unexpected Authorization header %q", r.Header.Get("Authorization"))
		}
	}))
	defer server.Close()

	oldEndpoint := UploadMediaEndpoint
	UploadMediaEndpoint = server.URL
	defer func() { UploadMediaEndpoint = oldEndpoint }()

	var response UploadMediaResponse
	if err := postMediaForm(ctx, source, map[string]string{"command": "INIT"}, "", nil, &response); err != nil {
		t.Fatalf("postMediaForm() error = %v", err)
	}
	if response.Data.ID != "media-1" {
		t.Fatalf("response media id = %q, want media-1", response.Data.ID)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if source.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", source.refreshes)
	}
}

func TestPostMediaFormForbiddenErrorIncludesUploadContext(t *testing.T) {
	ctx := context.Background()
	source := StaticBearerTokenSource{Token: "access-token"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"title":"Forbidden","detail":"Forbidden"}`))
	}))
	defer server.Close()

	oldEndpoint := UploadMediaEndpoint
	UploadMediaEndpoint = server.URL
	defer func() { UploadMediaEndpoint = oldEndpoint }()

	err := postMediaForm(ctx, source, map[string]string{"command": "INIT"}, "", nil, nil)
	if err == nil {
		t.Fatal("postMediaForm() succeeded, want error")
	}
	for _, want := range []string{"media upload INIT failed with status 403", "media.write", "tweet.write", "Media API access"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("postMediaForm() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestTokenManagerRedactsKnownTokensFromRefreshErrors(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `invalid refresh-1 and access-1`, http.StatusBadRequest)
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	storePath := filepath.Join(t.TempDir(), "token.json")
	if err := saveOAuth2Token(storePath, OAuth2Token{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "bearer",
	}); err != nil {
		t.Fatalf("saveOAuth2Token() error = %v", err)
	}
	manager, err := NewTokenManager(OAuth2Config{
		ClientID:       "client-1",
		TokenStorePath: storePath,
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	_, err = manager.BearerToken(ctx)
	if err == nil {
		t.Fatal("BearerToken() succeeded, want error")
	}
	if strings.Contains(err.Error(), "refresh-1") || strings.Contains(err.Error(), "access-1") {
		t.Fatalf("BearerToken() error leaked token value: %v", err)
	}
	if !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("BearerToken() error = %v, want ErrAuthorizationRequired", err)
	}
}

func TestTokenManagerWithoutStoredTokenReturnsAuthorizationRequired(t *testing.T) {
	manager, err := NewTokenManager(OAuth2Config{ClientID: "client-1"})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	if !manager.AuthorizationRequired() {
		t.Fatal("AuthorizationRequired() = false, want true")
	}

	_, err = manager.BearerToken(context.Background())
	if !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("BearerToken() error = %v, want ErrAuthorizationRequired", err)
	}
}

func TestTokenManagerExchangesAuthorizationCode(t *testing.T) {
	ctx := context.Background()
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if got := r.FormValue("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.FormValue("code"); got != "code-1" {
			t.Fatalf("code = %q, want code-1", got)
		}
		if got := r.FormValue("code_verifier"); got != "verifier-1" {
			t.Fatalf("code_verifier = %q, want verifier-1", got)
		}
		if got := r.FormValue("redirect_uri"); got != "https://example.com/twitter/callback" {
			t.Fatalf("redirect_uri = %q, want configured redirect URL", got)
		}
		if got := r.FormValue("client_id"); got != "client-1" {
			t.Fatalf("client_id = %q, want client-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"bearer","expires_in":3600,"access_token":"access-1","refresh_token":"refresh-1","scope":"tweet.read tweet.write users.read media.write offline.access"}`))
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	storePath := filepath.Join(t.TempDir(), "token.json")
	manager, err := NewTokenManager(OAuth2Config{
		ClientID:       "client-1",
		RedirectURL:    "https://example.com/twitter/callback",
		TokenStorePath: storePath,
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	if err := manager.ExchangeAuthorizationCode(ctx, "code-1", "verifier-1"); err != nil {
		t.Fatalf("ExchangeAuthorizationCode() error = %v", err)
	}
	if !sawRequest {
		t.Fatal("token endpoint was not called")
	}

	token, err := manager.BearerToken(ctx)
	if err != nil {
		t.Fatalf("BearerToken() error = %v", err)
	}
	if token != "access-1" {
		t.Fatalf("BearerToken() = %q, want access-1", token)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var saved OAuth2Token
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if saved.AccessToken != "access-1" || saved.RefreshToken != "refresh-1" {
		t.Fatalf("saved token = %#v, want exchanged tokens", saved)
	}
}

func TestOAuth2LoginManagerIssuesLoginAndAuthorizeURL(t *testing.T) {
	manager, err := NewTokenManager(OAuth2Config{
		ClientID:    "client-1",
		RedirectURL: "https://example.com/twitter/callback",
	})
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	loginManager, err := NewOAuth2LoginManager(manager, OAuth2Config{
		ClientID:    "client-1",
		RedirectURL: "https://example.com/twitter/callback",
	})
	if err != nil {
		t.Fatalf("NewOAuth2LoginManager() error = %v", err)
	}

	oldAuthorizeEndpoint := OAuth2AuthorizeEndpoint
	OAuth2AuthorizeEndpoint = "https://twitter.example/authorize"
	defer func() { OAuth2AuthorizeEndpoint = oldAuthorizeEndpoint }()

	loginURL, expiresAt, err := loginManager.IssueLoginURL()
	if err != nil {
		t.Fatalf("IssueLoginURL() error = %v", err)
	}
	if expiresAt.IsZero() {
		t.Fatal("IssueLoginURL() returned zero expiry")
	}
	parsedLoginURL, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("Parse(loginURL) error = %v", err)
	}
	if got := parsedLoginURL.String(); !strings.HasPrefix(got, "https://example.com/twitter/login?auth=") {
		t.Fatalf("login URL = %q, want callback origin and /twitter/login", got)
	}

	authorizeURL, err := loginManager.BeginLogin(parsedLoginURL.Query().Get("auth"))
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	parsedAuthorizeURL, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("Parse(authorizeURL) error = %v", err)
	}
	q := parsedAuthorizeURL.Query()
	if got := parsedAuthorizeURL.Scheme + "://" + parsedAuthorizeURL.Host + parsedAuthorizeURL.Path; got != "https://twitter.example/authorize" {
		t.Fatalf("authorize endpoint = %q, want test endpoint", got)
	}
	if got := q.Get("client_id"); got != "client-1" {
		t.Fatalf("client_id = %q, want client-1", got)
	}
	if got := q.Get("redirect_uri"); got != "https://example.com/twitter/callback" {
		t.Fatalf("redirect_uri = %q, want configured redirect URL", got)
	}
	if got := q.Get("scope"); got != OAuth2Scope {
		t.Fatalf("scope = %q, want %q", got, OAuth2Scope)
	}
	if q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("authorize URL is missing state or code_challenge: %q", authorizeURL)
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}

	if _, err := loginManager.BeginLogin(parsedLoginURL.Query().Get("auth")); !errors.Is(err, ErrInvalidLoginAuth) {
		t.Fatalf("BeginLogin() reused auth error = %v, want ErrInvalidLoginAuth", err)
	}
}

type recordingTokenSource struct {
	token     string
	refreshes int
}

func (s *recordingTokenSource) BearerToken(ctx context.Context) (string, error) {
	return s.token, nil
}

func (s *recordingTokenSource) Refresh(ctx context.Context) error {
	s.refreshes++
	s.token = "new-token"
	return nil
}

type errUnexpectedToken string

func (e errUnexpectedToken) Error() string {
	return "unexpected token " + string(e)
}

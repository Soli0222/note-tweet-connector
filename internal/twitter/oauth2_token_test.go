package twitter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		_, _ = w.Write([]byte(`{"token_type":"bearer","expires_in":3600,"access_token":"access-2","refresh_token":"refresh-2","scope":"tweet.write offline.access"}`))
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	storePath := filepath.Join(t.TempDir(), "token.json")
	manager, err := NewTokenManager(OAuth2Config{
		ClientID:       "client-1",
		RefreshToken:   "refresh-1",
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
		RefreshToken:   "refresh-1",
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

	manager, err := NewTokenManager(OAuth2Config{
		ClientID:     "client-1",
		RefreshToken: "refresh-1",
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

func TestTokenManagerRedactsKnownTokensFromRefreshErrors(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `invalid refresh-1 and access-1`, http.StatusBadRequest)
	}))
	defer server.Close()

	oldEndpoint := OAuth2TokenEndpoint
	OAuth2TokenEndpoint = server.URL
	defer func() { OAuth2TokenEndpoint = oldEndpoint }()

	manager, err := NewTokenManager(OAuth2Config{
		ClientID:     "client-1",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
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

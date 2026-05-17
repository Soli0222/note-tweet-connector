package twitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const tokenRefreshLeeway = 5 * time.Minute

var OAuth2TokenEndpoint = "https://api.x.com/2/oauth2/token"

var ErrAuthorizationRequired = errors.New("twitter OAuth 2.0 authorization required")

type BearerTokenSource interface {
	BearerToken(ctx context.Context) (string, error)
}

type ForceRefreshBearerTokenSource interface {
	BearerTokenSource
	Refresh(ctx context.Context) error
}

type StaticBearerTokenSource struct {
	Token string
}

func (s StaticBearerTokenSource) BearerToken(ctx context.Context) (string, error) {
	if s.Token == "" {
		return "", fmt.Errorf("twitter OAuth 2.0 access token is not configured")
	}
	return s.Token, nil
}

type OAuth2Config struct {
	ClientID       string
	RedirectURL    string
	TokenStorePath string
}

type OAuth2Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
}

type TokenManager struct {
	mu         sync.Mutex
	cfg        OAuth2Config
	httpClient *http.Client
	token      OAuth2Token
}

type AuthorizationRequiredError struct {
	Reason string
	Err    error
}

func (e *AuthorizationRequiredError) Error() string {
	if e.Reason == "" {
		return ErrAuthorizationRequired.Error()
	}
	if e.Err != nil {
		return ErrAuthorizationRequired.Error() + ": " + e.Reason + ": " + e.Err.Error()
	}
	return ErrAuthorizationRequired.Error() + ": " + e.Reason
}

func (e *AuthorizationRequiredError) Unwrap() error {
	return e.Err
}

func (e *AuthorizationRequiredError) Is(target error) bool {
	return target == ErrAuthorizationRequired
}

func authorizationRequired(reason string, err error) error {
	return &AuthorizationRequiredError{Reason: reason, Err: err}
}

func NewTokenManager(cfg OAuth2Config) (*TokenManager, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("twitter OAuth 2.0 client id is not configured")
	}

	m := &TokenManager{
		cfg:        cfg,
		httpClient: httpClient,
		token:      OAuth2Token{TokenType: "bearer"},
	}

	if cfg.TokenStorePath != "" {
		token, ok, err := loadOAuth2Token(cfg.TokenStorePath)
		if err != nil {
			return nil, err
		}
		if ok {
			m.token = token
		}
	}

	return m, nil
}

func (m *TokenManager) BearerToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token.AccessToken != "" && !m.shouldRefreshLocked(time.Now()) {
		return m.token.AccessToken, nil
	}
	if err := m.refreshLocked(ctx); err != nil {
		return "", err
	}
	return m.token.AccessToken, nil
}

func (m *TokenManager) AuthorizationRequired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token.AccessToken != "" && !m.shouldRefreshLocked(time.Now()) {
		return false
	}
	return m.token.RefreshToken == ""
}

func (m *TokenManager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshLocked(ctx)
}

func (m *TokenManager) shouldRefreshLocked(now time.Time) bool {
	if m.token.AccessToken == "" {
		return true
	}
	if m.token.ExpiresAt.IsZero() {
		return true
	}
	return !now.Add(tokenRefreshLeeway).Before(m.token.ExpiresAt)
}

func (m *TokenManager) refreshLocked(ctx context.Context) error {
	refreshToken := m.token.RefreshToken
	if refreshToken == "" {
		return authorizationRequired("refresh token is not available", nil)
	}

	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", m.cfg.ClientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OAuth2TokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview := previewBody(respBytes, refreshToken, m.token.AccessToken)
		return authorizationRequired("token refresh failed", fmt.Errorf("status %d: %s", resp.StatusCode, preview))
	}

	var refreshResp struct {
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		AccessToken  string `json:"access_token"`
		Scope        string `json:"scope"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBytes, &refreshResp); err != nil {
		return fmt.Errorf("failed to parse twitter OAuth 2.0 token response: %w", err)
	}
	if refreshResp.AccessToken == "" {
		return authorizationRequired("token refresh response did not include access token", nil)
	}

	if err := m.storeTokenResponseLocked(refreshResp.AccessToken, refreshResp.RefreshToken, refreshResp.TokenType, refreshResp.Scope, refreshResp.ExpiresIn); err != nil {
		return err
	}
	slog.Info("Refreshed Twitter OAuth 2.0 access token")
	return nil
}

func (m *TokenManager) ExchangeAuthorizationCode(ctx context.Context, code, codeVerifier string) error {
	if code == "" {
		return fmt.Errorf("twitter OAuth 2.0 authorization code is empty")
	}
	if codeVerifier == "" {
		return fmt.Errorf("twitter OAuth 2.0 PKCE code verifier is empty")
	}
	if m.cfg.RedirectURL == "" {
		return fmt.Errorf("twitter OAuth 2.0 redirect URL is not configured")
	}

	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", m.cfg.RedirectURL)
	values.Set("code_verifier", codeVerifier)
	values.Set("client_id", m.cfg.ClientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OAuth2TokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		preview := previewBody(respBytes, code)
		return fmt.Errorf("twitter OAuth 2.0 authorization code exchange failed with status %d: %s", resp.StatusCode, preview)
	}

	var tokenResp struct {
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		AccessToken  string `json:"access_token"`
		Scope        string `json:"scope"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBytes, &tokenResp); err != nil {
		return fmt.Errorf("failed to parse twitter OAuth 2.0 token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("twitter OAuth 2.0 token response did not include access token")
	}
	if tokenResp.RefreshToken == "" {
		return fmt.Errorf("twitter OAuth 2.0 token response did not include refresh token")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.storeTokenResponseLocked(tokenResp.AccessToken, tokenResp.RefreshToken, tokenResp.TokenType, tokenResp.Scope, tokenResp.ExpiresIn); err != nil {
		return err
	}
	slog.Info("Stored Twitter OAuth 2.0 user token")
	return nil
}

func (m *TokenManager) storeTokenResponseLocked(accessToken, refreshToken, tokenType, scope string, expiresIn int64) error {
	m.token.AccessToken = accessToken
	if refreshToken != "" {
		m.token.RefreshToken = refreshToken
	}
	m.token.TokenType = tokenType
	if m.token.TokenType == "" {
		m.token.TokenType = "bearer"
	}
	m.token.Scope = scope
	if expiresIn > 0 {
		m.token.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	} else {
		m.token.ExpiresAt = time.Time{}
	}

	if m.cfg.TokenStorePath != "" {
		if err := saveOAuth2Token(m.cfg.TokenStorePath, m.token); err != nil {
			return err
		}
	}
	return nil
}

func loadOAuth2Token(path string) (OAuth2Token, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return OAuth2Token{}, false, nil
		}
		return OAuth2Token{}, false, err
	}
	if len(data) == 0 {
		return OAuth2Token{}, false, nil
	}

	var token OAuth2Token
	if err := json.Unmarshal(data, &token); err != nil {
		return OAuth2Token{}, false, fmt.Errorf("failed to parse twitter OAuth 2.0 token store: %w", err)
	}
	return token, true, nil
}

func saveOAuth2Token(path string, token OAuth2Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func previewBody(body []byte, secrets ...string) string {
	const limit = 512
	preview := strings.TrimSpace(string(body))
	for _, secret := range secrets {
		if secret != "" {
			preview = strings.ReplaceAll(preview, secret, "[redacted]")
		}
	}
	if len(preview) > limit {
		preview = preview[:limit] + "..."
	}
	return preview
}

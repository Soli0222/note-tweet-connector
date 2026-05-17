package twitter

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	ClientSecret   string
	AccessToken    string
	RefreshToken   string
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

func NewTokenManager(cfg OAuth2Config) (*TokenManager, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("twitter OAuth 2.0 client id is not configured")
	}
	if cfg.RefreshToken == "" {
		return nil, fmt.Errorf("twitter OAuth 2.0 refresh token is not configured")
	}

	m := &TokenManager{
		cfg:        cfg,
		httpClient: httpClient,
		token: OAuth2Token{
			AccessToken:  cfg.AccessToken,
			RefreshToken: cfg.RefreshToken,
			TokenType:    "bearer",
		},
	}

	if cfg.TokenStorePath != "" {
		token, ok, err := loadOAuth2Token(cfg.TokenStorePath)
		if err != nil {
			return nil, err
		}
		if ok {
			m.token = token
			if m.token.RefreshToken == "" {
				m.token.RefreshToken = cfg.RefreshToken
			}
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
		refreshToken = m.cfg.RefreshToken
	}
	if refreshToken == "" {
		return fmt.Errorf("twitter OAuth 2.0 refresh token is not configured")
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
	if m.cfg.ClientSecret != "" {
		credential := base64.StdEncoding.EncodeToString([]byte(m.cfg.ClientID + ":" + m.cfg.ClientSecret))
		req.Header.Set("Authorization", "Basic "+credential)
	}

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
		preview := previewBody(respBytes, refreshToken, m.cfg.RefreshToken, m.cfg.AccessToken, m.token.AccessToken)
		return fmt.Errorf("twitter OAuth 2.0 token refresh failed with status %d: %s", resp.StatusCode, preview)
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
		return fmt.Errorf("twitter OAuth 2.0 token response did not include access token")
	}

	m.token.AccessToken = refreshResp.AccessToken
	if refreshResp.RefreshToken != "" {
		m.token.RefreshToken = refreshResp.RefreshToken
	}
	m.token.TokenType = refreshResp.TokenType
	if m.token.TokenType == "" {
		m.token.TokenType = "bearer"
	}
	m.token.Scope = refreshResp.Scope
	if refreshResp.ExpiresIn > 0 {
		m.token.ExpiresAt = time.Now().Add(time.Duration(refreshResp.ExpiresIn) * time.Second)
	}

	if m.cfg.TokenStorePath != "" {
		if err := saveOAuth2Token(m.cfg.TokenStorePath, m.token); err != nil {
			return err
		}
	}
	slog.Info("Refreshed Twitter OAuth 2.0 access token")
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

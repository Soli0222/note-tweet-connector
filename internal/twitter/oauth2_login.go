package twitter

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
)

const (
	OAuth2Scope         = "tweet.read tweet.write users.read media.write offline.access"
	oauth2LoginTokenTTL = 5 * time.Minute
)

var (
	OAuth2AuthorizeEndpoint = "https://twitter.com/i/oauth2/authorize"
	ErrInvalidLoginAuth     = errors.New("invalid twitter OAuth 2.0 login auth token")
	ErrInvalidOAuth2State   = errors.New("invalid twitter OAuth 2.0 state")
)

type OAuth2LoginManager struct {
	mu           sync.Mutex
	tokenManager *TokenManager
	clientID     string
	redirectURL  string
	ttl          time.Duration
	now          func() time.Time
	authTokens   map[string]time.Time
	states       map[string]oauth2LoginState
}

type oauth2LoginState struct {
	codeVerifier string
	expiresAt    time.Time
}

func NewOAuth2LoginManager(tokenManager *TokenManager, cfg OAuth2Config) (*OAuth2LoginManager, error) {
	if tokenManager == nil {
		return nil, fmt.Errorf("twitter OAuth 2.0 token manager is nil")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("twitter OAuth 2.0 client id is not configured")
	}
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("twitter OAuth 2.0 redirect URL is not configured")
	}
	redirectURL, err := url.Parse(cfg.RedirectURL)
	if err != nil {
		return nil, fmt.Errorf("invalid twitter OAuth 2.0 redirect URL: %w", err)
	}
	if redirectURL.Scheme == "" || redirectURL.Host == "" {
		return nil, fmt.Errorf("invalid twitter OAuth 2.0 redirect URL")
	}

	return &OAuth2LoginManager{
		tokenManager: tokenManager,
		clientID:     cfg.ClientID,
		redirectURL:  cfg.RedirectURL,
		ttl:          oauth2LoginTokenTTL,
		now:          time.Now,
		authTokens:   map[string]time.Time{},
		states:       map[string]oauth2LoginState{},
	}, nil
}

func (m *OAuth2LoginManager) IssueLoginURL() (string, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	m.cleanupExpiredLocked(now)

	for token, expiresAt := range m.authTokens {
		return m.loginURLForToken(token), expiresAt, nil
	}

	token, err := randomURLToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := now.Add(m.ttl)
	m.authTokens[token] = expiresAt
	return m.loginURLForToken(token), expiresAt, nil
}

func (m *OAuth2LoginManager) BeginLogin(authToken string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	m.cleanupExpiredLocked(now)

	expiresAt, ok := m.authTokens[authToken]
	if !ok || now.After(expiresAt) {
		delete(m.authTokens, authToken)
		return "", ErrInvalidLoginAuth
	}
	delete(m.authTokens, authToken)

	codeVerifier, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	state, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	m.states[state] = oauth2LoginState{
		codeVerifier: codeVerifier,
		expiresAt:    now.Add(m.ttl),
	}

	challengeHash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	authorizeURL, err := url.Parse(OAuth2AuthorizeEndpoint)
	if err != nil {
		return "", err
	}
	q := authorizeURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", m.clientID)
	q.Set("redirect_uri", m.redirectURL)
	q.Set("scope", OAuth2Scope)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	authorizeURL.RawQuery = q.Encode()
	return authorizeURL.String(), nil
}

func (m *OAuth2LoginManager) CompleteLogin(ctx context.Context, state, code string) error {
	m.mu.Lock()
	now := m.now()
	m.cleanupExpiredLocked(now)

	loginState, ok := m.states[state]
	if !ok || now.After(loginState.expiresAt) {
		delete(m.states, state)
		m.mu.Unlock()
		return ErrInvalidOAuth2State
	}
	delete(m.states, state)
	m.mu.Unlock()

	return m.tokenManager.ExchangeAuthorizationCode(ctx, code, loginState.codeVerifier)
}

func (m *OAuth2LoginManager) CancelLogin(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, state)
}

func (m *OAuth2LoginManager) loginURLForToken(token string) string {
	redirectURL, err := url.Parse(m.redirectURL)
	if err != nil {
		return ""
	}
	redirectURL.Path = "/twitter/login"
	redirectURL.RawQuery = url.Values{"auth": {token}}.Encode()
	redirectURL.Fragment = ""
	return redirectURL.String()
}

func (m *OAuth2LoginManager) cleanupExpiredLocked(now time.Time) {
	for token, expiresAt := range m.authTokens {
		if !now.Before(expiresAt) {
			delete(m.authTokens, token)
		}
	}
	for state, loginState := range m.states {
		if !now.Before(loginState.expiresAt) {
			delete(m.states, state)
		}
	}
}

func randomURLToken(byteCount int) (string, error) {
	b := make([]byte, byteCount)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

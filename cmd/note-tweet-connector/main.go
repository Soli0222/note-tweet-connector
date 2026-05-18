package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/handler"
	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/misskey"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var version = "dev"

// Config holds the application configuration
type Config struct {
	Port             string
	MetricsPort      string
	TrackerDBPath    string
	TrackerRetention time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	IdleTimeout      time.Duration
	ShutdownTimeout  time.Duration
	LogLevel         string

	MisskeyHookSecret         string
	MisskeyHost               string
	MisskeyToken              string
	MisskeyMediaHost          string
	TwitterMediaHosts         string
	TwitterOAuth2ClientID     string
	TwitterOAuth2RedirectURL  string
	TwitterTokenStorePath     string
	TwitterBearerToken        string
	TwitterStreamReconnectMin time.Duration
	TwitterStreamReconnectMax time.Duration
	TwitterUsername           string
}

func parseFlags() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Port, "port", "8080", "Server port")
	flag.StringVar(&cfg.MetricsPort, "metrics-port", "9090", "Metrics server port")
	flag.StringVar(&cfg.TrackerDBPath, "tracker-db-path", "data/tracker.sqlite", "Path to sqlite database for the cross-post tracker")
	flag.DurationVar(&cfg.TrackerRetention, "tracker-retention", 90*24*time.Hour, "Duration to keep tracker records before pruning; non-positive keeps records indefinitely")
	flag.DurationVar(&cfg.ReadTimeout, "read-timeout", 15*time.Second, "HTTP read timeout")
	flag.DurationVar(&cfg.WriteTimeout, "write-timeout", 15*time.Second, "HTTP write timeout")
	flag.DurationVar(&cfg.IdleTimeout, "idle-timeout", 60*time.Second, "HTTP idle timeout")
	flag.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", 30*time.Second, "Graceful shutdown timeout")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&cfg.MisskeyHookSecret, "misskey-hook-secret", "", "Secret used to verify Misskey webhook requests")
	flag.StringVar(&cfg.MisskeyHost, "misskey-host", "", "Misskey instance host")
	flag.StringVar(&cfg.MisskeyToken, "misskey-token", "", "Misskey API token")
	flag.StringVar(&cfg.MisskeyMediaHost, "misskey-media-host", "", "Allowed Misskey media host for Twitter uploads")
	flag.StringVar(&cfg.TwitterMediaHosts, "twitter-media-hosts", misskey.DefaultTwitterMediaHosts, "Comma-separated allowed Twitter media hosts for Misskey uploads")
	flag.StringVar(&cfg.TwitterOAuth2ClientID, "twitter-oauth2-client-id", "", "Twitter OAuth 2.0 client ID")
	flag.StringVar(&cfg.TwitterOAuth2RedirectURL, "twitter-oauth2-redirect-url", "", "Twitter OAuth 2.0 redirect URL")
	flag.StringVar(&cfg.TwitterTokenStorePath, "twitter-token-store-path", "data/twitter_oauth2_token.json", "Path to JSON file for refreshed Twitter OAuth 2.0 tokens")
	flag.StringVar(&cfg.TwitterBearerToken, "twitter-bearer-token", "", "Twitter Application-Only Bearer Token for Filtered Stream")
	flag.DurationVar(&cfg.TwitterStreamReconnectMin, "twitter-stream-reconnect-min", 5*time.Second, "Minimum Twitter stream reconnect backoff")
	flag.DurationVar(&cfg.TwitterStreamReconnectMax, "twitter-stream-reconnect-max", 5*time.Minute, "Maximum Twitter stream reconnect backoff")
	flag.StringVar(&cfg.TwitterUsername, "twitter-username", "", "Fallback Twitter username")

	showVersion := flag.Bool("version", false, "Show version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Printf("note-tweet-connector version %s\n", version)
		os.Exit(0)
	}

	return cfg
}

func (cfg *Config) validate() error {
	var missing []string
	required := map[string]string{
		"-misskey-hook-secret": cfg.MisskeyHookSecret,
		"-misskey-host":        cfg.MisskeyHost,
		"-misskey-token":       cfg.MisskeyToken,
		"-misskey-media-host":  cfg.MisskeyMediaHost,
	}
	for name, value := range required {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}
	if cfg.TwitterOAuth2ClientID == "" {
		return fmt.Errorf("missing required flags: -twitter-oauth2-client-id")
	}
	if cfg.TwitterOAuth2RedirectURL == "" {
		return fmt.Errorf("missing required flags: -twitter-oauth2-redirect-url")
	}
	if cfg.TwitterBearerToken == "" {
		return fmt.Errorf("missing required flags: -twitter-bearer-token")
	}
	if cfg.TwitterUsername == "" {
		return fmt.Errorf("missing required flags: -twitter-username")
	}
	if cfg.TwitterStreamReconnectMin <= 0 {
		return fmt.Errorf("-twitter-stream-reconnect-min must be positive")
	}
	if cfg.TwitterStreamReconnectMax < cfg.TwitterStreamReconnectMin {
		return fmt.Errorf("-twitter-stream-reconnect-max must be greater than or equal to -twitter-stream-reconnect-min")
	}
	return nil
}

func (cfg *Config) handlerConfig(bearerTokenSource twitter.BearerTokenSource) handler.Config {
	return handler.Config{
		MisskeyHost:              cfg.MisskeyHost,
		MisskeyToken:             cfg.MisskeyToken,
		TwitterUsername:          cfg.TwitterUsername,
		TwitterMediaAllowedHosts: misskey.ParseAllowedHosts(cfg.TwitterMediaHosts),
		Twitter: twitter.Config{
			OAuth2ClientID:    cfg.TwitterOAuth2ClientID,
			OAuth2RedirectURL: cfg.TwitterOAuth2RedirectURL,
			TokenStorePath:    cfg.TwitterTokenStorePath,
			BearerTokenSource: bearerTokenSource,
			MisskeyMediaHost:  cfg.MisskeyMediaHost,
		},
	}
}

func (cfg *Config) twitterOAuth2Config() twitter.OAuth2Config {
	return twitter.OAuth2Config{
		ClientID:       cfg.TwitterOAuth2ClientID,
		RedirectURL:    cfg.TwitterOAuth2RedirectURL,
		TokenStorePath: cfg.TwitterTokenStorePath,
	}
}

func setupLogger(level string) {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))
}

type server struct {
	crossPostTracker tracker.CrossPostTracker
	metrics          *metrics.Metrics
	cfg              handler.Config
	misskeySecret    string
	twitterOAuth2    *twitter.OAuth2LoginManager
}

type authorizationLoggingTokenSource struct {
	source twitter.ForceRefreshBearerTokenSource
	login  *twitter.OAuth2LoginManager
}

func (s *authorizationLoggingTokenSource) BearerToken(ctx context.Context) (string, error) {
	token, err := s.source.BearerToken(ctx)
	if errors.Is(err, twitter.ErrAuthorizationRequired) {
		logTwitterOAuth2AuthorizationRequired(s.login)
	}
	return token, err
}

func (s *authorizationLoggingTokenSource) Refresh(ctx context.Context) error {
	err := s.source.Refresh(ctx)
	if errors.Is(err, twitter.ErrAuthorizationRequired) {
		logTwitterOAuth2AuthorizationRequired(s.login)
	}
	return err
}

func (s *server) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	userAgent := r.Header.Get("User-Agent")
	if strings.Contains(userAgent, "Misskey-Hooks") {
		start := time.Now()
		secret := r.Header.Get("X-Misskey-Hook-Secret")
		expectedSecret := s.misskeySecret
		if expectedSecret == "" || secret != expectedSecret {
			http.Error(w, "Invalid Misskey secret", http.StatusUnauthorized)
			slog.Error("Invalid Misskey secret")
			s.metrics.WebhookRequestsTotal.WithLabelValues("misskey", "unauthorized").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("misskey", "unauthorized").Inc()
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			slog.Error("Failed to read request body", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("misskey", "error").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("misskey", "read_body").Inc()
			return
		}

		err = handler.Note2TweetHandlerWithConfig(r.Context(), s.cfg, body, s.crossPostTracker, s.metrics)
		if err != nil {
			http.Error(w, "Failed to handle request", http.StatusInternalServerError)
			slog.Error("Failed to handle request", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("misskey", "error").Inc()
			return
		}

		s.metrics.WebhookRequestsTotal.WithLabelValues("misskey", "success").Inc()
		s.metrics.WebhookRequestDuration.WithLabelValues("misskey").Observe(time.Since(start).Seconds())

	} else {
		http.Error(w, "Unsupported User-Agent", http.StatusBadRequest)
		slog.Error("Unsupported User-Agent", slog.Any("User-Agent", userAgent))
		s.metrics.WebhookRequestsTotal.WithLabelValues("unknown", "bad_request").Inc()
		s.metrics.WebhookRequestErrors.WithLabelValues("unknown", "unsupported_user_agent").Inc()
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *server) twitterLoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	if s.twitterOAuth2 == nil {
		http.Error(w, "Twitter OAuth 2.0 login is not configured", http.StatusInternalServerError)
		return
	}

	authorizeURL, err := s.twitterOAuth2.BeginLogin(r.URL.Query().Get("auth"))
	if err != nil {
		if errors.Is(err, twitter.ErrInvalidLoginAuth) {
			http.Error(w, "Invalid or expired login auth token", http.StatusForbidden)
			return
		}
		http.Error(w, "Failed to start Twitter OAuth 2.0 login", http.StatusInternalServerError)
		slog.Error("Failed to start Twitter OAuth 2.0 login", slog.Any("error", err))
		return
	}

	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (s *server) twitterCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	if s.twitterOAuth2 == nil {
		http.Error(w, "Twitter OAuth 2.0 login is not configured", http.StatusInternalServerError)
		return
	}

	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		s.twitterOAuth2.CancelLogin(r.URL.Query().Get("state"))
		http.Error(w, "Twitter OAuth 2.0 authorization failed: "+oauthErr, http.StatusBadRequest)
		slog.Warn("Twitter OAuth 2.0 authorization failed",
			slog.String("error", oauthErr),
			slog.String("error_description", r.URL.Query().Get("error_description")))
		logTwitterOAuth2AuthorizationRequired(s.twitterOAuth2)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	if err := s.twitterOAuth2.CompleteLogin(r.Context(), state, code); err != nil {
		if errors.Is(err, twitter.ErrInvalidOAuth2State) {
			http.Error(w, "Invalid or expired state; restart Twitter OAuth 2.0 login", http.StatusBadRequest)
			logTwitterOAuth2AuthorizationRequired(s.twitterOAuth2)
			return
		}
		http.Error(w, "Failed to complete Twitter OAuth 2.0 login", http.StatusBadGateway)
		slog.Error("Failed to complete Twitter OAuth 2.0 login", slog.Any("error", err))
		logTwitterOAuth2AuthorizationRequired(s.twitterOAuth2)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte("Twitter OAuth 2.0 authorization completed. You can close this page.\n")); err != nil {
		slog.Error("Failed to write OAuth 2.0 callback response", slog.Any("error", err))
	}
}

func logTwitterOAuth2AuthorizationRequired(login *twitter.OAuth2LoginManager) {
	if login == nil {
		return
	}
	loginURL, expiresAt, err := login.IssueLoginURL()
	if err != nil {
		slog.Error("Failed to issue Twitter OAuth 2.0 login URL", slog.Any("error", err))
		return
	}
	slog.Warn("Twitter OAuth 2.0 authorization required",
		slog.String("login_url", loginURL),
		slog.Time("expires_at", expiresAt))
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok\n")); err != nil {
		slog.Error("Failed to write response", slog.Any("error", err))
	}
}

func printBanner() {
	banner := `
               _                  _                          _                                                   _
              | |                | |                        | |                                                 | |
 _ __    ___  | |_   ___  ______ | |_ __      __  ___   ___ | |_  ______   ___   ___   _ __   _ __    ___   ___ | |_   ___   _ __
| '_ \  / _ \ | __| / _ \|______|| __|\ \ /\ / / / _ \ / _ \| __||______| / __| / _ \ | '_ \ | '_ \  / _ \ / __|| __| / _ \ | '__|
| | | || (_) || |_ |  __/        | |_  \ V  V / |  __/|  __/| |_         | (__ | (_) || | | || | | ||  __/| (__ | |_ | (_) || |
|_| |_| \___/  \__| \___|         \__|  \_/\_/   \___| \___| \__|         \___| \___/ |_| |_||_| |_| \___| \___| \__| \___/ |_|

                                                                                                    
Version: ` + version

	fmt.Println(banner)
}

func periodicTrackerEntriesMetric(ctx context.Context, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateTrackerEntriesMetric(ctx, crossPostTracker, m)
		}
	}
}

func updateTrackerEntriesMetric(ctx context.Context, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics) {
	count, err := crossPostTracker.Count(ctx)
	if err != nil {
		slog.Error("Failed to count cross-post tracker records", slog.Any("error", err))
		return
	}
	m.TrackerEntriesTotal.Set(float64(count))
}

func runTwitterStream(ctx context.Context, streamClient *twitter.StreamClient, cfg handler.Config, crossPostTracker tracker.CrossPostTracker, m *metrics.Metrics, reconnectMin, reconnectMax time.Duration) {
	backoff := reconnectMin
	for {
		if ctx.Err() != nil {
			return
		}

		m.TwitterStreamConnects.WithLabelValues("attempt").Inc()
		err := streamClient.Consume(ctx, func(ctx context.Context, line []byte) error {
			m.TwitterStreamLastMessageTime.Set(float64(time.Now().Unix()))
			if err := handler.Tweet2NoteHandlerWithConfig(ctx, cfg, line, crossPostTracker, m); err != nil {
				m.TwitterStreamMessages.WithLabelValues("error").Inc()
				slog.Error("Failed to process Twitter stream message", slog.Any("error", err))
				return nil
			}
			m.TwitterStreamMessages.WithLabelValues("success").Inc()
			return nil
		})
		if err == nil || errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil) {
			return
		}

		reason := twitterStreamDisconnectReason(err)
		m.TwitterStreamDisconnects.WithLabelValues(reason).Inc()
		slog.Warn("Twitter stream disconnected",
			slog.String("reason", reason),
			slog.Duration("reconnect_after", backoff),
			slog.Any("error", err))

		sleep := twitterStreamReconnectDelay(err, backoff)
		if sleep > reconnectMax {
			sleep = reconnectMax
		}
		if !sleepContext(ctx, sleep) {
			return
		}
		if backoff < reconnectMax {
			backoff *= 2
			if backoff > reconnectMax {
				backoff = reconnectMax
			}
		}
	}
}

func twitterStreamDisconnectReason(err error) string {
	if errors.Is(err, twitter.ErrStreamKeepAliveTimeout) {
		return "keep_alive_timeout"
	}
	var rateLimitErr *twitter.StreamRateLimitError
	if errors.As(err, &rateLimitErr) {
		return "rate_limit"
	}
	var httpErr *twitter.StreamHTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("http_%d", httpErr.StatusCode)
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	return "error"
}

func twitterStreamReconnectDelay(err error, fallback time.Duration) time.Duration {
	var rateLimitErr *twitter.StreamRateLimitError
	if errors.As(err, &rateLimitErr) && !rateLimitErr.ResetAt.IsZero() {
		delay := time.Until(rateLimitErr.ResetAt)
		if delay > 0 {
			return delay
		}
	}
	return fallback
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func main() {
	cfg := parseFlags()

	setupLogger(cfg.LogLevel)
	if err := cfg.validate(); err != nil {
		slog.Error("Invalid configuration", slog.Any("error", err))
		os.Exit(1)
	}

	printBanner()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize metrics
	m := metrics.New(version)

	crossPostTracker, err := tracker.NewSQLiteCrossPostTracker(ctx, cfg.TrackerDBPath, cfg.TrackerRetention)
	if err != nil {
		slog.Error("Failed to initialize cross-post tracker", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		if err := crossPostTracker.Close(); err != nil {
			slog.Error("Failed to close cross-post tracker", slog.Any("error", err))
		}
	}()
	updateTrackerEntriesMetric(ctx, crossPostTracker, m)
	go periodicTrackerEntriesMetric(ctx, crossPostTracker, m)

	oauth2Cfg := cfg.twitterOAuth2Config()
	tokenManager, err := twitter.NewTokenManager(oauth2Cfg)
	if err != nil {
		slog.Error("Failed to initialize Twitter OAuth 2.0 token source", slog.Any("error", err))
		os.Exit(1)
	}
	oauth2Login, err := twitter.NewOAuth2LoginManager(tokenManager, oauth2Cfg)
	if err != nil {
		slog.Error("Failed to initialize Twitter OAuth 2.0 login manager", slog.Any("error", err))
		os.Exit(1)
	}
	if tokenManager.AuthorizationRequired() {
		logTwitterOAuth2AuthorizationRequired(oauth2Login)
	}
	handlerCfg := cfg.handlerConfig(&authorizationLoggingTokenSource{
		source: tokenManager,
		login:  oauth2Login,
	})
	streamClient := twitter.NewStreamClient(twitter.StaticBearerTokenSource{Token: cfg.TwitterBearerToken})
	streamClient.OnConnect = func() {
		m.TwitterStreamConnects.WithLabelValues("success").Inc()
		slog.Info("Connected to Twitter Filtered Stream")
	}
	streamRule := twitter.DefaultStreamRule(cfg.TwitterUsername)
	streamRuleTag := twitter.DefaultStreamRuleTag()
	m.TwitterStreamRuleUpdates.WithLabelValues("ensure", "attempt").Inc()
	if err := streamClient.EnsureRule(ctx, streamRule, streamRuleTag); err != nil {
		m.TwitterStreamRuleUpdates.WithLabelValues("ensure", "error").Inc()
		slog.Error("Failed to ensure Twitter stream rule", slog.Any("error", err))
		os.Exit(1)
	}
	m.TwitterStreamRuleUpdates.WithLabelValues("ensure", "success").Inc()
	slog.Info("Ensured Twitter stream rule",
		slog.String("rule", streamRule),
		slog.String("tag", streamRuleTag))

	s := &server{
		crossPostTracker: crossPostTracker,
		metrics:          m,
		cfg:              handlerCfg,
		misskeySecret:    cfg.MisskeyHookSecret,
		twitterOAuth2:    oauth2Login,
	}

	// Main server
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.webhookHandler)
	mux.HandleFunc("/twitter/login", s.twitterLoginHandler)
	mux.HandleFunc("/twitter/callback", s.twitterCallbackHandler)
	mux.HandleFunc("/healthz", healthzHandler)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Metrics server
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())

	metricsSrv := &http.Server{
		Addr:         ":" + cfg.MetricsPort,
		Handler:      metricsMux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Start metrics server
	go func() {
		slog.Info("Starting metrics server...", slog.String("port", cfg.MetricsPort))
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Metrics server error", slog.Any("error", err))
		}
	}()

	// Start Twitter stream worker
	go func() {
		slog.Info("Starting Twitter Filtered Stream worker")
		runTwitterStream(ctx, streamClient, handlerCfg, crossPostTracker, m, cfg.TwitterStreamReconnectMin, cfg.TwitterStreamReconnectMax)
	}()

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		slog.Info("Shutting down servers...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", slog.Any("error", err))
		}
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("Metrics server shutdown error", slog.Any("error", err))
		}
	}()

	slog.Info("Starting server...",
		slog.String("version", version),
		slog.String("port", cfg.Port),
		slog.String("metrics_port", cfg.MetricsPort),
		slog.String("tracker_db_path", cfg.TrackerDBPath),
		slog.Duration("tracker_retention", cfg.TrackerRetention),
		slog.String("log_level", cfg.LogLevel))

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("ListenAndServe", slog.Any("error", err))
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

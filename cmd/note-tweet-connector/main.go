package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
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

const version = "2.0.2"

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

	MisskeyHookSecret            string
	MisskeyHost                  string
	MisskeyToken                 string
	MisskeyMediaHost             string
	TwitterMediaHosts            string
	TwitterAPIKey                string
	TwitterAPIKeySecret          string
	TwitterAccessToken           string
	TwitterAccessTokenSecret     string
	TwitterUserAccessToken       string
	TwitterWebhookConsumerSecret string
	TwitterUsername              string
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
	flag.StringVar(&cfg.TwitterAPIKey, "twitter-api-key", "", "Twitter API key")
	flag.StringVar(&cfg.TwitterAPIKeySecret, "twitter-api-key-secret", "", "Twitter API key secret")
	flag.StringVar(&cfg.TwitterAccessToken, "twitter-access-token", "", "Twitter access token")
	flag.StringVar(&cfg.TwitterAccessTokenSecret, "twitter-access-token-secret", "", "Twitter access token secret")
	flag.StringVar(&cfg.TwitterUserAccessToken, "twitter-user-access-token", "", "Twitter OAuth 2.0 user access token")
	flag.StringVar(&cfg.TwitterWebhookConsumerSecret, "twitter-webhook-consumer-secret", "", "Twitter webhook consumer secret; defaults to twitter-api-key-secret")
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
		"-misskey-hook-secret":         cfg.MisskeyHookSecret,
		"-misskey-host":                cfg.MisskeyHost,
		"-misskey-token":               cfg.MisskeyToken,
		"-misskey-media-host":          cfg.MisskeyMediaHost,
		"-twitter-api-key":             cfg.TwitterAPIKey,
		"-twitter-api-key-secret":      cfg.TwitterAPIKeySecret,
		"-twitter-access-token":        cfg.TwitterAccessToken,
		"-twitter-access-token-secret": cfg.TwitterAccessTokenSecret,
		"-twitter-user-access-token":   cfg.TwitterUserAccessToken,
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
	if cfg.TwitterWebhookConsumerSecret == "" {
		cfg.TwitterWebhookConsumerSecret = cfg.TwitterAPIKeySecret
	}
	return nil
}

func (cfg *Config) handlerConfig() handler.Config {
	return handler.Config{
		MisskeyHost:              cfg.MisskeyHost,
		MisskeyToken:             cfg.MisskeyToken,
		TwitterUsername:          cfg.TwitterUsername,
		TwitterMediaAllowedHosts: misskey.ParseAllowedHosts(cfg.TwitterMediaHosts),
		Twitter: twitter.Config{
			APIKey:            cfg.TwitterAPIKey,
			APIKeySecret:      cfg.TwitterAPIKeySecret,
			AccessToken:       cfg.TwitterAccessToken,
			AccessTokenSecret: cfg.TwitterAccessTokenSecret,
			UserAccessToken:   cfg.TwitterUserAccessToken,
			MisskeyMediaHost:  cfg.MisskeyMediaHost,
		},
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
	twitterSecret    string
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

func (s *server) twitterWebhookHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	switch r.Method {
	case http.MethodGet:
		crcToken := r.URL.Query().Get("crc_token")
		if crcToken == "" {
			http.Error(w, "Missing crc_token", http.StatusBadRequest)
			s.metrics.WebhookRequestsTotal.WithLabelValues("twitter_crc", "bad_request").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("twitter_crc", "missing_crc_token").Inc()
			return
		}

		responseToken, err := twitterResponseToken(crcToken, s.twitterSecret)
		if err != nil {
			http.Error(w, "Twitter webhook secret is not configured", http.StatusInternalServerError)
			slog.Error("Twitter webhook secret is not configured", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("twitter_crc", "error").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("twitter_crc", "missing_secret").Inc()
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"response_token": responseToken}); err != nil {
			slog.Error("Failed to write CRC response", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("twitter_crc", "error").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("twitter_crc", "write_response").Inc()
			return
		}

		s.metrics.WebhookRequestsTotal.WithLabelValues("twitter_crc", "success").Inc()
		s.metrics.WebhookRequestDuration.WithLabelValues("twitter_crc").Observe(time.Since(start).Seconds())
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			slog.Error("Failed to read request body", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("twitter", "error").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("twitter", "read_body").Inc()
			return
		}

		signature := r.Header.Get("x-twitter-webhooks-signature")
		if ok, err := verifyTwitterSignature(body, signature, s.twitterSecret); err != nil || !ok {
			http.Error(w, "Invalid Twitter signature", http.StatusUnauthorized)
			slog.Error("Invalid Twitter signature", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("twitter", "unauthorized").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("twitter", "signature").Inc()
			return
		}

		if err := handler.Tweet2NoteHandlerWithConfig(r.Context(), s.cfg, body, s.crossPostTracker, s.metrics); err != nil {
			http.Error(w, "Failed to handle request", http.StatusInternalServerError)
			slog.Error("Failed to handle request", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("twitter", "error").Inc()
			return
		}

		s.metrics.WebhookRequestsTotal.WithLabelValues("twitter", "success").Inc()
		s.metrics.WebhookRequestDuration.WithLabelValues("twitter").Observe(time.Since(start).Seconds())
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
	}
}

func twitterResponseToken(crcToken, secret string) (string, error) {
	signature, err := twitterHMAC([]byte(crcToken), secret)
	if err != nil {
		return "", err
	}
	return "sha256=" + signature, nil
}

func verifyTwitterSignature(body []byte, signature, secret string) (bool, error) {
	expected, err := twitterHMAC(body, secret)
	if err != nil {
		return false, err
	}
	want := "sha256=" + expected
	return subtle.ConstantTimeCompare([]byte(signature), []byte(want)) == 1, nil
}

func twitterHMAC(message []byte, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("twitter webhook consumer secret must be set")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(message)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("Webhook Test Server is healthy\nVersion: " + version)); err != nil {
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

	s := &server{
		crossPostTracker: crossPostTracker,
		metrics:          m,
		cfg:              cfg.handlerConfig(),
		misskeySecret:    cfg.MisskeyHookSecret,
		twitterSecret:    cfg.TwitterWebhookConsumerSecret,
	}

	// Main server
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.webhookHandler)
	mux.HandleFunc("/twitter/webhook", s.twitterWebhookHandler)
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

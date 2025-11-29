package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/handler"
	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const version = "2.0.1"

// Config holds the application configuration
type Config struct {
	Port            string
	MetricsPort     string
	TrackerExpiry   time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
}

func parseFlags() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Port, "port", "8080", "Server port")
	flag.StringVar(&cfg.MetricsPort, "metrics-port", "9090", "Metrics server port")
	flag.DurationVar(&cfg.TrackerExpiry, "tracker-expiry", 5*time.Hour, "Duration to keep processed content in tracker")
	flag.DurationVar(&cfg.ReadTimeout, "read-timeout", 15*time.Second, "HTTP read timeout")
	flag.DurationVar(&cfg.WriteTimeout, "write-timeout", 15*time.Second, "HTTP write timeout")
	flag.DurationVar(&cfg.IdleTimeout, "idle-timeout", 60*time.Second, "HTTP idle timeout")
	flag.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", 30*time.Second, "Graceful shutdown timeout")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "Log level (debug, info, warn, error)")

	showVersion := flag.Bool("version", false, "Show version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Printf("note-tweet-connector version %s\n", version)
		os.Exit(0)
	}

	// Environment variable override for port (for backward compatibility)
	if envPort := os.Getenv("PORT"); envPort != "" {
		cfg.Port = envPort
	}

	return cfg
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
	contentTracker *tracker.ContentTracker
	metrics        *metrics.Metrics
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
		expectedSecret := os.Getenv("MISSKEY_HOOK_SECRET")
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

		err = handler.Note2TweetHandler(r.Context(), body, s.contentTracker, s.metrics)
		if err != nil {
			http.Error(w, "Failed to handle request", http.StatusInternalServerError)
			slog.Error("Failed to handle request", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("misskey", "error").Inc()
			return
		}

		s.metrics.WebhookRequestsTotal.WithLabelValues("misskey", "success").Inc()
		s.metrics.WebhookRequestDuration.WithLabelValues("misskey").Observe(time.Since(start).Seconds())

	} else if strings.Contains(userAgent, "IFTTT-Hooks") {
		start := time.Now()
		secret := r.Header.Get("X-IFTTT-Hook-Secret")
		expectedSecret := os.Getenv("IFTTT_HOOK_SECRET")
		if expectedSecret == "" || secret != expectedSecret {
			http.Error(w, "Invalid IFTTT secret", http.StatusUnauthorized)
			slog.Error("Invalid IFTTT secret")
			s.metrics.WebhookRequestsTotal.WithLabelValues("ifttt", "unauthorized").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("ifttt", "unauthorized").Inc()
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			slog.Error("Failed to read request body", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("ifttt", "error").Inc()
			s.metrics.WebhookRequestErrors.WithLabelValues("ifttt", "read_body").Inc()
			return
		}

		err = handler.Tweet2NoteHandler(r.Context(), body, s.contentTracker, s.metrics)
		if err != nil {
			http.Error(w, "Failed to handle request", http.StatusInternalServerError)
			slog.Error("Failed to handle request", slog.Any("error", err))
			s.metrics.WebhookRequestsTotal.WithLabelValues("ifttt", "error").Inc()
			return
		}

		s.metrics.WebhookRequestsTotal.WithLabelValues("ifttt", "success").Inc()
		s.metrics.WebhookRequestDuration.WithLabelValues("ifttt").Observe(time.Since(start).Seconds())

	} else {
		http.Error(w, "Unsupported User-Agent", http.StatusBadRequest)
		slog.Error("Unsupported User-Agent", slog.Any("User-Agent", userAgent))
		s.metrics.WebhookRequestsTotal.WithLabelValues("unknown", "bad_request").Inc()
		s.metrics.WebhookRequestErrors.WithLabelValues("unknown", "unsupported_user_agent").Inc()
		return
	}

	w.WriteHeader(http.StatusOK)
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

func main() {
	cfg := parseFlags()

	if err := godotenv.Load(); err != nil {
		slog.Warn(".env file not found, using environment variables")
	}

	setupLogger(cfg.LogLevel)

	printBanner()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize metrics
	m := metrics.New(version)

	contentTracker := tracker.NewContentTracker(ctx, cfg.TrackerExpiry)

	s := &server{
		contentTracker: contentTracker,
		metrics:        m,
	}

	// Main server
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.webhookHandler)
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
		slog.String("log_level", cfg.LogLevel))

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("ListenAndServe", slog.Any("error", err))
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

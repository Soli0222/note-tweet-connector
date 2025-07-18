package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Soli0222/note-tweet-connector/handler"
	"github.com/joho/godotenv"
)

var (
	version = "1.7.1"
	contentTracker = handler.NewContentTracker(5 * time.Hour)
)

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	userAgent := r.Header.Get("User-Agent")
	if strings.Contains(userAgent, "Misskey-Hooks") {
		secret := r.Header.Get("X-Misskey-Hook-Secret")
		if !strings.Contains(secret, os.Getenv("MISSKEY_HOOK_SECRET")) {
			http.Error(w, "Invalid Misskey secret", http.StatusUnauthorized)
			slog.Error("Invalid Misskey secret")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			slog.Error("Failed to read request body", slog.Any("error", err))
			return
		}

		err = handler.Note2TweetHandler(body, contentTracker)
		if err != nil {
			http.Error(w, "Failed to handle request", http.StatusInternalServerError)
			slog.Error("Failed to handle request", slog.Any("error", err))
			return
		}

	} else if strings.Contains(userAgent, "IFTTT-Hooks") {
		secret := r.Header.Get("X-IFTTT-Hook-Secret")
		if !strings.Contains(secret, os.Getenv("IFTTT_HOOK_SECRET")) {
			http.Error(w, "Invalid IFTTT secret", http.StatusUnauthorized)
			slog.Error("Invalid IFTTT secret")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			slog.Error("Failed to read request body", slog.Any("error", err))
			return
		}

		err = handler.Tweet2NoteHandler(body, contentTracker)
		if err != nil {
			http.Error(w, "Failed to handle request", http.StatusInternalServerError)
			slog.Error("Failed to handle request", slog.Any("error", err))
			return
		}

	} else {
		http.Error(w, "Unsupported User-Agent", http.StatusBadRequest)
		slog.Error("Unsupported User-Agent", slog.Any("User-Agent", userAgent))
		return
	}

	w.WriteHeader(http.StatusOK)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Webhook Test Server is healthy\nVersion: " + version))
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
	err := godotenv.Load()
	if err != nil {
		slog.Warn(".env file not found, using environment variables")
	}

	printBanner()

	http.HandleFunc("/", webhookHandler)
	http.HandleFunc("/healthz", healthzHandler)

	slog.Info("Starting server...", slog.Any("version", version))
	slog.Info("Server is listening on port 8080...")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		slog.Error("ListenAndServe", slog.Any("error", err))
		os.Exit(1)
	}
}

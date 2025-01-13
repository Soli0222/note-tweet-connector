package main

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/Soli0222/note-tweet-connector/handler"
	"github.com/joho/godotenv"
)

var version = "1.3.0"

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

		err = handler.Note2TweetHandler(body)
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

		err = handler.Tweet2NoteHandler(body)
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

func main() {
	err := godotenv.Load()
	if err != nil {
		slog.Warn(".env file not found, using environment variables")
	}

	http.HandleFunc("/", webhookHandler)
	http.HandleFunc("/healthz", healthzHandler)

	slog.Info("Starting server...", slog.Any("version", version))
	slog.Info("Server is listening on port 8080...")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		slog.Error("ListenAndServe", slog.Any("error", err))
		os.Exit(1)
	}
}

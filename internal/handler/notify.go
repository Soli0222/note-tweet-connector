package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/Soli0222/note-tweet-connector/internal/misskey"
	"github.com/Soli0222/note-tweet-connector/internal/notify"
	"github.com/Soli0222/note-tweet-connector/internal/twitter"
)

func notifyTwitterFailure(ctx context.Context, cfg Config, noteID string, err error, mediaCount int, quoteTweetID string) {
	notifier := cfg.Notifier
	if notifier == nil {
		return
	}

	var apiErr *twitter.APIError
	if errors.As(err, &apiErr) && (apiErr.Operation == "media upload" || apiErr.Operation == "media download") {
		fields := []notify.Field{
			{Name: "note_id", Value: noteID},
			{Name: "operation", Value: apiErr.Operation},
			{Name: "media_count", Value: strconv.Itoa(mediaCount)},
		}
		if apiErr.Command != "" {
			fields = append(fields, notify.Field{Name: "command", Value: apiErr.Command})
		}
		if apiErr.StatusCode > 0 {
			fields = append(fields, notify.Field{Name: "status", Value: strconv.Itoa(apiErr.StatusCode)})
		}
		if apiErr.BodyPreview != "" {
			fields = append(fields, notify.Field{Name: "response", Value: apiErr.BodyPreview})
		}
		notifyHandlerEvent(ctx, notifier, notify.Event{
			Kind:      notify.EventTwitterMediaUploadFailed,
			Severity:  notify.SeverityError,
			Title:     "Twitter media upload に失敗しました",
			Message:   "Misskey ノートのメディアを Twitter へアップロードできませんでした。",
			Fields:    fields,
			DedupeKey: fmt.Sprintf("twitter_media_upload_failed:%s:%s:%d:%s", apiErr.Operation, apiErr.Command, apiErr.StatusCode, noteID),
		})
		return
	}

	fields := []notify.Field{
		{Name: "note_id", Value: noteID},
		{Name: "media_count", Value: strconv.Itoa(mediaCount)},
	}
	if quoteTweetID != "" {
		fields = append(fields, notify.Field{Name: "quote_tweet_id", Value: quoteTweetID})
	}
	status := 0
	if errors.As(err, &apiErr) {
		status = apiErr.StatusCode
		if apiErr.StatusCode > 0 {
			fields = append(fields, notify.Field{Name: "status", Value: strconv.Itoa(apiErr.StatusCode)})
		}
		if apiErr.BodyPreview != "" {
			fields = append(fields, notify.Field{Name: "response", Value: apiErr.BodyPreview})
		}
	} else {
		fields = append(fields, notify.Field{Name: "error", Value: err.Error()})
	}
	notifyHandlerEvent(ctx, notifier, notify.Event{
		Kind:      notify.EventTwitterPostFailed,
		Severity:  notify.SeverityError,
		Title:     "Twitter POST に失敗しました",
		Message:   "Misskey ノートを Tweet として投稿できませんでした。",
		Fields:    fields,
		DedupeKey: fmt.Sprintf("twitter_post_failed:%d:%s", status, noteID),
	})
}

func notifyMisskeyFailure(ctx context.Context, cfg Config, operation, tweetID string, err error, mediaIndex int) {
	notifier := cfg.Notifier
	if notifier == nil {
		return
	}

	fields := []notify.Field{
		{Name: "operation", Value: operation},
		{Name: "tweet_id", Value: tweetID},
	}
	if mediaIndex >= 0 {
		fields = append(fields, notify.Field{Name: "media_index", Value: strconv.Itoa(mediaIndex)})
	}

	status := 0
	var apiErr *misskey.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Operation != "" {
			fields = append(fields, notify.Field{Name: "api_operation", Value: apiErr.Operation})
		}
		status = apiErr.StatusCode
		if apiErr.StatusCode > 0 {
			fields = append(fields, notify.Field{Name: "status", Value: strconv.Itoa(apiErr.StatusCode)})
		}
		if apiErr.BodyPreview != "" {
			fields = append(fields, notify.Field{Name: "response", Value: apiErr.BodyPreview})
		}
	} else {
		fields = append(fields, notify.Field{Name: "error", Value: err.Error()})
	}

	notifyHandlerEvent(ctx, notifier, notify.Event{
		Kind:      notify.EventMisskeyAPIFailed,
		Severity:  notify.SeverityError,
		Title:     "Misskey API に失敗しました",
		Message:   "Twitter の tweet を Misskey に転送できませんでした。",
		Fields:    fields,
		DedupeKey: fmt.Sprintf("misskey_api_failed:%s:%d:%s", operation, status, tweetID),
	})
}

func notifyHandlerEvent(ctx context.Context, notifier notify.Notifier, event notify.Event) {
	if err := notifier.Notify(ctx, event); err != nil {
		slog.Warn("Failed to send Discord notification", slog.Any("error", err), slog.String("kind", string(event.Kind)))
	}
}

package twitter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestTweetBodyIncludesQuoteTweetID(t *testing.T) {
	got := tweetBody("hello", []string{"media-1"}, "tweet-quote")
	want := map[string]interface{}{
		"text": "hello",
		"media": map[string]interface{}{
			"media_ids": []string{"media-1"},
		},
		"quote_tweet_id": "tweet-quote",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tweetBody() = %#v, want %#v", got, want)
	}
}

func TestMediaCategoryForType(t *testing.T) {
	tests := []struct {
		mediaType string
		want      string
		wantErr   bool
	}{
		{mediaType: "image/jpeg", want: "tweet_image"},
		{mediaType: "image/png", want: "tweet_image"},
		{mediaType: "image/gif", want: "tweet_gif"},
		{mediaType: "video/mp4", want: "tweet_video"},
		{mediaType: "application/octet-stream", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.mediaType, func(t *testing.T) {
			got, err := mediaCategoryForType(tt.mediaType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("mediaCategoryForType() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("mediaCategoryForType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMediaTypeFromURL(t *testing.T) {
	tests := []struct {
		fileURL string
		want    string
	}{
		{fileURL: "https://media.example/image.jpg", want: "image/jpeg"},
		{fileURL: "https://media.example/image.png?foo=bar", want: "image/png"},
		{fileURL: "https://media.example/image.gif", want: "image/gif"},
		{fileURL: "https://media.example/video.mp4", want: "video/mp4"},
		{fileURL: "https://media.example/file.bin", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.fileURL, func(t *testing.T) {
			got := mediaTypeFromURL(tt.fileURL)
			if got != tt.want {
				t.Fatalf("mediaTypeFromURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateMediaURL(t *testing.T) {
	if err := validateMediaURL("https://media.example/image.png", "media.example"); err != nil {
		t.Fatalf("validateMediaURL() error = %v", err)
	}
	if err := validateMediaURL("https://other.example/image.png", "media.example"); err == nil {
		t.Fatal("validateMediaURL() expected host error")
	}
}

func TestUploadMediaFromURLUsesSimpleUploadForImage(t *testing.T) {
	ctx := context.Background()
	var uploadCalled bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-data"))
		case "/2/media/upload":
			uploadCalled = true
			if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
				t.Fatalf("Authorization = %q, want Bearer token-1", got)
			}
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Fatalf("ParseMultipartForm() error = %v", err)
			}
			if command := r.FormValue("command"); command != "" {
				t.Fatalf("command = %q, want empty simple upload", command)
			}
			if mediaType := r.FormValue("media_type"); mediaType != "image/png" {
				t.Fatalf("media_type = %q, want image/png", mediaType)
			}
			if mediaCategory := r.FormValue("media_category"); mediaCategory != "tweet_image" {
				t.Fatalf("media_category = %q, want tweet_image", mediaCategory)
			}
			if _, _, err := r.FormFile("media"); err != nil {
				t.Fatalf("FormFile(media) error = %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"media-1"}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	oldEndpoint := UploadMediaEndpoint
	UploadMediaEndpoint = server.URL + "/2/media/upload"
	defer func() { UploadMediaEndpoint = oldEndpoint }()

	mediaURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	mediaID, err := uploadMediaFromURL(ctx, Config{
		BearerTokenSource: StaticBearerTokenSource{Token: "token-1"},
		MisskeyMediaHost:  mediaURL.Host,
	}, server.URL+"/image.png")
	if err != nil {
		t.Fatalf("uploadMediaFromURL() error = %v", err)
	}
	if mediaID != "media-1" {
		t.Fatalf("mediaID = %q, want media-1", mediaID)
	}
	if !uploadCalled {
		t.Fatal("upload endpoint was not called")
	}
}

func TestMediaUploadForbiddenErrorIncludesUploadContext(t *testing.T) {
	err := mediaUploadRequestError("INIT", http.StatusForbidden, []byte(`{"title":"Forbidden"}`))
	if err == nil {
		t.Fatal("mediaUploadRequestError() returned nil")
	}
	for _, want := range []string{"media upload INIT failed with status 403", "tweet.write", "Media API access"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("mediaUploadRequestError() = %q, want substring %q", err.Error(), want)
		}
	}
}

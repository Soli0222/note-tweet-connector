package twitter

import (
	"reflect"
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

func TestLoadTwitterUserAccessToken(t *testing.T) {
	t.Setenv("TWITTER_USER_ACCESS_TOKEN", "token")

	got, err := loadTwitterUserAccessToken()
	if err != nil {
		t.Fatalf("loadTwitterUserAccessToken() error = %v", err)
	}
	if got != "token" {
		t.Fatalf("loadTwitterUserAccessToken() = %q, want token", got)
	}
}

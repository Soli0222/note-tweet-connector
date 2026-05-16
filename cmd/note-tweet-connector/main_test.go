package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestTwitterResponseToken(t *testing.T) {
	t.Setenv("TWITTER_WEBHOOK_CONSUMER_SECRET", "secret")

	got, err := twitterResponseToken("challenge")
	if err != nil {
		t.Fatalf("twitterResponseToken() error = %v", err)
	}

	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte("challenge"))
	want := "sha256=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("twitterResponseToken() = %q, want %q", got, want)
	}
}

func TestVerifyTwitterSignature(t *testing.T) {
	t.Setenv("TWITTER_WEBHOOK_CONSUMER_SECRET", "secret")

	body := []byte(`{"tweet_create_events":[]}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	signature := "sha256=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	ok, err := verifyTwitterSignature(body, signature)
	if err != nil {
		t.Fatalf("verifyTwitterSignature() error = %v", err)
	}
	if !ok {
		t.Fatal("verifyTwitterSignature() = false, want true")
	}

	ok, err = verifyTwitterSignature(body, "sha256=invalid")
	if err != nil {
		t.Fatalf("verifyTwitterSignature() error = %v", err)
	}
	if ok {
		t.Fatal("verifyTwitterSignature() = true, want false")
	}
}

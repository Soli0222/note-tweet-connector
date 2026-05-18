package twitter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultStreamRule(t *testing.T) {
	if got := DefaultStreamRule("dummy_user"); got != "from:dummy_user -is:retweet -is:reply" {
		t.Fatalf("DefaultStreamRule() = %q", got)
	}
	if got := DefaultStreamRule(""); got != "" {
		t.Fatalf("DefaultStreamRule(empty) = %q", got)
	}
}

func TestEnsureRuleNoopWhenRuleExists(t *testing.T) {
	ctx := context.Background()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q, want Bearer token-1", got)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"rule-1","value":"from:dummy_user -is:retweet -is:reply","tag":"note-tweet-connector"}]}`))
	}))
	defer server.Close()

	client := NewStreamClient(StaticBearerTokenSource{Token: "token-1"})
	client.RulesEndpoint = server.URL + "/2/tweets/search/stream/rules"
	client.HTTPClient = server.Client()

	if err := client.EnsureRule(ctx, DefaultStreamRule("dummy_user"), "note-tweet-connector"); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}
	want := []string{"GET /2/tweets/search/stream/rules"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestEnsureRuleReplacesStaleTaggedRule(t *testing.T) {
	ctx := context.Background()
	var bodies []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q, want Bearer token-1", got)
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"id":"old-rule","value":"from:old_user -is:retweet","tag":"note-tweet-connector"},{"id":"other","value":"cats","tag":"external"}]}`))
		case http.MethodPost:
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			bodies = append(bodies, body)
			_, _ = w.Write([]byte(`{"meta":{"sent":"ok"}}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client := NewStreamClient(StaticBearerTokenSource{Token: "token-1"})
	client.RulesEndpoint = server.URL + "/2/tweets/search/stream/rules"
	client.HTTPClient = server.Client()

	if err := client.EnsureRule(ctx, DefaultStreamRule("dummy_user"), "note-tweet-connector"); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("POST count = %d, want 2", len(bodies))
	}
	if _, ok := bodies[0]["delete"]; !ok {
		t.Fatalf("first POST body = %#v, want delete", bodies[0])
	}
	if _, ok := bodies[1]["add"]; !ok {
		t.Fatalf("second POST body = %#v, want add", bodies[1])
	}
}

func TestConsumeReadsStreamLines(t *testing.T) {
	ctx := context.Background()
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q, want Bearer token-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("\r\n"))
		_, _ = w.Write([]byte(`{"data":{"id":"1","text":"hello"}}` + "\n"))
	}))
	defer server.Close()

	client := NewStreamClient(StaticBearerTokenSource{Token: "token-1"})
	client.StreamEndpoint = server.URL + "/2/tweets/search/stream"
	client.StreamHTTPClient = server.Client()
	client.KeepAliveTimeout = time.Second

	var lines [][]byte
	err := client.Consume(ctx, func(ctx context.Context, line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	})
	if err == nil {
		t.Fatal("Consume() error = nil, want EOF after test server closes")
	}
	if len(lines) != 1 || string(lines[0]) != `{"data":{"id":"1","text":"hello"}}` {
		t.Fatalf("lines = %q", lines)
	}
	for _, want := range []string{"tweet.fields=", "expansions=", "user.fields=username", "media.fields="} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q does not contain %q", gotQuery, want)
		}
	}
}

func TestNewStreamClientUsesHTTPClientWithoutTimeoutForStream(t *testing.T) {
	client := NewStreamClient(StaticBearerTokenSource{Token: "token-1"})
	if client.StreamHTTPClient == nil {
		t.Fatal("StreamHTTPClient is nil")
	}
	if client.StreamHTTPClient.Timeout != 0 {
		t.Fatalf("StreamHTTPClient.Timeout = %s, want 0", client.StreamHTTPClient.Timeout)
	}
	if client.HTTPClient == nil || client.HTTPClient.Timeout == 0 {
		t.Fatalf("HTTPClient timeout = %v, want non-zero timeout for non-stream requests", client.HTTPClient.Timeout)
	}
}

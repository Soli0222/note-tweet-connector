package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Soli0222/note-tweet-connector/internal/metrics"
	"github.com/Soli0222/note-tweet-connector/internal/tracker"
)

func TestParseNotePayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
		check   func(*testing.T, *payloadNoteData)
	}{
		{
			name: "valid payload with text",
			payload: `{
				"body": {
					"note": {
						"id": "dummy-note-1",
						"text": "This is a test note",
						"visibility": "public",
						"localOnly": false,
						"files": [],
						"cw": null
					}
				},
				"server": "https://misskey.example"
			}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadNoteData) {
				if p.Body.Note.ID != "dummy-note-1" {
					t.Errorf("expected note ID 'dummy-note-1', got '%s'", p.Body.Note.ID)
				}
				if p.Body.Note.Text != "This is a test note" {
					t.Errorf("expected text 'This is a test note', got '%s'", p.Body.Note.Text)
				}
				if p.Server != "https://misskey.example" {
					t.Errorf("expected server 'https://misskey.example', got '%s'", p.Server)
				}
			},
		},
		{
			name: "valid payload with CW",
			payload: `{
				"body": {
					"note": {
						"id": "dummy-note-2",
						"text": "Hidden content",
						"visibility": "public",
						"localOnly": false,
						"files": [],
						"cw": "Content Warning"
					}
				},
				"server": "https://misskey.example"
			}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadNoteData) {
				if p.Body.Note.Cw != "Content Warning" {
					t.Errorf("expected CW 'Content Warning', got '%s'", p.Body.Note.Cw)
				}
			},
		},
		{
			name: "valid payload with renote",
			payload: `{
				"body": {
					"note": {
						"id": "dummy-note-3",
						"text": null,
						"visibility": "public",
						"localOnly": false,
						"files": [],
						"cw": null,
						"renote": {
							"id": "original-note",
							"uri": "https://remote.example/notes/original",
							"text": "Original note content",
							"user": {
								"host": "remote.example",
								"username": "remoteuser"
							}
						}
					}
				},
				"server": "https://misskey.example"
			}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadNoteData) {
				if p.Body.Note.Renote.Text != "Original note content" {
					t.Errorf("expected renote text 'Original note content', got '%s'", p.Body.Note.Renote.Text)
				}
				if p.Body.Note.Renote.User.Username != "remoteuser" {
					t.Errorf("expected renote username 'remoteuser', got '%s'", p.Body.Note.Renote.User.Username)
				}
			},
		},
		{
			name:    "invalid JSON",
			payload: `{invalid json}`,
			wantErr: true,
			check:   nil,
		},
		{
			name: "full misskey webhook payload",
			payload: `{
				"body": {
					"note": {
						"channel": null,
						"channelId": null,
						"clippedCount": 0,
						"createdAt": "2025-11-28T15:29:57.766Z",
						"cw": null,
						"deletedAt": null,
						"emojis": {},
						"fileIds": [],
						"files": [],
						"id": "dummy-note-1",
						"isHidden": false,
						"localOnly": true,
						"mentions": [],
						"myReaction": null,
						"poll": null,
						"reactionAcceptance": "likeOnly",
						"reactionAndUserPairCache": [],
						"reactionCount": 0,
						"reactionEmojis": {},
						"reactions": {},
						"renote": null,
						"renoteCount": 10,
						"renoteId": null,
						"repliesCount": 5,
						"reply": null,
						"replyId": null,
						"tags": [],
						"text": "This is a dummy note for testing purposes.",
						"user": {
							"avatarBlurhash": null,
							"avatarDecorations": [],
							"avatarUrl": "",
							"badgeRoles": [],
							"emojis": {},
							"host": null,
							"id": "dummy-user-1",
							"isBot": false,
							"isCat": true,
							"isDsite": true,
							"isNoCat": true,
							"isSheep": true,
							"name": "DummyUser1",
							"onlineStatus": "active",
							"username": "dummy1"
						},
						"userId": "dummy-user-1",
						"visibility": "public",
						"visibleUserIds": []
					}
				},
				"createdAt": 1764343797766,
				"eventId": "830ebb53-8042-452c-9e68-9bf238df21a5",
				"hookId": "afmj9k8mlbjg0004",
				"server": "http://localhost:3000",
				"type": "note",
				"userId": "adllfptlpy0w0003"
			}`,
			wantErr: false,
			check: func(t *testing.T, p *payloadNoteData) {
				if p.Body.Note.ID != "dummy-note-1" {
					t.Errorf("expected note ID 'dummy-note-1', got '%s'", p.Body.Note.ID)
				}
				if p.Body.Note.Text != "This is a dummy note for testing purposes." {
					t.Errorf("expected text 'This is a dummy note for testing purposes.', got '%s'", p.Body.Note.Text)
				}
				if p.Body.Note.Visibility != "public" {
					t.Errorf("expected visibility 'public', got '%s'", p.Body.Note.Visibility)
				}
				if p.Server != "http://localhost:3000" {
					t.Errorf("expected server 'http://localhost:3000', got '%s'", p.Server)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseNotePayload([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseNotePayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil && result != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestNote2TweetHandler_SkipConditions(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name: "skip non-public note",
			payload: `{
				"body": {
					"note": {
						"id": "note-private",
						"text": "Private note",
						"visibility": "followers",
						"localOnly": false,
						"files": [],
						"cw": null
					}
				},
				"server": "https://misskey.example"
			}`,
			wantErr: false,
		},
		{
			name: "skip RT @ pattern",
			payload: `{
				"body": {
					"note": {
						"id": "note-rt",
						"text": "RT @someone this is a retweet",
						"visibility": "public",
						"localOnly": false,
						"files": [],
						"cw": null
					}
				},
				"server": "https://misskey.example"
			}`,
			wantErr: false,
		},
		{
			name: "skip home visibility",
			payload: `{
				"body": {
					"note": {
						"id": "note-home",
						"text": "Home only note",
						"visibility": "home",
						"localOnly": false,
						"files": [],
						"cw": null
					}
				},
				"server": "https://misskey.example"
			}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Note2TweetHandler(ctx, []byte(tt.payload), contentTracker, m)
			if (err != nil) != tt.wantErr {
				t.Errorf("Note2TweetHandler() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNote2TweetHandler_DuplicateDetection(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	// First note
	payload1 := `{
		"body": {
			"note": {
				"id": "note-1",
				"text": "Duplicate test content",
				"visibility": "public",
				"localOnly": false,
				"files": [],
				"cw": null
			}
		},
		"server": "https://misskey.example"
	}`

	// Second note with same content but different ID
	payload2 := `{
		"body": {
			"note": {
				"id": "note-2",
				"text": "Duplicate test content",
				"visibility": "public",
				"localOnly": false,
				"files": [],
				"cw": null
			}
		},
		"server": "https://misskey.example"
	}`

	// Process first note - this will fail at Twitter posting but content should be tracked
	_ = Note2TweetHandler(ctx, []byte(payload1), contentTracker, m)

	// The content should now be marked as processed
	// Second call should detect duplicate
	err := Note2TweetHandler(ctx, []byte(payload2), contentTracker, m)
	if err != nil {
		t.Errorf("Note2TweetHandler() should not return error for duplicate, got %v", err)
	}
}

func TestNote2TweetHandler_CWHandling(t *testing.T) {
	payload := `{
		"body": {
			"note": {
				"id": "note-cw",
				"text": "Spoiler content",
				"visibility": "public",
				"localOnly": false,
				"files": [],
				"cw": "Spoiler Alert"
			}
		},
		"server": "https://misskey.example"
	}`

	result, err := parseNotePayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseNotePayload() error = %v", err)
	}

	if result.Body.Note.Cw != "Spoiler Alert" {
		t.Errorf("expected CW 'Spoiler Alert', got '%s'", result.Body.Note.Cw)
	}
}

func TestNote2TweetHandler_FileExtraction(t *testing.T) {
	payload := `{
		"body": {
			"note": {
				"id": "note-files",
				"text": "Note with images",
				"visibility": "public",
				"localOnly": false,
				"files": [
					{"type": "image/png", "url": "https://example.com/image1.png"},
					{"type": "image/jpeg", "url": "https://example.com/image2.jpg"},
					{"type": "video/mp4", "url": "https://example.com/video.mp4"},
					{"type": "image/gif", "url": "https://example.com/image3.gif"}
				],
				"cw": null
			}
		},
		"server": "https://misskey.example"
	}`

	result, err := parseNotePayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseNotePayload() error = %v", err)
	}

	if len(result.Body.Note.Files) != 4 {
		t.Errorf("expected 4 files, got %d", len(result.Body.Note.Files))
	}

	// Verify image filtering logic
	imageCount := 0
	for _, f := range result.Body.Note.Files {
		if m, ok := f.(map[string]interface{}); ok {
			typeStr, _ := m["type"].(string)
			if typeStr != "" && containsSubstring(typeStr, "image") {
				imageCount++
			}
		}
	}

	if imageCount != 3 {
		t.Errorf("expected 3 images, got %d", imageCount)
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestRTAtPattern(t *testing.T) {
	tests := []struct {
		text    string
		matches bool
	}{
		{"RT @user this is a retweet", true},
		{"RT@user no space", true},
		{"RT  @user extra space", true},
		{"rt @user lowercase", false},
		{"Not a retweet", false},
		{"Something RT @user in middle", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := rtAtPattern.MatchString(tt.text)
			if result != tt.matches {
				t.Errorf("rtAtPattern.MatchString(%q) = %v, want %v", tt.text, result, tt.matches)
			}
		})
	}
}

func TestNote2TweetHandler_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	contentTracker := tracker.NewContentTracker(ctx, 1*time.Hour)
	m := metrics.NewNoop()

	err := Note2TweetHandler(ctx, []byte(`{invalid json}`), contentTracker, m)
	if err == nil {
		t.Error("Note2TweetHandler() should return error for invalid JSON")
	}
}

func TestNote2TweetHandler_RenoteHandling(t *testing.T) {
	t.Setenv("MISSKEY_HOST", "misskey.example")

	payload := `{
		"body": {
			"note": {
				"id": "note-renote",
				"text": null,
				"visibility": "public",
				"localOnly": false,
				"files": [],
				"cw": null,
				"renote": {
					"id": "original-note",
					"uri": "https://remote.example/notes/original",
					"text": "Original content from remote",
					"user": {
						"host": "remote.example",
						"username": "remoteuser"
					}
				}
			}
		},
		"server": "https://misskey.example"
	}`

	result, err := parseNotePayload([]byte(payload))
	if err != nil {
		t.Fatalf("parseNotePayload() error = %v", err)
	}

	if result.Body.Note.Renote.User.Username != "remoteuser" {
		t.Errorf("expected renote username 'remoteuser', got '%s'", result.Body.Note.Renote.User.Username)
	}
	if result.Body.Note.Renote.User.Host != "remote.example" {
		t.Errorf("expected renote host 'remote.example', got '%s'", result.Body.Note.Renote.User.Host)
	}
}

func TestPayloadNoteDataJSON(t *testing.T) {
	original := &payloadNoteData{
		Server: "https://misskey.example",
	}
	original.Body.Note.ID = "test-id"
	original.Body.Note.Text = "test text"
	original.Body.Note.Visibility = "public"

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed payloadNoteData
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if parsed.Server != original.Server {
		t.Errorf("Server mismatch: got %s, want %s", parsed.Server, original.Server)
	}
	if parsed.Body.Note.ID != original.Body.Note.ID {
		t.Errorf("Note.ID mismatch: got %s, want %s", parsed.Body.Note.ID, original.Body.Note.ID)
	}
}

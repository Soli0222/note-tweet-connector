package misskey

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateNoteWithFilesUsesBearerAuth(t *testing.T) {
	var gotAuth string
	var gotBody map[string]interface{}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/notes/create" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"createdNote":{"id":"note-1"}}`))
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	host := strings.TrimPrefix(server.URL, "https://")
	noteID, err := CreateNoteWithFiles(context.Background(), host, "test-token", "hello", []string{"file-1"})
	if err != nil {
		t.Fatalf("CreateNoteWithFiles() error = %v", err)
	}
	if noteID != "note-1" {
		t.Fatalf("noteID = %q, want note-1", noteID)
	}

	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer token", gotAuth)
	}
	if _, ok := gotBody["i"]; ok {
		t.Fatalf("body unexpectedly included i: %#v", gotBody)
	}
	if gotBody["text"] != "hello" {
		t.Fatalf("text = %#v", gotBody["text"])
	}
	fileIDs, ok := gotBody["fileIds"].([]interface{})
	if !ok || len(fileIDs) != 1 || fileIDs[0] != "file-1" {
		t.Fatalf("fileIds = %#v", gotBody["fileIds"])
	}
}

func TestCreateNoteWithOptionsIncludesRenoteID(t *testing.T) {
	var gotBody map[string]interface{}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/notes/create" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"createdNote":{"id":"note-quote"}}`))
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	host := strings.TrimPrefix(server.URL, "https://")
	noteID, err := CreateNoteWithOptions(context.Background(), host, "test-token", CreateNoteOptions{
		Text:     "quote text",
		FileIDs:  []string{"file-1"},
		RenoteID: "source-note",
	})
	if err != nil {
		t.Fatalf("CreateNoteWithOptions() error = %v", err)
	}
	if noteID != "note-quote" {
		t.Fatalf("noteID = %q, want note-quote", noteID)
	}
	if gotBody["text"] != "quote text" {
		t.Fatalf("text = %#v", gotBody["text"])
	}
	if gotBody["renoteId"] != "source-note" {
		t.Fatalf("renoteId = %#v, want source-note", gotBody["renoteId"])
	}
	fileIDs, ok := gotBody["fileIds"].([]interface{})
	if !ok || len(fileIDs) != 1 || fileIDs[0] != "file-1" {
		t.Fatalf("fileIds = %#v", gotBody["fileIds"])
	}
}

func TestUploadDriveFileFromURL(t *testing.T) {
	var gotAuth string
	var gotFile string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media/sample.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		case "/api/drive/files/create":
			gotAuth = r.Header.Get("Authorization")
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Fatalf("ParseMultipartForm() error = %v", err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("FormFile() error = %v", err)
			}
			defer func() { _ = file.Close() }()
			if header.Filename != "sample.png" {
				t.Fatalf("filename = %q", header.Filename)
			}
			body, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			gotFile = string(body)
			if r.FormValue("i") != "" {
				t.Fatalf("multipart unexpectedly included i")
			}
			_, _ = w.Write([]byte(`{"id":"drive-file-1"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	host := strings.TrimPrefix(server.URL, "https://")

	gotID, err := UploadDriveFileFromURLWithAllowedHosts(context.Background(), host, "test-token", server.URL+"/media/sample.png", []string{host})
	if err != nil {
		t.Fatalf("UploadDriveFileFromURL() error = %v", err)
	}
	if gotID != "drive-file-1" {
		t.Fatalf("id = %q", gotID)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotFile != "png-bytes" {
		t.Fatalf("uploaded file = %q", gotFile)
	}
}

func TestValidateTwitterMediaURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "allowed", url: "https://pbs.twimg.com/media/sample.png"},
		{name: "http rejected", url: "http://pbs.twimg.com/media/sample.png", wantErr: true},
		{name: "host rejected", url: "https://example.com/media/sample.png", wantErr: true},
		{name: "invalid", url: "://bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTwitterMediaURL(tt.url, []string{"pbs.twimg.com", "video.twimg.com"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateTwitterMediaURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUploadDriveFileFromURLRejectsNonImage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not image"))
	}))
	defer server.Close()

	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()

	host := strings.TrimPrefix(server.URL, "https://")

	_, err := UploadDriveFileFromURLWithAllowedHosts(context.Background(), host, "test-token", server.URL+"/media/sample.txt", []string{host})
	if err == nil {
		t.Fatal("UploadDriveFileFromURL() expected error for non-image response")
	}
}

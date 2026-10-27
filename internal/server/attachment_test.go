package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentUploadDownloadScopeAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &app.Service{Store: db}
	p, err := s.SaveProject(context.Background(), domain.Project{Name: "files"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.SaveProject(context.Background(), domain.Project{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&server.Server{App: s}).Handler())
	defer srv.Close()
	var body bytes.Buffer
	multi := multipart.NewWriter(&body)
	w, err := multi.CreateFormFile("file", "requirements.md")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("# Attached requirement\n退款上限 998 元。")
	w.Write(content)
	multi.Close()
	resp, err := http.Post(srv.URL+"/api/projects/"+p.ID+"/attachments", multi.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	var a domain.Attachment
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatal(resp.StatusCode, string(b))
	}
	if err = json.NewDecoder(resp.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if a.Size != len(content) || a.Text != "" || a.Content != nil {
		t.Fatal("upload metadata leaked content", a)
	}
	resp, err = http.Get(srv.URL + "/api/projects/" + p.ID + "/attachments/" + a.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, content) || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatal("download does not match")
	}
	resp, err = http.Get(srv.URL + "/api/projects/" + other.ID + "/attachments/" + a.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-project attachment accessible")
	}
	srv.Close()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stored, err := db.GetAttachment(context.Background(), a.ID)
	if err != nil || !bytes.Equal(stored.Content, content) || !strings.Contains(stored.Text, "998") {
		t.Fatal("attachment lost after restart", err)
	}
}

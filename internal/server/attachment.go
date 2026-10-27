package server

import (
	"ai-pm-system/internal/attachment"
	"ai-pm-system/internal/domain"
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(attachment.MaxSize); err != nil {
		fail(w, errors.New("附件上传失败，单个文件上限为 10 MB"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		fail(w, errors.New("每次上传需要一个 file 附件"))
		return
	}
	f, err := files[0].Open()
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, attachment.MaxSize+1))
	if err != nil {
		fail(w, err)
		return
	}
	a, err := s.App.SaveAttachment(r.Context(), s.actor(r), r.PathValue("id"), files[0].Filename, content)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, a)
}

func (s *Server) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	a, err := s.App.Store.GetAttachment(r.Context(), r.PathValue("attachment"))
	if err != nil {
		fail(w, err)
		return
	}
	if a.ProjectID != r.PathValue("id") {
		fail(w, domain.ErrForbidden)
		return
	}
	disposition := "attachment"
	if a.Kind == "image" && strings.HasPrefix(a.MIME, "image/") && r.URL.Query().Get("preview") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", a.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Name}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, a.Name, time.UnixMilli(a.At), bytes.NewReader(a.Content))
}

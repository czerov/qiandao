package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"qiandao/internal/oneonefivecookie"
)

func (s *Server) handle115CookieApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": oneonefivecookie.AppOptions()})
}

func (s *Server) handle115CookieCreateQRCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		App string `json:"app"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	svc, err := s.get115CookieService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	session, err := svc.CreateSession(ctx, req.App)
	if err != nil {
		writeError(w, oneonefivecookie.StatusCodeForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": session})
}

func (s *Server) handle115CookieSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/115-cookie/sessions/")
	if strings.TrimSpace(id) == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	svc, err := s.get115CookieService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	session, ok := svc.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "115 扫码会话不存在或已过期")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": session})
}

func (s *Server) get115CookieService() (*oneonefivecookie.Service, error) {
	s.cookie115Mu.Lock()
	defer s.cookie115Mu.Unlock()

	if s.cookie115Service != nil {
		return s.cookie115Service, nil
	}
	service, err := oneonefivecookie.NewService(oneonefivecookie.Config{
		RequestTimeout: 30 * time.Second,
		HTTPClient:     oneonefivecookie.NewDirectHTTPClient(30 * time.Second),
	})
	if err != nil {
		return nil, err
	}
	s.cookie115Service = service
	return service, nil
}

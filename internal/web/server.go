package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"qiandao/internal/domain"
	"qiandao/internal/oneonefivecookie"
	"qiandao/internal/service"
	"qiandao/internal/store"
)

type Server struct {
	store      store.Store
	service    *service.SignInService
	staticDir  string
	sessionKey []byte

	cookie115Mu      sync.Mutex
	cookie115Service *oneonefivecookie.Service
}

func New(st store.Store, svc *service.SignInService, staticDir string) *Server {
	key := []byte(os.Getenv("SIGNIN_SESSION_SECRET"))
	if len(key) == 0 {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
	}
	return &Server{
		store:      st,
		service:    svc,
		staticDir:  staticDir,
		sessionKey: key,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ping" {
		_, _ = w.Write([]byte("pong signin-app"))
		return
	}
	if r.URL.Path == "/api/login" {
		s.handleLogin(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.authenticated(r) {
			writeError(w, http.StatusUnauthorized, "未登录")
			return
		}
		s.handleAPI(w, r)
		return
	}
	s.serveStatic(w, r)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Username), []byte(settings.Web.Username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(req.Password), []byte(settings.Web.Password)) != 1 {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	exp := time.Now().Add(7 * 24 * time.Hour)
	token := s.signSession(req.Username, exp)
	http.SetCookie(w, &http.Cookie{
		Name:     "signin_session",
		Value:    token,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/logout":
		http.SetCookie(w, &http.Cookie{Name: "signin_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case r.URL.Path == "/api/me":
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case r.URL.Path == "/api/config":
		s.handleConfig(w, r)
	case r.URL.Path == "/api/overview":
		s.handleOverview(w, r)
	case r.URL.Path == "/api/accounts":
		s.handleAccounts(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/accounts/"):
		s.handleAccountByID(w, r)
	case r.URL.Path == "/api/signin/batch":
		s.handleBatchSignIn(w, r)
	case r.URL.Path == "/api/signin/records":
		s.handleRecords(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/signin/"):
		s.handleSignInOne(w, r)
	case r.URL.Path == "/api/115-cookie/apps":
		s.handle115CookieApps(w, r)
	case r.URL.Path == "/api/115-cookie/qrcode":
		s.handle115CookieCreateQRCode(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/115-cookie/sessions/"):
		s.handle115CookieSession(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GetSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": settings.Masked()})
	case http.MethodPost:
		current, err := s.store.GetSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var next domain.Settings
		if err := readJSON(r, &next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		next = domain.MergeMaskedSettings(next, current)
		if err := s.store.SaveSettings(r.Context(), next); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": next.Masked()})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accounts, err := s.store.ListAccounts(r.Context(), domain.AccountFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	records, err := s.store.ListRecords(r.Context(), domain.RecordFilter{Limit: 30})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	var todaySuccess, todayFailed int
	for _, record := range records {
		t := record.FinishedAt.In(loc)
		if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
			if record.Success {
				todaySuccess++
			} else {
				todayFailed++
			}
		}
	}
	enabled := 0
	for _, account := range accounts {
		if account.Enabled {
			enabled++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"account_count":  len(accounts),
			"enabled_count":  enabled,
			"today_success":  todaySuccess,
			"today_failed":   todayFailed,
			"recent_records": records,
		},
	})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accounts, err := s.store.ListAccounts(r.Context(), domain.AccountFilter{Platform: r.URL.Query().Get("platform")})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for i := range accounts {
			accounts[i] = accounts[i].Masked()
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": accounts})
	case http.MethodPost:
		var acc domain.Account
		if err := readJSON(r, &acc); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		now := time.Now()
		acc.ID = domain.NewID("acc")
		acc.CreatedAt = now
		acc.UpdatedAt = now
		normalizeAccount(&acc)
		if err := validateAccount(acc); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.SaveAccount(r.Context(), acc); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": acc.Masked()})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAccountByID(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	if tail == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if strings.HasSuffix(tail, "/toggle") {
		id := strings.TrimSuffix(tail, "/toggle")
		s.handleToggleAccount(w, r, id)
		return
	}
	id := tail
	switch r.Method {
	case http.MethodPut:
		current, err := s.store.GetAccount(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		var next domain.Account
		if err := readJSON(r, &next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		next.ID = current.ID
		next.CreatedAt = current.CreatedAt
		next.UpdatedAt = time.Now()
		next.Credential = domain.MergeMaskedCredential(next.Credential, current.Credential)
		normalizeAccount(&next)
		if err := validateAccount(next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.SaveAccount(r.Context(), next); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": next.Masked()})
	case http.MethodDelete:
		if err := s.store.DeleteAccount(r.Context(), id); err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleToggleAccount(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	acc, err := s.store.GetAccount(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	acc.Enabled = !acc.Enabled
	acc.UpdatedAt = time.Now()
	if err := s.store.SaveAccount(r.Context(), acc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": acc.Masked()})
}

func (s *Server) handleSignInOne(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/signin/")
	if id == "" || id == "batch" || id == "records" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := s.service.SignInAccount(ctx, id, "manual")
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{"success": err == nil, "data": result, "error": errorString(err)})
}

func (s *Server) handleBatchSignIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Platform    string `json:"platform"`
		OnlyEnabled bool   `json:"only_enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	results, err := s.service.SignInAll(ctx, req.Platform, req.OnlyEnabled, "batch")
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{"success": err == nil, "data": results, "error": errorString(err)})
}

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := s.store.ListRecords(r.Context(), domain.RecordFilter{
		AccountID: r.URL.Query().Get("account_id"),
		Platform:  r.URL.Query().Get("platform"),
		Limit:     limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": records})
}

func (s *Server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie("signin_session")
	if err != nil || cookie.Value == "" {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return false
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < exp
}

func (s *Server) signSession(username string, exp time.Time) string {
	payload := []byte(fmt.Sprintf("%s|%d", username, exp.Unix()))
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write(payload)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	clean := filepath.Clean(strings.TrimPrefix(path, "/"))
	if clean == "." || strings.HasPrefix(clean, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.staticDir, clean)
	if _, err := os.Stat(full); err != nil {
		full = filepath.Join(s.staticDir, "index.html")
	}
	http.ServeFile(w, r, full)
}

func normalizeAccount(acc *domain.Account) {
	acc.Platform = strings.ToLower(strings.TrimSpace(acc.Platform))
	acc.Name = strings.TrimSpace(acc.Name)
	acc.Cron = strings.TrimSpace(acc.Cron)
	if acc.Cron == "" {
		acc.Cron = "30 8"
	}
	acc.Credential.Username = strings.TrimSpace(acc.Credential.Username)
	acc.Credential.APIKey = strings.TrimSpace(acc.Credential.APIKey)
	acc.Credential.AppID = strings.TrimSpace(acc.Credential.AppID)
	acc.Credential.SessionID = strings.TrimSpace(acc.Credential.SessionID)
	acc.Credential.CSRFToken = strings.TrimSpace(acc.Credential.CSRFToken)
	acc.Options.AuthMode = strings.TrimSpace(acc.Options.AuthMode)
}

func validateAccount(acc domain.Account) error {
	switch acc.Platform {
	case domain.PlatformHDHive, domain.PlatformJuYing:
	default:
		return fmt.Errorf("平台必须是 hdhive 或 juying")
	}
	parts := strings.Fields(acc.Cron)
	if len(parts) < 2 {
		return fmt.Errorf("定时格式应为 MM HH，例如 30 8")
	}
	return nil
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "error": msg})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "记录不存在")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

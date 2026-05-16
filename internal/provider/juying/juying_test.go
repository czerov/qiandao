package juying

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginSetsTokenAndFrontendHeaders(t *testing.T) {
	var sawLoginHeaders bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/csrf/":
			http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "csrf-token", Path: "/"})
			_, _ = w.Write([]byte(`{"status":"success"}`))
		case "/api/app/login/":
			sawLoginHeaders = r.Header.Get("X-Requested-With") == "XMLHttpRequest" &&
				r.Header.Get("X-CSRFToken") == "csrf-token"
			_, _ = w.Write([]byte(`{"status":"success","data":{"token":"app-token"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	httpClient := srv.Client()
	httpClient.Jar, _ = cookiejar.New(nil)
	c := &client{http: httpClient, baseURL: srv.URL, headers: map[string]string{}}

	if err := c.login(context.Background(), "user@example.com", "password"); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if !sawLoginHeaders {
		t.Fatalf("login request did not include frontend-compatible headers")
	}
	if got := c.headers["X-App-User-Token"]; got != "app-token" {
		t.Fatalf("X-App-User-Token = %q, want app-token", got)
	}
}

func TestLoginReturnsServerMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/csrf/" {
			_, _ = w.Write([]byte(`{"status":"success"}`))
			return
		}
		if r.URL.Path != "/api/app/login/" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","message":"用户名/邮箱或密码错误"}`))
	}))
	defer srv.Close()

	httpClient := srv.Client()
	httpClient.Jar, _ = cookiejar.New(nil)
	c := &client{http: httpClient, baseURL: srv.URL, headers: map[string]string{}}

	err := c.login(context.Background(), "user@example.com", "bad-password")
	if err == nil {
		t.Fatal("login returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "用户名/邮箱或密码错误") || !strings.Contains(got, "HTTP 400") {
		t.Fatalf("login error = %q, want server message and HTTP status", got)
	}
}

func TestLoginReturnsNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := srv.URL
	srv.Close()

	c := &client{
		http:    &http.Client{Timeout: 100 * time.Millisecond},
		baseURL: baseURL,
		headers: map[string]string{},
	}

	err := c.login(context.Background(), "user@example.com", "password")
	if err == nil {
		t.Fatal("login returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "聚影登录失败:") {
		t.Fatalf("login error = %q, want network detail", got)
	}
}

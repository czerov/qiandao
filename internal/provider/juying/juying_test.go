package juying

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qiandao/internal/domain"
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
	if got := c.userToken; got != "app-token" {
		t.Fatalf("userToken = %q, want app-token", got)
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

func TestSignInUsesWebTokenOverDeveloperAuth(t *testing.T) {
	var sawSignin bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/csrf/":
			http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "csrf-value", Path: "/"})
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/app/login/":
			_, _ = w.Write([]byte(`{"token":"user-token","message":"登录成功"}`))
		case "/api/app/checkin/do/":
			sawSignin = true
			if got := r.Header.Get("X-App-User-Token"); got != "user-token" {
				t.Fatalf("X-App-User-Token = %q", got)
			}
			if got := r.Header.Get("X-App-Id"); got != "" {
				t.Fatalf("unexpected X-App-Id = %q", got)
			}
			if got := r.Header.Get("X-CSRFToken"); got != "csrf-value" {
				t.Fatalf("X-CSRFToken = %q", got)
			}
			_, _ = w.Write([]byte(`{"success":true,"message":"登录后签到成功","data":{"reward_points":5}}`))
		case "/api/app/profile/", "/api/app/checkin/stats/":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	result, err := New().SignIn(context.Background(), domain.Account{
		ID:       "acc",
		Platform: domain.PlatformJuYing,
		Credential: domain.AccountCredential{
			Username: "user@example.com",
			Password: "secret",
			AppID:    "app-id",
			APIKey:   "api-key",
		},
	}, domain.Settings{Providers: domain.ProviderSettings{JuYing: domain.JuYingSettings{BaseURL: srv.URL}}})
	if err != nil {
		t.Fatalf("SignIn returned error: %v", err)
	}
	if !sawSignin || !result.SignInResult.Success || result.SignInResult.Message != "登录后签到成功" {
		t.Fatalf("result=%#v sawSignin=%v", result.SignInResult, sawSignin)
	}
}

func TestDiscoverCheckinPathFromNestedBundle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><script type="module" src="/assets/main.js"></script></html>`))
		case "/assets/main.js":
			_, _ = w.Write([]byte(`component:()=>import("./CheckIn.js")`))
		case "/assets/CheckIn.js":
			_, _ = w.Write([]byte(`const pe={getStats(){return api.get("/api/app/checkin/stats/")},doCheckin(){return api.post("/api/app/checkin/do/")}};`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &client{http: srv.Client(), baseURL: srv.URL, headers: map[string]string{}}
	got, err := c.discoverCheckinPath(context.Background())
	if err != nil {
		t.Fatalf("discoverCheckinPath returned error: %v", err)
	}
	if got != "/api/app/checkin/do/" {
		t.Fatalf("discoverCheckinPath = %q, want /api/app/checkin/do/", got)
	}
}

func TestExtractCheckinPathPrefersDoCheckin(t *testing.T) {
	source := "var t={getStats(){return e.get(`/api/app/checkin/stats/`)},doCheckin(){return e.post(`/api/app/checkin/do/`)}};"
	if got := extractCheckinPath(source); got != "/api/app/checkin/do/" {
		t.Fatalf("extractCheckinPath = %q, want /api/app/checkin/do/", got)
	}
}

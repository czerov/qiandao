package hdhive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindActionIDFromCreateServerReference(t *testing.T) {
	const chunk = `let d=(0,s.createServerReference)("6068df40ea98050274d29084c0083d1712cc19e909",s.callServer,void 0,s.findSourceMapURL,"login");`
	got := findActionID(chunk, []string{"login"})
	want := "6068df40ea98050274d29084c0083d1712cc19e909"
	if got != want {
		t.Fatalf("findActionID() = %q, want %q", got, want)
	}
}

func TestHDHiveLoginActionPayload(t *testing.T) {
	payload := nextActionPayload(map[string]string{
		"username":           "user@example.com",
		"password":           encodePassword("12345678"),
		"password_transport": "base64",
	}, "/")
	for _, want := range []string{
		`"username":"user@example.com"`,
		`"password":"MTIzNDU2Nzg="`,
		`"password_transport":"base64"`,
		`"/"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload %q missing %q", payload, want)
		}
	}
}

func TestMessageFromNextActionBody(t *testing.T) {
	body := []byte("0:{\"a\":\"$@1\",\"f\":\"\",\"b\":\"build\",\"q\":\"\",\"i\":false}\n1:{\"error\":{\"success\":false,\"message\":\"用户名或密码错误\",\"code\":\"401\",\"internal_detail\":\"用户名或密码错误\"}}\n")
	if got := messageFromBody(body, "fallback"); got != "用户名或密码错误" {
		t.Fatalf("messageFromBody() = %q, want %q", got, "用户名或密码错误")
	}
	if likelySuccess(body) {
		t.Fatal("likelySuccess() = true, want false")
	}
}

func TestMessageFromNestedError(t *testing.T) {
	body := []byte(`{"error":{"message":"需要额外验证","code":"403"}}`)
	if got := messageFromBody(body, "fallback"); got != "需要额外验证" {
		t.Fatalf("messageFromBody() = %q, want %q", got, "需要额外验证")
	}
	if likelySuccess(body) {
		t.Fatal("likelySuccess() = true, want false")
	}
}

func TestLoginActionCapturesCookieWithBrowserHeaders(t *testing.T) {
	var sawActionHeaders bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<script src="/chunk.js"></script>`))
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<script src="/chunk.js"></script>`))
				return
			}
			sawActionHeaders = r.Header.Get("Next-Action") == hdhiveLoginActionFallback &&
				r.Header.Get("Next-Router-State-Tree") == hdhiveLoginRouterStateTree &&
				r.Header.Get("x-nextjs-post") == "1"
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
			w.WriteHeader(http.StatusSeeOther)
		case "/chunk.js":
			_, _ = w.Write([]byte(`let d=(0,s.createServerReference)("` + hdhiveLoginActionFallback + `",s.callServer,void 0,s.findSourceMapURL,"login");`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := srv.Client()
	if err := login(context.Background(), client, srv.URL, "user@example.com", "password123"); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if !sawActionHeaders {
		t.Fatal("login action did not include HDHive browser-compatible action headers")
	}
	if got := authCookieString(client, srv.URL); !strings.Contains(got, "session=ok") {
		t.Fatalf("authCookieString() = %q, want session cookie", got)
	}
}

func TestParseCookiePairsSkipsAttributes(t *testing.T) {
	cookies := parseCookiePairs("session=abc; Path=/; HttpOnly; SameSite=Lax; csrftoken=def")
	var names []string
	for _, cookie := range cookies {
		names = append(names, cookie.Name)
	}
	got := strings.Join(names, ",")
	if got != "session,csrftoken" {
		t.Fatalf("cookie names = %q, want session,csrftoken", got)
	}
}

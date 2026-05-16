package hdhive

import (
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

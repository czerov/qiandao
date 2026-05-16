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

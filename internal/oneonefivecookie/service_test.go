package oneonefivecookie

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFormatCookieTextIncludesKIDWhenPresent(t *testing.T) {
	got := FormatCookieText(Cookie{
		UID:  "u",
		CID:  "c",
		SEID: "s",
		KID:  "k",
	})
	want := "UID=u; CID=c; SEID=s; KID=k"
	if got != want {
		t.Fatalf("FormatCookieText() = %q, want %q", got, want)
	}
}

func TestFormatCookieTextOmitsEmptyKID(t *testing.T) {
	got := FormatCookieText(Cookie{
		UID:  "u",
		CID:  "c",
		SEID: "s",
	})
	if strings.Contains(got, "KID=") {
		t.Fatalf("FormatCookieText() should omit empty KID: %q", got)
	}
}

func TestCookieValidateRejectsMissingRequiredFields(t *testing.T) {
	err := (Cookie{UID: "u", CID: "c"}).Validate()
	if err == nil {
		t.Fatal("expected missing SEID to be rejected")
	}
}

func TestAppValidation(t *testing.T) {
	if !IsValidApp("alipaymini") {
		t.Fatal("expected alipaymini to be supported")
	}
	if !IsValidApp("qandroid") || !IsValidApp("qandriod") {
		t.Fatal("expected qandroid alias and qandriod app to be supported")
	}
	if IsValidApp("harmony") {
		t.Fatal("harmony should be excluded")
	}
}

func TestGetQRCodeTokenUsesSelectedAppEndpoint(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":true,"code":0,"data":{"uid":"u","time":123,"sign":"s"}}`))
	}))
	defer server.Close()

	client := testClient(t, server.Client(), server.URL, server.URL)
	token, err := client.GetQRCodeToken(context.Background(), "alipaymini")
	if err != nil {
		t.Fatalf("GetQRCodeToken() error = %v", err)
	}
	if gotPath != "/api/1.0/alipaymini/1.0/token/" {
		t.Fatalf("GetQRCodeToken() path = %q, want selected app token endpoint", gotPath)
	}
	if token.UID != "u" || token.Time != 123 || token.Sign != "s" {
		t.Fatalf("GetQRCodeToken() = %+v", token)
	}
}

func TestGetQRCodeImageUsesSelectedAppEndpoint(t *testing.T) {
	var gotPath string
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer server.Close()

	client := testClient(t, server.Client(), server.URL, server.URL)
	dataURL, err := client.GetQRCodeImageDataURL(context.Background(), "mac", "uid 1")
	if err != nil {
		t.Fatalf("GetQRCodeImageDataURL() error = %v", err)
	}
	if gotPath != "/api/1.0/mac/1.0/qrcode" {
		t.Fatalf("GetQRCodeImageDataURL() path = %q, want selected app qrcode endpoint", gotPath)
	}
	if gotQuery != "uid=uid+1" {
		t.Fatalf("GetQRCodeImageDataURL() query = %q", gotQuery)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("GetQRCodeImageDataURL() = %q", dataURL)
	}
}

func TestGetLoginResultFallsBackToPassportAPI(t *testing.T) {
	qrHit := false
	qrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qrHit = true
		http.Error(w, "temporary upstream failure", http.StatusBadGateway)
	}))
	defer qrServer.Close()

	passportHit := false
	passportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passportHit = true
		if r.URL.Path != "/app/1.0/alipaymini/1.0/login/qrcode/" {
			t.Fatalf("passport path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":true,"code":0,"data":{"cookie":{"UID":"u","CID":"c","SEID":"s","KID":"k"}}}`))
	}))
	defer passportServer.Close()

	client := testClient(t, passportServer.Client(), passportServer.URL, qrServer.URL)
	result, err := client.GetLoginResult(context.Background(), "alipaymini", "uid")
	if err != nil {
		t.Fatalf("GetLoginResult() error = %v", err)
	}
	if !qrHit || !passportHit {
		t.Fatalf("expected qrcodeapi and passportapi to be called, got qr=%v passport=%v", qrHit, passportHit)
	}
	if result.Cookie.UID != "u" || result.Cookie.CID != "c" || result.Cookie.SEID != "s" || result.Cookie.KID != "k" {
		t.Fatalf("GetLoginResult() cookie = %+v", result.Cookie)
	}
}

func TestGetLoginResultMapsDesktopApps(t *testing.T) {
	var gotPath string
	var gotAppField string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatalf("ParseMultipartForm() error = %v", err)
		}
		gotAppField = r.FormValue("app")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":true,"code":0,"data":{"cookie":{"UID":"u","CID":"c","SEID":"s"}}}`))
	}))
	defer server.Close()

	client := testClient(t, server.Client(), server.URL, server.URL)
	if _, err := client.GetLoginResult(context.Background(), "mac", "uid"); err != nil {
		t.Fatalf("GetLoginResult() error = %v", err)
	}
	if gotPath != "/app/1.0/os_mac/1.0/login/qrcode/" {
		t.Fatalf("GetLoginResult() path = %q, want os_mac endpoint", gotPath)
	}
	if gotAppField != "" {
		t.Fatalf("GetLoginResult() sent unexpected app form field %q", gotAppField)
	}
}

func TestGetLoginResultMapsIOSUserAgent(t *testing.T) {
	var gotPath string
	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":true,"code":0,"data":{"cookie":{"UID":"u","CID":"c","SEID":"s"}}}`))
	}))
	defer server.Close()

	client := testClient(t, server.Client(), server.URL, server.URL)
	if _, err := client.GetLoginResult(context.Background(), "ios", "uid"); err != nil {
		t.Fatalf("GetLoginResult() error = %v", err)
	}
	if gotPath != "/app/1.0/ios/1.0/login/qrcode/" {
		t.Fatalf("GetLoginResult() path = %q, want ios endpoint", gotPath)
	}
	if gotUserAgent != "UPhone/1.0.0" {
		t.Fatalf("GetLoginResult() user agent = %q", gotUserAgent)
	}
}

func TestGetQRCodeTokenAnnotatesDirectTimeout(t *testing.T) {
	timeoutClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, timeoutNetError{}
	})}

	client := testClient(t, timeoutClient, "https://passportapi.115.com", "https://qrcodeapi.115.com")
	_, err := client.GetQRCodeToken(context.Background(), "alipaymini")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "直连") {
		t.Fatalf("timeout error should explain direct transport, got %v", err)
	}
}

func TestNewDirectHTTPClientDoesNotUseProxy(t *testing.T) {
	client := NewDirectHTTPClient(time.Second)
	if client.Timeout != time.Second {
		t.Fatalf("NewDirectHTTPClient() timeout = %v", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("NewDirectHTTPClient() transport = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("NewDirectHTTPClient() must not use a proxy")
	}
}

func TestNormalizeConfigDefaultsToDirectClient(t *testing.T) {
	cfg, err := normalizeConfig(Config{})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	transport, ok := cfg.HTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTPClient transport = %T", cfg.HTTPClient.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("default HTTPClient must be direct and ignore environment proxy")
	}
}

func testClient(t *testing.T, httpClient *http.Client, passportBaseURL, qrCodeBaseURL string) *Client {
	t.Helper()
	cfg, err := normalizeConfig(Config{
		PassportAPIBaseURL: passportBaseURL,
		QRCodeAPIBaseURL:   qrCodeBaseURL,
		RequestTimeout:     time.Second,
		HTTPClient:         httpClient,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	return newClient(cfg)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string {
	return "dial tcp timeout"
}

func (timeoutNetError) Timeout() bool {
	return true
}

func (timeoutNetError) Temporary() bool {
	return true
}

var _ error = timeoutNetError{}

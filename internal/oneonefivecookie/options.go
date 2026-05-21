package oneonefivecookie

import (
	"strings"
	"time"
)

const (
	defaultPassportAPIBaseURL = "https://passportapi.115.com"
	defaultQRCodeAPIBaseURL   = "https://qrcodeapi.115.com"
	defaultDirectDialTimeout  = 15 * time.Second

	sessionStatusPending    = "pending"
	sessionStatusScanned    = "scanned"
	sessionStatusAuthorized = "authorized"
	sessionStatusCanceled   = "canceled"
	sessionStatusExpired    = "expired"
	sessionStatusFailed     = "failed"
)

type AppOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var appOptions = []AppOption{
	{Value: "web", Label: "网页版"},
	{Value: "android", Label: "115生活(Android端)"},
	{Value: "115android", Label: "115(Android端)"},
	{Value: "ios", Label: "115生活(iOS端)"},
	{Value: "115ipad", Label: "115(iPad端)"},
	{Value: "tv", Label: "115网盘(Android电视端)"},
	{Value: "alipaymini", Label: "115生活(支付宝小程序)"},
	{Value: "wechatmini", Label: "115生活(微信小程序)"},
	{Value: "qandriod", Label: "115管理(Android端)"},
	{Value: "115ios", Label: "115(iOS端)"},
	{Value: "ipad", Label: "iPad端"},
	{Value: "qios", Label: "115管理(iOS端)"},
	{Value: "qipad", Label: "115管理(iPad端)"},
	{Value: "linux", Label: "Linux端"},
	{Value: "mac", Label: "macOS端"},
	{Value: "windows", Label: "Windows端"},
}

func AppOptions() []AppOption {
	out := make([]AppOption, len(appOptions))
	copy(out, appOptions)
	return out
}

func IsValidApp(app string) bool {
	app = normalizeApp(app)
	for _, option := range appOptions {
		if option.Value == app {
			return true
		}
	}
	return false
}

func normalizeApp(app string) string {
	app = strings.TrimSpace(app)
	if app == "qandroid" {
		return "qandriod"
	}
	return app
}

func loginAppSpecFor(app string) loginAppSpec {
	app = normalizeApp(app)
	switch app {
	case "desktop":
		return loginAppSpec{App: "web"}
	case "windows", "mac", "linux":
		return loginAppSpec{App: "os_" + app}
	case "ios":
		return loginAppSpec{App: "ios", UserAgent: "UPhone/1.0.0"}
	case "qios":
		return loginAppSpec{App: "ios", UserAgent: "OfficePhone/1.0.0"}
	case "ipad":
		return loginAppSpec{App: "ios", UserAgent: "UPad/1.0.0"}
	case "qipad":
		return loginAppSpec{App: "ios", UserAgent: "OfficePad/1.0.0"}
	default:
		return loginAppSpec{App: app}
	}
}

type loginAppSpec struct {
	App       string
	UserAgent string
}

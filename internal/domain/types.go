package domain

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

const (
	PlatformHDHive = "hdhive"
	PlatformJuYing = "juying"
	MaskValue      = "********"
)

type Account struct {
	ID            string            `json:"id"`
	Platform      string            `json:"platform"`
	Name          string            `json:"name"`
	Enabled       bool              `json:"enabled"`
	Cron          string            `json:"cron"`
	NotifyTG      bool              `json:"notify_tg"`
	NotifyWebhook bool              `json:"notify_webhook"`
	Credential    AccountCredential `json:"credential"`
	Options       AccountOptions    `json:"options"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type AccountCredential struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	APIKey    string `json:"api_key"`
	AppID     string `json:"app_id"`
	Cookie    string `json:"cookie"`
	SessionID string `json:"sessionid"`
	CSRFToken string `json:"csrftoken"`
}

type AccountOptions struct {
	Dog      bool   `json:"dog"`
	AuthMode string `json:"auth_mode"`
}

type SignInResult struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id"`
	Platform     string    `json:"platform"`
	AccountName  string    `json:"account_name"`
	Trigger      string    `json:"trigger"`
	Success      bool      `json:"success"`
	Message      string    `json:"message"`
	Mode         string    `json:"mode"`
	Username     string    `json:"username"`
	Nickname     string    `json:"nickname"`
	Email        string    `json:"email"`
	SigninDays   int       `json:"signin_days"`
	RewardPoints int       `json:"reward_points"`
	TotalPoints  int       `json:"total_points"`
	Raw          string    `json:"raw,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

type Settings struct {
	Timezone  string           `json:"timezone"`
	ProxyURL  string           `json:"proxy_url"`
	Web       WebConfig        `json:"web"`
	Notify    NotifyConfig     `json:"notify"`
	Providers ProviderSettings `json:"providers"`
	Scheduler SchedulerConfig  `json:"scheduler"`
}

type WebConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type NotifyConfig struct {
	TelegramBotToken string `json:"telegram_bot_token"`
	TelegramAdminID  string `json:"telegram_admin_id"`
	TelegramProxyURL string `json:"telegram_proxy_url"`
	WebhookURL       string `json:"webhook_url"`
}

type ProviderSettings struct {
	HDHive HDHiveSettings `json:"hdhive"`
	JuYing JuYingSettings `json:"juying"`
}

type HDHiveSettings struct {
	BaseURL      string `json:"base_url"`
	GlobalAPIKey string `json:"global_api_key"`
	ProxyURL     string `json:"proxy_url"`
}

type JuYingSettings struct {
	BaseURL    string `json:"base_url"`
	SigninPath string `json:"signin_path"`
	ProxyMode  string `json:"proxy_mode"`
	ProxyURL   string `json:"proxy_url"`
}

type SchedulerConfig struct {
	RandomDelaySeconds int `json:"random_delay_seconds"`
}

type AccountFilter struct {
	Platform    string
	OnlyEnabled bool
}

type RecordFilter struct {
	AccountID string
	Platform  string
	Limit     int
}

func DefaultSettings() Settings {
	return Settings{
		Timezone: "Asia/Shanghai",
		Web: WebConfig{
			Username: "admin",
			Password: "admin",
		},
		Providers: ProviderSettings{
			HDHive: HDHiveSettings{
				BaseURL: "https://hdhive.com",
			},
			JuYing: JuYingSettings{
				BaseURL:    "https://share.huamucang.top",
				SigninPath: "/api/app/checkin/do/",
				ProxyMode:  "direct",
			},
		},
		Scheduler: SchedulerConfig{
			RandomDelaySeconds: 15,
		},
	}
}

func (s Settings) WithDefaults() Settings {
	d := DefaultSettings()
	if strings.TrimSpace(s.Timezone) == "" {
		s.Timezone = d.Timezone
	}
	if strings.TrimSpace(s.Web.Username) == "" {
		s.Web.Username = d.Web.Username
	}
	if s.Web.Password == "" {
		s.Web.Password = d.Web.Password
	}
	if strings.TrimSpace(s.Providers.HDHive.BaseURL) == "" {
		s.Providers.HDHive.BaseURL = d.Providers.HDHive.BaseURL
	}
	if strings.TrimSpace(s.Providers.JuYing.BaseURL) == "" {
		s.Providers.JuYing.BaseURL = d.Providers.JuYing.BaseURL
	}
	if strings.TrimSpace(s.Providers.JuYing.SigninPath) == "" {
		s.Providers.JuYing.SigninPath = d.Providers.JuYing.SigninPath
	}
	if strings.TrimSpace(s.Providers.JuYing.ProxyMode) == "" {
		s.Providers.JuYing.ProxyMode = d.Providers.JuYing.ProxyMode
	}
	if s.Scheduler.RandomDelaySeconds < 0 {
		s.Scheduler.RandomDelaySeconds = 0
	}
	return s
}

func (a Account) DisplayName() string {
	if strings.TrimSpace(a.Name) != "" {
		return strings.TrimSpace(a.Name)
	}
	if strings.TrimSpace(a.Credential.Username) != "" {
		return strings.TrimSpace(a.Credential.Username)
	}
	if strings.TrimSpace(a.Credential.AppID) != "" {
		return strings.TrimSpace(a.Credential.AppID)
	}
	if a.ID != "" {
		return a.ID
	}
	return "未命名账号"
}

func (a Account) Masked() Account {
	a.Credential = a.Credential.Masked()
	return a
}

func (c AccountCredential) Masked() AccountCredential {
	if c.Password != "" {
		c.Password = MaskValue
	}
	if c.APIKey != "" {
		c.APIKey = MaskValue
	}
	if c.Cookie != "" {
		c.Cookie = MaskValue
	}
	if c.SessionID != "" {
		c.SessionID = MaskValue
	}
	if c.CSRFToken != "" {
		c.CSRFToken = MaskValue
	}
	return c
}

func MergeMaskedCredential(next, old AccountCredential) AccountCredential {
	if next.Password == MaskValue {
		next.Password = old.Password
	}
	if next.APIKey == MaskValue {
		next.APIKey = old.APIKey
	}
	if next.Cookie == MaskValue {
		next.Cookie = old.Cookie
	}
	if next.SessionID == MaskValue {
		next.SessionID = old.SessionID
	}
	if next.CSRFToken == MaskValue {
		next.CSRFToken = old.CSRFToken
	}
	return next
}

func (s Settings) Masked() Settings {
	if s.Web.Password != "" {
		s.Web.Password = MaskValue
	}
	if s.Notify.TelegramBotToken != "" {
		s.Notify.TelegramBotToken = MaskValue
	}
	if s.Notify.WebhookURL != "" {
		s.Notify.WebhookURL = MaskValue
	}
	if s.Providers.HDHive.GlobalAPIKey != "" {
		s.Providers.HDHive.GlobalAPIKey = MaskValue
	}
	return s
}

func MergeMaskedSettings(next, old Settings) Settings {
	if next.Web.Password == MaskValue {
		next.Web.Password = old.Web.Password
	}
	if next.Notify.TelegramBotToken == MaskValue {
		next.Notify.TelegramBotToken = old.Notify.TelegramBotToken
	}
	if next.Notify.WebhookURL == MaskValue {
		next.Notify.WebhookURL = old.Notify.WebhookURL
	}
	if next.Providers.HDHive.GlobalAPIKey == MaskValue {
		next.Providers.HDHive.GlobalAPIKey = old.Providers.HDHive.GlobalAPIKey
	}
	return next.WithDefaults()
}

func (s Settings) SignInProxyURL() string {
	if strings.TrimSpace(s.ProxyURL) != "" {
		return strings.TrimSpace(s.ProxyURL)
	}
	if strings.TrimSpace(s.Providers.HDHive.ProxyURL) != "" {
		return strings.TrimSpace(s.Providers.HDHive.ProxyURL)
	}
	juyingProxyMode := strings.TrimSpace(s.Providers.JuYing.ProxyMode)
	if strings.TrimSpace(s.Providers.JuYing.ProxyURL) != "" && juyingProxyMode == "custom_proxy" {
		return strings.TrimSpace(s.Providers.JuYing.ProxyURL)
	}
	if juyingProxyMode == "tg_proxy" {
		return strings.TrimSpace(s.Notify.TelegramProxyURL)
	}
	return ""
}

func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

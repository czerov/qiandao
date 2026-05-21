package oneonefivecookie

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type Config struct {
	PassportAPIBaseURL string
	QRCodeAPIBaseURL   string
	RequestTimeout     time.Duration
	PollInterval       time.Duration
	SessionTimeout     time.Duration
	SessionRetention   time.Duration
	UserAgent          string
	HTTPClient         *http.Client
}

type Service struct {
	cfg      Config
	client   *Client
	sessions *SessionStore
}

type Client struct {
	httpClient      *http.Client
	passportBaseURL string
	qrCodeBaseURL   string
	requestTimeout  time.Duration
	userAgent       string
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*AuthSession
}

type AuthSession struct {
	ID               string        `json:"id"`
	App              string        `json:"app"`
	UID              string        `json:"uid"`
	RemoteIssuedAt   int64         `json:"-"`
	RemoteSign       string        `json:"-"`
	QRCodePNGDataURL string        `json:"qr_code_png_data_url,omitempty"`
	Status           string        `json:"status"`
	Message          string        `json:"message"`
	RawStatus        int           `json:"raw_status"`
	Cookie           *Cookie       `json:"-"`
	CookieText       string        `json:"cookie_text,omitempty"`
	CookieJSON       []CookieEntry `json:"cookie_json,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	LastError        string        `json:"last_error,omitempty"`
	cancel           context.CancelFunc
}

type SessionResponse struct {
	ID               string        `json:"id"`
	App              string        `json:"app"`
	UID              string        `json:"uid"`
	Status           string        `json:"status"`
	Message          string        `json:"message"`
	RawStatus        int           `json:"raw_status"`
	QRCodePNGDataURL string        `json:"qr_code_png_data_url,omitempty"`
	CookieText       string        `json:"cookie_text,omitempty"`
	CookieJSON       []CookieEntry `json:"cookie_json,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	LastError        string        `json:"last_error,omitempty"`
}

type QRTokenData struct {
	UID  string `json:"uid"`
	Time int64  `json:"time"`
	Sign string `json:"sign"`
}

type QRStatusData struct {
	Status int `json:"status"`
}

type Cookie struct {
	UID  string `json:"UID"`
	CID  string `json:"CID"`
	SEID string `json:"SEID"`
	KID  string `json:"KID,omitempty"`
}

type LoginData struct {
	Cookie Cookie `json:"cookie"`
}

type APIEnvelope[T any] struct {
	State   any    `json:"state"`
	Code    int    `json:"code"`
	Errno   int    `json:"errno"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type APIError struct {
	URL        string
	StatusCode int
	Code       int
	Errno      int
	Message    string
	Body       string
}

type CookieEntry struct {
	Domain   string `json:"domain"`
	HostOnly bool   `json:"hostOnly"`
	HTTPOnly bool   `json:"httpOnly"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	SameSite string `json:"sameSite"`
	Secure   bool   `json:"secure"`
	Session  bool   `json:"session"`
	StoreID  string `json:"storeId"`
	Value    string `json:"value"`
	ID       int    `json:"id"`
}

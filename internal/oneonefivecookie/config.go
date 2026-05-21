package oneonefivecookie

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func normalizeConfig(cfg Config) (Config, error) {
	passportBase, err := normalizeBaseURL(firstNonEmpty(cfg.PassportAPIBaseURL, defaultPassportAPIBaseURL))
	if err != nil {
		return Config{}, fmt.Errorf("passport api base url: %w", err)
	}
	qrBase, err := normalizeBaseURL(firstNonEmpty(cfg.QRCodeAPIBaseURL, defaultQRCodeAPIBaseURL))
	if err != nil {
		return Config{}, fmt.Errorf("qrcode api base url: %w", err)
	}

	cfg.PassportAPIBaseURL = passportBase
	cfg.QRCodeAPIBaseURL = qrBase
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 15 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = 60 * time.Second
	}
	if cfg.SessionRetention <= 0 {
		cfg.SessionRetention = 30 * time.Minute
	}
	cfg.UserAgent = firstNonEmpty(cfg.UserAgent, "qiandao/115-cookie")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = NewDirectHTTPClient(30 * time.Second)
	}
	return cfg, nil
}

func normalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("must include host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func NewDirectHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   defaultDirectDialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   defaultDirectDialTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func newClient(cfg Config) *Client {
	return &Client{
		httpClient:      cfg.HTTPClient,
		passportBaseURL: cfg.PassportAPIBaseURL,
		qrCodeBaseURL:   cfg.QRCodeAPIBaseURL,
		requestTimeout:  cfg.RequestTimeout,
		userAgent:       cfg.UserAgent,
	}
}

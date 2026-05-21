package oneonefivecookie

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func isNegativeAPIState(state any) bool {
	switch value := state.(type) {
	case nil:
		return false
	case bool:
		return !value
	case float64:
		return value == 0
	case string:
		normalized := strings.TrimSpace(strings.ToLower(value))
		return normalized == "0" || normalized == "false" || normalized == "fail" || normalized == "failed"
	default:
		return false
	}
}

func generateSessionID() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(random)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func StatusCodeForError(err error) int {
	if isTimeoutError(err) {
		return http.StatusGatewayTimeout
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return http.StatusBadGateway
	}
	if strings.Contains(strings.ToLower(err.Error()), "unsupported 115 login app") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func annotateNetworkFailure(err error, endpoint string) error {
	if err == nil || !isTimeoutError(err) {
		return err
	}
	return fmt.Errorf("%w；当前使用直连访问 %s，代码不会读取系统或环境代理，请检查容器/主机出站、防火墙、DNS 或运营商线路", err, endpointHost(endpoint))
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "115 API"
	}
	return parsed.Hostname()
}

func truncateBody(body []byte) string {
	const maxLen = 512
	if len(body) <= maxLen {
		return string(body)
	}
	return string(body[:maxLen]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (e *APIError) Error() string {
	parts := []string{"115 API error"}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.Code != 0 {
		parts = append(parts, fmt.Sprintf("code=%d", e.Code))
	}
	if e.Errno != 0 {
		parts = append(parts, fmt.Sprintf("errno=%d", e.Errno))
	}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.StatusCode))
	}
	return strings.Join(parts, " ")
}

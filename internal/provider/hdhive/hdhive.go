package hdhive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"qiandao/internal/domain"
	"qiandao/internal/httpx"
	"qiandao/internal/provider"
)

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Platform() string {
	return domain.PlatformHDHive
}

func (p *Provider) SignIn(ctx context.Context, account domain.Account, settings domain.Settings) (provider.Result, error) {
	settings = settings.WithDefaults()
	baseURL := httpx.NormalizeBaseURL(settings.Providers.HDHive.BaseURL, "https://hdhive.com")
	apiKey := strings.TrimSpace(account.Credential.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(settings.Providers.HDHive.GlobalAPIKey)
	}
	useWeb := account.Options.Dog || account.Credential.Username != "" || account.Credential.Password != "" || account.Credential.Cookie != ""
	if useWeb {
		result, err := p.webSignIn(ctx, account, settings, baseURL)
		if err == nil {
			return result, nil
		}
		if account.Options.Dog || apiKey == "" {
			return provider.Result{}, err
		}
		fallback, fallbackErr := p.openAPISignIn(ctx, account, settings, baseURL, apiKey)
		if fallbackErr == nil {
			fallback.SignInResult.Message = "网页登录失败，已回退 Open API: " + fallback.SignInResult.Message
			return fallback, nil
		}
		return provider.Result{}, fmt.Errorf("网页登录失败: %v; Open API 回退也失败: %w", err, fallbackErr)
	}
	if apiKey == "" {
		return provider.Result{}, fmt.Errorf("影巢 Open API 签到需要账号 API Key 或全局 API Key")
	}
	return p.openAPISignIn(ctx, account, settings, baseURL, apiKey)
}

func (p *Provider) openAPISignIn(ctx context.Context, account domain.Account, settings domain.Settings, baseURL, apiKey string) (provider.Result, error) {
	client, err := httpx.NewClient(settings.Providers.HDHive.ProxyURL, 30*time.Second)
	if err != nil {
		return provider.Result{}, err
	}
	body, status, err := doJSONRequest(ctx, client, http.MethodPost, httpx.JoinURL(baseURL, "/api/open/checkin"), nil, func(req *http.Request) {
		setOpenAPIHeaders(req, apiKey)
	})
	if err != nil {
		return provider.Result{}, err
	}
	if status == http.StatusMethodNotAllowed {
		body, status, err = doJSONRequest(ctx, client, http.MethodGet, httpx.JoinURL(baseURL, "/api/open/checkin"), nil, func(req *http.Request) {
			setOpenAPIHeaders(req, apiKey)
		})
		if err != nil {
			return provider.Result{}, err
		}
	}
	result := parseHDHiveResult(body, status)
	result.Platform = domain.PlatformHDHive
	result.AccountID = account.ID
	result.AccountName = account.DisplayName()
	result.Mode = "Open API"
	meBody, meStatus, meErr := doJSONRequest(ctx, client, http.MethodGet, httpx.JoinURL(baseURL, "/api/open/me"), nil, func(req *http.Request) {
		setOpenAPIHeaders(req, apiKey)
	})
	if meErr == nil && meStatus < 400 {
		mergeHDHiveMe(&result, meBody)
	}
	if !result.Success {
		return provider.Result{SignInResult: result}, fmt.Errorf("%s", result.Message)
	}
	return provider.Result{SignInResult: result}, nil
}

func (p *Provider) webSignIn(ctx context.Context, account domain.Account, settings domain.Settings, baseURL string) (provider.Result, error) {
	var lastErr error
	for _, host := range hostCandidates(baseURL) {
		result, err := p.webSignInHost(ctx, account, settings, host)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的影巢域名")
	}
	return provider.Result{}, lastErr
}

func (p *Provider) webSignInHost(ctx context.Context, account domain.Account, settings domain.Settings, baseURL string) (provider.Result, error) {
	client, err := httpx.NewClient(settings.Providers.HDHive.ProxyURL, 35*time.Second)
	if err != nil {
		return provider.Result{}, err
	}
	base, _ := url.Parse(baseURL)
	if strings.TrimSpace(account.Credential.Cookie) != "" {
		setCookieHeader(client, base, account.Credential.Cookie)
	} else {
		if account.Credential.Username == "" || account.Credential.Password == "" {
			return provider.Result{}, fmt.Errorf("影巢网页登录需要账号密码或有效 Cookie")
		}
		if err := login(ctx, client, baseURL, account.Credential.Username, account.Credential.Password); err != nil {
			return provider.Result{}, err
		}
	}
	body, status, err := webCheckIn(ctx, client, baseURL, account.Options.Dog)
	if err != nil {
		return provider.Result{}, err
	}
	result := parseHDHiveResult(body, status)
	result.Platform = domain.PlatformHDHive
	result.AccountID = account.ID
	result.AccountName = account.DisplayName()
	if account.Options.Dog {
		result.Mode = "赌狗抽签"
	} else {
		result.Mode = "网页签到"
	}
	if !result.Success {
		return provider.Result{SignInResult: result}, fmt.Errorf("%s", result.Message)
	}
	cookie := cookieString(client, base)
	var patch *domain.AccountCredential
	if cookie != "" && cookie != account.Credential.Cookie {
		next := account.Credential
		next.Cookie = cookie
		patch = &next
	}
	return provider.Result{SignInResult: result, CredentialPatch: patch}, nil
}

func setOpenAPIHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Api-Key", apiKey)
}

func doJSONRequest(ctx context.Context, client *http.Client, method, rawURL string, payload any, decorate func(*http.Request)) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if decorate != nil {
		decorate(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, 0, err
	}
	return raw, resp.StatusCode, nil
}

func login(ctx context.Context, client *http.Client, baseURL, username, password string) error {
	_, _, _ = doJSONRequest(ctx, client, http.MethodGet, httpx.JoinURL(baseURL, "/login"), nil, nil)
	jsonPayloads := []map[string]string{
		{"username": username, "password": password},
		{"email": username, "password": password},
		{"name": username, "password": password},
	}
	for _, endpoint := range []string{"/api/login", "/api/auth/login", "/api/user/login"} {
		for _, payload := range jsonPayloads {
			body, status, err := doJSONRequest(ctx, client, http.MethodPost, httpx.JoinURL(baseURL, endpoint), payload, nil)
			if err == nil && status < 400 && likelySuccess(body) {
				return nil
			}
		}
	}
	form := url.Values{}
	form.Set("username", username)
	form.Set("email", username)
	form.Set("password", password)
	for _, endpoint := range []string{"/login", "/auth/login"} {
		body, status, err := doFormRequest(ctx, client, httpx.JoinURL(baseURL, endpoint), form, nil)
		if err == nil && status < 400 && likelySuccess(body) {
			return nil
		}
	}
	if err := tryNextAction(ctx, client, baseURL, []string{"login", "signIn"}, fmt.Sprintf(`["%s","%s"]`, escapeJSONString(username), escapeJSONString(password))); err == nil {
		return nil
	}
	return fmt.Errorf("影巢网页登录失败，请检查账号密码、Cookie 或站点是否需要额外验证")
}

func webCheckIn(ctx context.Context, client *http.Client, baseURL string, dog bool) ([]byte, int, error) {
	payload := map[string]bool{"dog": dog}
	for _, endpoint := range []string{"/api/checkin", "/api/user/checkin", "/api/attendance/checkin", "/api/signin"} {
		body, status, err := doJSONRequest(ctx, client, http.MethodPost, httpx.JoinURL(baseURL, endpoint), payload, nil)
		if err == nil && status < 500 && !looksLikeNotFound(status, body) {
			return body, status, nil
		}
	}
	actionPayload := "[false]"
	if dog {
		actionPayload = "[true]"
	}
	body, status, err := postNextAction(ctx, client, baseURL, []string{"checkIn", "checkin", "signIn", "signin"}, actionPayload)
	if err == nil {
		return body, status, nil
	}
	return nil, 0, err
}

func doFormRequest(ctx context.Context, client *http.Client, rawURL string, form url.Values, decorate func(*http.Request)) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if decorate != nil {
		decorate(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, 0, err
	}
	return raw, resp.StatusCode, nil
}

func postNextAction(ctx context.Context, client *http.Client, baseURL string, names []string, payload string) ([]byte, int, error) {
	id, err := discoverActionID(ctx, client, baseURL, names)
	if err != nil {
		return nil, 0, err
	}
	for _, endpoint := range []string{"/", "/login", "/user", "/dashboard"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpx.JoinURL(baseURL, endpoint), strings.NewReader(payload))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125 Safari/537.36")
		req.Header.Set("Accept", "text/x-component")
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		req.Header.Set("Next-Action", id)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if readErr == nil && resp.StatusCode < 500 && !looksLikeNotFound(resp.StatusCode, raw) {
			return raw, resp.StatusCode, nil
		}
	}
	return nil, 0, fmt.Errorf("影巢 Server Action 调用失败")
}

func tryNextAction(ctx context.Context, client *http.Client, baseURL string, names []string, payload string) error {
	body, status, err := postNextAction(ctx, client, baseURL, names, payload)
	if err != nil {
		return err
	}
	if status >= 400 || !likelySuccess(body) {
		return fmt.Errorf("Server Action 返回失败")
	}
	return nil
}

func discoverActionID(ctx context.Context, client *http.Client, baseURL string, names []string) (string, error) {
	var blobs []string
	var scripts []string
	for _, page := range []string{"/", "/login", "/user", "/dashboard"} {
		body, status, err := doJSONRequest(ctx, client, http.MethodGet, httpx.JoinURL(baseURL, page), nil, nil)
		if err == nil && status < 400 {
			text := string(body)
			blobs = append(blobs, text)
			scripts = append(scripts, scriptSources(text)...)
		}
	}
	seen := map[string]bool{}
	for _, src := range scripts {
		if seen[src] || len(seen) > 12 {
			continue
		}
		seen[src] = true
		body, status, err := doJSONRequest(ctx, client, http.MethodGet, httpx.JoinURL(baseURL, src), nil, nil)
		if err == nil && status < 400 {
			blobs = append(blobs, string(body))
		}
	}
	for _, blob := range blobs {
		if id := findActionID(blob, names); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("未发现 Next.js Server Action ID")
}

func scriptSources(html string) []string {
	re := regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+\.js[^"']*)["']`)
	matches := re.FindAllStringSubmatch(html, -1)
	var out []string
	for _, m := range matches {
		if len(m) > 1 {
			out = append(out, strings.ReplaceAll(m[1], `\u0026`, "&"))
		}
	}
	return out
}

func findActionID(blob string, names []string) string {
	for _, name := range names {
		quoted := regexp.QuoteMeta(name)
		patterns := []string{
			`(?is)` + quoted + `.{0,400}?["']([a-f0-9]{32,80})["']`,
			`(?is)["']([a-f0-9]{32,80})["'].{0,400}?` + quoted,
		}
		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			if m := re.FindStringSubmatch(blob); len(m) > 1 {
				return m[1]
			}
		}
	}
	return ""
}

func parseHDHiveResult(raw []byte, status int) domain.SignInResult {
	result := domain.SignInResult{
		Success: status >= 200 && status < 300,
		Message: "签到请求已完成",
		Raw:     compactRaw(raw),
	}
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		result.Success = successFromMap(obj, result.Success)
		result.Message = messageFromMap(obj, result.Message)
		src := unwrapData(obj)
		result.Nickname = stringValue(src, "nickname", "nickName", "name")
		result.Username = stringValue(src, "username", "userName")
		result.Email = stringValue(src, "email")
		result.SigninDays = intValue(src, "signin_days", "signinDays", "checkin_days", "days")
		result.RewardPoints = intValue(src, "gained_points", "gainedPoints", "reward_points", "rewardPoints", "points_delta", "point")
		result.TotalPoints = intValue(src, "total_points", "totalPoints", "points", "bonus")
		if user, ok := src["user"].(map[string]any); ok {
			mergeUserFields(&result, user)
		}
		if meta, ok := src["userMeta"].(map[string]any); ok {
			mergeUserFields(&result, meta)
		}
		if meta, ok := src["user_meta"].(map[string]any); ok {
			mergeUserFields(&result, meta)
		}
		return result
	}
	text := strings.TrimSpace(string(raw))
	if text != "" {
		result.Message = firstLine(text)
		result.Success = result.Success && !containsAny(strings.ToLower(text), []string{"error", "failed", "失败", "错误"})
	}
	return result
}

func mergeHDHiveMe(result *domain.SignInResult, raw []byte) {
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	src := unwrapData(obj)
	mergeUserFields(result, src)
	if user, ok := src["user"].(map[string]any); ok {
		mergeUserFields(result, user)
	}
	if meta, ok := src["userMeta"].(map[string]any); ok {
		mergeUserFields(result, meta)
	}
	if meta, ok := src["user_meta"].(map[string]any); ok {
		mergeUserFields(result, meta)
	}
}

func mergeUserFields(result *domain.SignInResult, src map[string]any) {
	if result.Nickname == "" {
		result.Nickname = stringValue(src, "nickname", "nickName", "name")
	}
	if result.Username == "" {
		result.Username = stringValue(src, "username", "userName")
	}
	if result.Email == "" {
		result.Email = stringValue(src, "email")
	}
	if result.SigninDays == 0 {
		result.SigninDays = intValue(src, "signin_days", "signinDays", "checkin_days", "days")
	}
	if result.RewardPoints == 0 {
		result.RewardPoints = intValue(src, "gained_points", "gainedPoints", "reward_points", "rewardPoints", "points_delta")
	}
	if result.TotalPoints == 0 {
		result.TotalPoints = intValue(src, "total_points", "totalPoints", "points", "bonus")
	}
}

func unwrapData(obj map[string]any) map[string]any {
	for _, key := range []string{"data", "result", "user", "profile"} {
		if child, ok := obj[key].(map[string]any); ok {
			return child
		}
	}
	return obj
}

func successFromMap(obj map[string]any, fallback bool) bool {
	for _, key := range []string{"success", "ok"} {
		if v, ok := obj[key].(bool); ok {
			return v
		}
	}
	if code := intValue(obj, "code", "status_code", "statusCode"); code != 0 {
		return code == 0 || code == 200
	}
	status := strings.ToLower(stringValue(obj, "status", "state"))
	if status != "" {
		return status == "ok" || status == "success" || status == "done"
	}
	msg := messageFromMap(obj, "")
	if containsAny(msg, []string{"成功", "已签到", "already", "success"}) {
		return true
	}
	if containsAny(msg, []string{"失败", "错误", "error", "failed"}) {
		return false
	}
	return fallback
}

func messageFromMap(obj map[string]any, fallback string) string {
	msg := stringValue(obj, "message", "msg", "detail", "error", "reason")
	if msg != "" {
		return msg
	}
	if data, ok := obj["data"].(string); ok && data != "" {
		return data
	}
	return fallback
}

func stringValue(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := obj[key]; ok {
			switch x := v.(type) {
			case string:
				return strings.TrimSpace(x)
			case float64:
				return strconv.FormatFloat(x, 'f', -1, 64)
			case int:
				return strconv.Itoa(x)
			}
		}
	}
	return ""
}

func intValue(obj map[string]any, keys ...string) int {
	for _, key := range keys {
		if v, ok := obj[key]; ok {
			switch x := v.(type) {
			case float64:
				return int(x)
			case int:
				return x
			case int64:
				return int(x)
			case json.Number:
				i, _ := x.Int64()
				return int(i)
			case string:
				x = strings.TrimSpace(strings.TrimPrefix(x, "+"))
				i, _ := strconv.Atoi(x)
				return i
			}
		}
	}
	return 0
}

func likelySuccess(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	text := strings.ToLower(string(raw))
	if containsAny(text, []string{"invalid", "unauthorized", "forbidden", "失败", "错误", "密码错误"}) {
		return false
	}
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return successFromMap(obj, true)
	}
	return true
}

func looksLikeNotFound(status int, raw []byte) bool {
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusGone {
		return true
	}
	text := strings.ToLower(string(raw))
	return containsAny(text, []string{"not found", "404", "method not allowed"})
}

func containsAny(s string, needles []string) bool {
	s = strings.ToLower(s)
	for _, needle := range needles {
		if strings.Contains(s, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func compactRaw(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > 4000 {
		return text[:4000]
	}
	return text
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

func escapeJSONString(s string) string {
	raw, _ := json.Marshal(s)
	return strings.Trim(string(raw), `"`)
}

func hostCandidates(baseURL string) []string {
	baseURL = httpx.NormalizeBaseURL(baseURL, "https://hdhive.com")
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = httpx.NormalizeBaseURL(v, "")
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	add(baseURL)
	for _, host := range []string{"https://hdhive.com", "https://hdhive.org", "https://hdhive.online"} {
		add(host)
	}
	return out
}

func setCookieHeader(client *http.Client, base *url.URL, raw string) {
	if client.Jar == nil || base == nil {
		return
	}
	var cookies []*http.Cookie
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: strings.TrimSpace(value), Path: "/"})
	}
	client.Jar.SetCookies(base, cookies)
}

func cookieString(client *http.Client, base *url.URL) string {
	if client.Jar == nil || base == nil {
		return ""
	}
	var parts []string
	for _, c := range client.Jar.Cookies(base) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

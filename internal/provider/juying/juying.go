package juying

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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

type client struct {
	http      *http.Client
	baseURL   string
	headers   map[string]string
	account   domain.Account
	userToken string
}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Platform() string {
	return domain.PlatformJuYing
}

func (p *Provider) SignIn(ctx context.Context, account domain.Account, settings domain.Settings) (provider.Result, error) {
	settings = settings.WithDefaults()
	baseURL := httpx.NormalizeBaseURL(settings.Providers.JuYing.BaseURL, "https://share.huamucang.top")
	proxyURL := resolveProxy(settings)
	httpClient, err := httpx.NewClient(proxyURL, 35*time.Second)
	if err != nil {
		return provider.Result{}, err
	}
	if httpClient.Jar == nil {
		httpClient.Jar, _ = cookiejar.New(nil)
	}
	c := &client{
		http:    httpClient,
		baseURL: baseURL,
		headers: map[string]string{},
		account: account,
	}
	if err := c.applyAuth(ctx, account); err != nil {
		return provider.Result{}, err
	}
	path := settings.Providers.JuYing.SigninPath
	if strings.TrimSpace(path) == "" {
		path = "/api/app/checkin/do/"
	}
	body, status, err := c.postSignIn(ctx, path)
	if err != nil {
		return provider.Result{}, err
	}
	if authFailed(status, body) && account.Credential.Username != "" && account.Credential.Password != "" {
		c.userToken = ""
		if err := c.login(ctx, account.Credential.Username, account.Credential.Password); err != nil {
			return provider.Result{}, err
		}
		body, status, err = c.postSignIn(ctx, path)
		if err != nil {
			return provider.Result{}, err
		}
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusGone {
		if discovered, discoverErr := c.discoverCheckinPath(ctx); discoverErr == nil && discovered != "" && discovered != path {
			path = discovered
			body, status, err = c.postSignIn(ctx, path)
			if err != nil {
				return provider.Result{}, err
			}
		}
	}
	result := parseJuYingResult(body, status)
	result.Platform = domain.PlatformJuYing
	result.AccountID = account.ID
	result.AccountName = account.DisplayName()
	result.Mode = authMode(account)
	c.mergeProfile(ctx, &result)
	if !result.Success {
		return provider.Result{SignInResult: result}, fmt.Errorf("%s", result.Message)
	}
	return provider.Result{SignInResult: result}, nil
}

func resolveProxy(settings domain.Settings) string {
	if proxyURL := settings.SignInProxyURL(); proxyURL != "" {
		return proxyURL
	}
	mode := strings.TrimSpace(settings.Providers.JuYing.ProxyMode)
	switch mode {
	case "custom_proxy":
		return strings.TrimSpace(settings.Providers.JuYing.ProxyURL)
	case "tg_proxy":
		return strings.TrimSpace(settings.Notify.TelegramProxyURL)
	default:
		return ""
	}
}

func (c *client) applyAuth(ctx context.Context, account domain.Account) error {
	cred := account.Credential
	if strings.TrimSpace(cred.Cookie) != "" {
		c.setCookie(cred.Cookie)
		if token := extractCookieValue(cred.Cookie, "csrftoken"); token != "" {
			c.headers["X-CSRFToken"] = token
		}
		if token := extractCookieValue(cred.Cookie, "app_user_token"); token != "" {
			c.userToken = token
		}
		return nil
	}
	if strings.TrimSpace(cred.SessionID) != "" {
		cookie := "sessionid=" + cred.SessionID
		if strings.TrimSpace(cred.CSRFToken) != "" {
			cookie += "; csrftoken=" + cred.CSRFToken
			c.headers["X-CSRFToken"] = cred.CSRFToken
		}
		c.setCookie(cookie)
		return nil
	}
	if strings.TrimSpace(cred.AppID) != "" && strings.TrimSpace(cred.APIKey) != "" {
		c.headers["X-App-Id"] = cred.AppID
		c.headers["X-App-Key"] = cred.APIKey
		if strings.TrimSpace(cred.Username) == "" || strings.TrimSpace(cred.Password) == "" {
			return nil
		}
	}
	if strings.TrimSpace(cred.Username) != "" && strings.TrimSpace(cred.Password) != "" {
		return c.login(ctx, cred.Username, cred.Password)
	}
	return fmt.Errorf("聚影账号需要账号密码、Cookie、sessionid/csrftoken 或 AppID/API Key")
}

func (c *client) login(ctx context.Context, username, password string) error {
	appID, hadAppID := c.headers["X-App-Id"]
	appKey, hadAppKey := c.headers["X-App-Key"]
	delete(c.headers, "X-App-Id")
	delete(c.headers, "X-App-Key")
	defer func() {
		if hadAppID {
			c.headers["X-App-Id"] = appID
		}
		if hadAppKey {
			c.headers["X-App-Key"] = appKey
		}
	}()

	csrf := c.fetchCSRF(ctx)
	if csrf != "" {
		c.headers["X-CSRFToken"] = csrf
	}
	payload := map[string]string{"username": strings.TrimSpace(username), "password": password}
	var lastBody []byte
	var lastStatus int
	var lastErr error
	body, status, err := c.request(ctx, http.MethodPost, "/api/app/login/", payload)
	if err == nil {
		lastBody, lastStatus = body, status
		if status >= 200 && status < 300 {
			if token := extractLoginToken(body); token != "" {
				c.userToken = token
				c.headers["X-App-User-Token"] = token
				return nil
			}
			msg := messageFromBody(body, "登录响应未返回用户 token")
			return fmt.Errorf("%s", msg)
		}
		if msg := messageFromBody(body, ""); msg != "" {
			lastErr = fmt.Errorf("%s", msg)
		}
	} else {
		lastErr = err
	}
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	body, status, err = c.form(ctx, "/api/app/login/", form)
	if err == nil {
		lastBody, lastStatus = body, status
		if status >= 200 && status < 300 {
			if token := extractLoginToken(body); token != "" {
				c.userToken = token
				c.headers["X-App-User-Token"] = token
				return nil
			}
			msg := messageFromBody(body, "登录响应未返回用户 token")
			return fmt.Errorf("%s", msg)
		}
		if msg := messageFromBody(body, ""); msg != "" {
			lastErr = fmt.Errorf("%s", msg)
		}
	} else {
		lastErr = err
	}
	msg := "聚影登录失败"
	if len(lastBody) > 0 {
		msg = messageFromBody(lastBody, msg)
	}
	if lastStatus > 0 {
		msg = fmt.Sprintf("%s (HTTP %d)", msg, lastStatus)
	} else if lastErr != nil {
		msg = fmt.Sprintf("%s: %v", msg, lastErr)
	}
	return fmt.Errorf("%s", msg)
}

func (c *client) fetchCSRF(ctx context.Context) string {
	body, status, err := c.request(ctx, http.MethodGet, "/api/csrf/", nil)
	if err != nil || status >= 400 {
		return ""
	}
	if token := extractTokenByKeys(body, "csrfToken", "csrftoken", "csrf"); token != "" {
		return token
	}
	base, _ := url.Parse(c.baseURL)
	for _, ck := range c.http.Jar.Cookies(base) {
		if ck.Name == "csrftoken" {
			return ck.Value
		}
	}
	return ""
}

func (c *client) request(ctx context.Context, method, path string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, httpx.JoinURL(c.baseURL, path), body)
	if err != nil {
		return nil, 0, err
	}
	c.setBrowserHeaders(req)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.applyAuthHeaders(req)
	resp, err := c.http.Do(req)
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

func (c *client) postSignIn(ctx context.Context, path string) ([]byte, int, error) {
	return c.request(ctx, http.MethodPost, path, map[string]any{})
}

func (c *client) form(ctx context.Context, path string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpx.JoinURL(c.baseURL, path), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	c.setBrowserHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.applyAuthHeaders(req)
	resp, err := c.http.Do(req)
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

func (c *client) setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if base, err := url.Parse(c.baseURL); err == nil {
		req.Header.Set("Origin", base.Scheme+"://"+base.Host)
		req.Header.Set("Referer", httpx.JoinURL(c.baseURL, "/"))
	}
}

func (c *client) applyAuthHeaders(req *http.Request) {
	if c.userToken != "" {
		req.Header.Set("X-App-User-Token", c.userToken)
	}
	if c.hasWebCookie() {
		cookie := strings.TrimSpace(c.account.Credential.Cookie)
		if cookie == "" {
			cookie = buildCookieHeader(c.account.Credential)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
	}
	if csrf := c.csrfToken(); csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}
	if c.userToken == "" && !c.hasWebCookie() {
		if appID := strings.TrimSpace(c.headers["X-App-Id"]); appID != "" {
			req.Header.Set("X-App-Id", appID)
		}
		if apiKey := strings.TrimSpace(c.headers["X-App-Key"]); apiKey != "" {
			req.Header.Set("X-App-Key", apiKey)
		}
	}
	for k, v := range c.headers {
		if c.hasWebSessionAuth() && isDeveloperAuthHeader(k) {
			continue
		}
		if strings.TrimSpace(v) != "" && req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
}

func (c *client) hasWebSessionAuth() bool {
	return c.userToken != "" || c.hasWebCookie()
}

func isDeveloperAuthHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "x-app-id", "x-app-key":
		return true
	default:
		return false
	}
}

func (c *client) hasWebCookie() bool {
	cred := c.account.Credential
	return strings.TrimSpace(cred.Cookie) != "" ||
		strings.TrimSpace(cred.SessionID) != "" ||
		strings.TrimSpace(cred.CSRFToken) != ""
}

func (c *client) csrfToken() string {
	if token := strings.TrimSpace(c.headers["X-CSRFToken"]); token != "" {
		return token
	}
	cred := c.account.Credential
	if token := strings.TrimSpace(cred.CSRFToken); token != "" {
		return token
	}
	if token := extractCookieValue(cred.Cookie, "csrftoken"); token != "" {
		return token
	}
	if c.http != nil && c.http.Jar != nil {
		base, _ := url.Parse(c.baseURL)
		for _, ck := range c.http.Jar.Cookies(base) {
			if ck.Name == "csrftoken" {
				return ck.Value
			}
		}
	}
	return ""
}

func buildCookieHeader(cred domain.AccountCredential) string {
	var parts []string
	if v := strings.TrimSpace(cred.SessionID); v != "" {
		parts = append(parts, "sessionid="+v)
	}
	if v := strings.TrimSpace(cred.CSRFToken); v != "" {
		parts = append(parts, "csrftoken="+v)
	}
	return strings.Join(parts, "; ")
}

func (c *client) setCookie(raw string) {
	base, err := url.Parse(c.baseURL)
	if err != nil || c.http.Jar == nil {
		return
	}
	var cookies []*http.Cookie
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		if strings.TrimSpace(name) == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value), Path: "/"})
	}
	c.http.Jar.SetCookies(base, cookies)
}

func (c *client) discoverCheckinPath(ctx context.Context) (string, error) {
	queue := []string{httpx.JoinURL(c.baseURL, "/")}
	seen := map[string]bool{}
	for len(queue) > 0 && len(seen) < 80 {
		currentURL := queue[0]
		queue = queue[1:]
		if seen[currentURL] {
			continue
		}
		seen[currentURL] = true
		body, err := c.fetchText(ctx, currentURL)
		if err != nil {
			continue
		}
		if path := extractCheckinPath(body); path != "" {
			return strings.ReplaceAll(path, `\u0026`, "&"), nil
		}
		for _, assetURL := range scriptSources(body, currentURL) {
			if !seen[assetURL] {
				queue = append(queue, assetURL)
			}
		}
	}
	return "", fmt.Errorf("未发现聚影签到路径")
}

func (c *client) fetchText(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	c.setBrowserHeaders(req)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/javascript,text/javascript,*/*")
	c.applyAuthHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *client) mergeProfile(ctx context.Context, result *domain.SignInResult) {
	for _, endpoint := range []string{"/api/app/profile/", "/api/app/user/profile/"} {
		body, status, err := c.request(ctx, http.MethodGet, endpoint, nil)
		if err == nil && status < 400 {
			mergeJuYingFields(result, body)
			break
		}
	}
	for _, endpoint := range []string{"/api/app/checkin/stats/", "/api/app/checkin/stat/"} {
		body, status, err := c.request(ctx, http.MethodGet, endpoint, nil)
		if err == nil && status < 400 {
			mergeJuYingFields(result, body)
			break
		}
	}
}

func parseJuYingResult(raw []byte, status int) domain.SignInResult {
	result := domain.SignInResult{
		Success: status >= 200 && status < 300,
		Message: "签到请求已完成",
		Raw:     compactRaw(raw),
	}
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		result.Success = successFromMap(obj, result.Success)
		result.Message = messageFromMap(obj, result.Message)
		if isAlreadyCheckedIn(result.Message) {
			result.Success = true
		}
		src := unwrapData(obj)
		mergeJuYingMap(&result, src)
		return result
	}
	text := strings.TrimSpace(string(raw))
	if text != "" {
		result.Message = firstLine(text)
	}
	if status >= 400 {
		result.Success = false
	}
	return result
}

func mergeJuYingFields(result *domain.SignInResult, raw []byte) {
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return
	}
	mergeJuYingMap(result, unwrapData(obj))
}

func mergeJuYingMap(result *domain.SignInResult, src map[string]any) {
	if result.Username == "" {
		result.Username = stringValue(src, "username", "userName", "name")
	}
	if result.Email == "" {
		result.Email = stringValue(src, "email")
	}
	if result.SigninDays == 0 {
		result.SigninDays = intValue(src, "signin_days", "signinDays", "checkin_days", "continuous_days", "days")
	}
	if result.RewardPoints == 0 {
		result.RewardPoints = intValue(src, "reward_points", "rewardPoints", "checkin_points", "points_delta", "points")
	}
	if result.TotalPoints == 0 {
		result.TotalPoints = intValue(src, "total_points", "totalPoints", "integral", "score", "balance")
	}
	for _, key := range []string{"user", "profile", "stats", "checkin"} {
		if child, ok := src[key].(map[string]any); ok {
			mergeJuYingMap(result, child)
		}
	}
}

func authMode(account domain.Account) string {
	cred := account.Credential
	if account.Options.AuthMode != "" {
		return account.Options.AuthMode
	}
	switch {
	case cred.Username != "" && cred.Password != "":
		return "账号密码"
	case cred.Cookie != "":
		return "Cookie"
	case cred.SessionID != "":
		return "sessionid"
	case cred.AppID != "" && cred.APIKey != "":
		return "AppID/API Key"
	default:
		return "未知"
	}
}

func authFailed(status int, body []byte) bool {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	text := strings.ToLower(string(body))
	return containsAny(text, []string{"未登录", "登录已过期", "unauthorized", "forbidden", "invalid token", "csrf failed"})
}

func likelySuccess(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return successFromMap(obj, true)
	}
	text := strings.ToLower(string(raw))
	return !containsAny(text, []string{"失败", "错误", "error", "failed", "unauthorized"})
}

func extractLoginToken(raw []byte) string {
	if token := extractTokenByKeys(raw, "token", "user_token", "app_user_token"); token != "" {
		return token
	}
	return extractTokenByKeys(raw, "access", "access_token", "accessToken", "key")
}

func extractTokenByKeys(raw []byte, keys ...string) string {
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return searchString(obj, keys...)
}

func searchString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringValue(obj, key); v != "" {
			return v
		}
	}
	for _, v := range obj {
		if child, ok := v.(map[string]any); ok {
			if found := searchString(child, keys...); found != "" {
				return found
			}
		}
	}
	return ""
}

func extractCookieValue(raw, name string) string {
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func scriptSources(html string, baseURL string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)<(?:script|link)[^>]+(?:src|href)=["']([^"']+\.js(?:\?[^"']*)?)["']`),
		regexp.MustCompile(`(?i)["']((?:\./)?(?:assets/)?[^"']*(?:checkin|signin)[^"']*\.js(?:\?[^"']*)?)["']`),
	}
	var out []string
	base, err := url.Parse(baseURL)
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(html, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			resolved := resolveScriptURL(base, strings.ReplaceAll(m[1], `\u0026`, "&"))
			if resolved != "" && !seen[resolved] {
				seen[resolved] = true
				out = append(out, resolved)
			}
		}
	}
	return out
}

func resolveScriptURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if base == nil || ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "assets/") && strings.Contains(base.Path, "/assets/") {
		resolved := *base
		resolved.RawQuery = ""
		resolved.Fragment = ""
		prefix := base.Path[:strings.Index(base.Path, "/assets/")]
		resolved.Path = strings.TrimRight(prefix, "/") + "/" + ref
		return resolved.String()
	}
	parsedRef, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return base.ResolveReference(parsedRef).String()
}

func extractCheckinPath(source string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile("doCheckin\\s*[:=]\\s*(?:async\\s*)?(?:\\([^)]*\\)|[A-Za-z_$][\\w$]*)?\\s*=>\\s*[^;}{]{0,500}?\\.(?:post|get)\\(\\s*[\"'`]([^\"'`]+)[\"'`]"),
		regexp.MustCompile("doCheckin\\s*\\([^)]*\\)\\s*\\{\\s*return\\s+[^;}{]{0,500}?\\.(?:post|get)\\(\\s*[\"'`]([^\"'`]+)[\"'`]"),
		regexp.MustCompile("doCheckin\\s*[:=]\\s*function[^\\{]*\\{\\s*return\\s+[^;}{]{0,500}?\\.(?:post|get)\\(\\s*[\"'`]([^\"'`]+)[\"'`]"),
		regexp.MustCompile("[\"'`]((?:https?://[^\"'`]+)?/api/[^\"'`]*checkin[^\"'`]*)[\"'`]"),
	}
	for _, pattern := range patterns {
		matches := pattern.FindAllStringSubmatch(source, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			path := strings.TrimSpace(match[1])
			if path != "" && strings.Contains(strings.ToLower(path), "checkin") {
				return path
			}
		}
	}
	return ""
}

func unwrapData(obj map[string]any) map[string]any {
	for _, key := range []string{"data", "result"} {
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
	if isAlreadyCheckedIn(msg) {
		return true
	}
	if containsAny(msg, []string{"成功", "已签到", "already", "success"}) {
		return true
	}
	if containsAny(msg, []string{"失败", "错误", "error", "failed", "未登录"}) {
		return false
	}
	return fallback
}

func isAlreadyCheckedIn(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "已签到") ||
		strings.Contains(message, "已经签到") ||
		strings.Contains(message, "already checked") ||
		strings.Contains(message, "already signed")
}

func messageFromBody(raw []byte, fallback string) string {
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return messageFromMap(obj, fallback)
	}
	if text := firstLine(string(raw)); text != "" {
		return text
	}
	return fallback
}

func messageFromMap(obj map[string]any, fallback string) string {
	if msg := stringValue(obj, "message", "msg", "detail", "error", "reason"); msg != "" {
		return msg
	}
	if data, ok := obj["data"].(string); ok && strings.TrimSpace(data) != "" {
		return strings.TrimSpace(data)
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

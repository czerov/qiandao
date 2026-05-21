package hdhive

import (
	"bytes"
	"context"
	"encoding/base64"
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

const (
	hdhiveBrowserUA             = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"
	hdhiveLoginActionFallback   = "60580c09f61d392c3d56ffd69ac1a901df5c7f03a4"
	hdhiveLegacyLoginActionID   = "603b753f736d128b24c8b4f894057aa301eda77339"
	hdhiveCheckinActionFallback = "40efbc107064215e9eff178b0466274739ba7d9cb4"
	hdhiveDugouActionFallback   = "f30185aabded8ca281fe911b11b1dbdba999fa1f"
	hdhiveLoginRouterStateTree  = `%5B%22%22%2C%7B%22children%22%3A%5B%22(auth)%22%2C%7B%22children%22%3A%5B%22login%22%2C%7B%22children%22%3A%5B%22__PAGE__%22%2C%7B%7D%2Cnull%2Cnull%5D%7D%5D%7D%5D%7D%5D`
	hdhiveAppRouterStateTree    = `%5B%22%22%2C%7B%22children%22%3A%5B%22(app)%22%2C%7B%22children%22%3A%5B%22__PAGE__%22%2C%7B%7D%2C%22%2F%22%2C%22refresh%22%5D%7D%5D%7D%2Cnull%2Cnull%2Ctrue%5D`
	hdhiveMaxActionChunkFetches = 80
)

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Platform() string {
	return domain.PlatformHDHive
}

func (p *Provider) SignIn(ctx context.Context, account domain.Account, settings domain.Settings) (provider.Result, error) {
	settings = settings.WithDefaults()
	baseURL := resolveHDHiveSignBaseURL(settings.Providers.HDHive.BaseURL)
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
	client, err := httpx.NewClient(settings.SignInProxyURL(), 30*time.Second)
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
	client, err := httpx.NewClient(settings.SignInProxyURL(), 35*time.Second)
	if err != nil {
		return provider.Result{}, err
	}
	base, _ := url.Parse(baseURL)
	var patch *domain.AccountCredential
	if strings.TrimSpace(account.Credential.Cookie) != "" {
		setCookieHeader(client, base, account.Credential.Cookie)
	} else {
		if account.Credential.Username == "" || account.Credential.Password == "" {
			return provider.Result{}, fmt.Errorf("影巢网页登录需要账号密码或有效 Cookie")
		}
		if err := login(ctx, client, baseURL, account.Credential.Username, account.Credential.Password); err != nil {
			return provider.Result{}, err
		}
		patch = credentialPatch(account, client, base)
	}
	body, status, err := webCheckIn(ctx, client, baseURL, account.Options.Dog)
	if err != nil && len(body) == 0 {
		return provider.Result{CredentialPatch: patch}, err
	}
	if patch == nil {
		patch = credentialPatch(account, client, base)
	}
	pr := buildHDHiveWebResult(account, patch, body, status, account.Options.Dog)
	result := pr.SignInResult
	if !result.Success {
		if err != nil && strings.TrimSpace(result.Message) == "" {
			result.Message = err.Error()
			pr.SignInResult = result
		}
		return pr, fmt.Errorf("%s", result.Message)
	}
	return pr, nil
}

func buildHDHiveWebResult(account domain.Account, patch *domain.AccountCredential, body []byte, status int, dog bool) provider.Result {
	result := parseHDHiveResult(body, status)
	result.Platform = domain.PlatformHDHive
	result.AccountID = account.ID
	result.AccountName = account.DisplayName()
	mergeHDHiveWebResult(&result, body, dog)
	if dog {
		result.Mode = "赌狗抽签"
	} else {
		result.Mode = "网页签到"
	}
	return provider.Result{SignInResult: result, CredentialPatch: patch}
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
	req.Header.Set("User-Agent", hdhiveBrowserUA)
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
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return fmt.Errorf("影巢网页登录需要账号密码")
	}
	if password == domain.MaskValue {
		return fmt.Errorf("影巢账号密码仍是掩码，请重新输入密码并保存")
	}
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	_, _ = fetchHDHiveText(ctx, client, baseURL, "/", "")
	loginPageBody, _ := fetchHDHiveText(ctx, client, baseURL, "/login", baseURL)
	originalCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() {
		client.CheckRedirect = originalCheckRedirect
	}()
	var meaningfulErr error
	var actionErr error
	if err := tryHdhiveLoginAction(ctx, client, baseURL, loginPageBody, username, password); err == nil {
		return nil
	} else {
		actionErr = err
		if isMeaningfulLoginError(err) {
			meaningfulErr = err
		}
	}
	jsonPayloads := []map[string]string{
		{"username": username, "password": password},
		{"email": username, "password": password},
		{"name": username, "password": password},
	}
	var lastErr error
	var lastBody []byte
	var lastStatus int
	for _, endpoint := range []string{"/api/login", "/api/auth/login", "/api/user/login"} {
		for _, payload := range jsonPayloads {
			body, status, err := doJSONRequest(ctx, client, http.MethodPost, httpx.JoinURL(baseURL, endpoint), payload, nil)
			if err == nil && status < 400 && likelySuccess(body) {
				return nil
			}
			rememberAttempt(&lastBody, &lastStatus, &lastErr, body, status, err)
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
		rememberAttempt(&lastBody, &lastStatus, &lastErr, body, status, err)
	}
	if err := tryNextAction(ctx, client, baseURL, []string{"login", "signIn"}, hdhiveLoginActionPayload(username, password), "/login", loginPageBody); err == nil {
		return nil
	} else {
		if actionErr == nil {
			actionErr = err
		}
		if meaningfulErr == nil && isMeaningfulLoginError(err) {
			meaningfulErr = err
		}
	}
	if meaningfulErr != nil {
		return fmt.Errorf("影巢网页登录失败: %w", meaningfulErr)
	}
	if len(lastBody) > 0 {
		msg := messageFromBody(lastBody, "影巢网页登录失败")
		if lastStatus > 0 {
			return fmt.Errorf("%s (HTTP %d)", msg, lastStatus)
		}
		return fmt.Errorf("%s", msg)
	}
	if actionErr != nil {
		return fmt.Errorf("影巢网页登录失败: %w", actionErr)
	}
	if lastErr != nil {
		return fmt.Errorf("影巢网页登录失败: %w", lastErr)
	}
	return fmt.Errorf("影巢网页登录失败，请检查账号密码、Cookie 或站点是否需要额外验证")
}

func tryHdhiveLoginAction(ctx context.Context, client *http.Client, baseURL, pageBody, username, password string) error {
	payload := hdhiveLoginActionPayload(username, password)
	body, status, err := postNextAction(ctx, client, baseURL, []string{"login"}, payload, "/login", pageBody)
	if err != nil {
		return err
	}
	if status >= 400 || !likelySuccess(body) {
		return fmt.Errorf("%s (HTTP %d)", messageFromBody(body, "影巢登录 action 返回失败"), status)
	}
	if authCookieString(client, baseURL) == "" {
		msg := messageFromBody(body, "auth cookies not found")
		return fmt.Errorf("%s (HTTP %d)", msg, status)
	}
	return nil
}

func hdhiveLoginActionPayload(username, password string) string {
	return nextActionPayload(map[string]string{
		"username":           username,
		"password":           base64.StdEncoding.EncodeToString([]byte(password)),
		"password_transport": "base64",
	}, "/")
}

func webCheckIn(ctx context.Context, client *http.Client, baseURL string, dog bool) ([]byte, int, error) {
	if !dog {
		body, status, err := hdhiveCustomerCheckIn(ctx, client, baseURL)
		if err == nil {
			return body, status, nil
		}
		if len(body) > 0 && status >= 200 && status < 500 && !looksLikeNotFound(status, body) {
			return body, status, err
		}
	}
	payload := map[string]bool{"dog": dog}
	for _, endpoint := range []string{"/api/customer/user/checkin", "/api/checkin", "/api/user/checkin", "/api/attendance/checkin", "/api/signin"} {
		body, status, err := doJSONRequest(ctx, client, http.MethodPost, httpx.JoinURL(baseURL, endpoint), payload, nil)
		if err == nil && status < 500 && !looksLikeNotFound(status, body) {
			return body, status, nil
		}
	}
	actionPayload := "[false]"
	if dog {
		actionPayload = "[true]"
	}
	body, status, err := postNextAction(ctx, client, baseURL, []string{"checkIn", "checkin", "signIn", "signin"}, actionPayload, "/", "")
	if err == nil {
		return body, status, nil
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusGone {
		err = fmt.Errorf("影巢签到失败 (HTTP %d: Action 已失效或站点路由已变更)", status)
	}
	if dog {
		body, status, dogErr := postNextAction(ctx, client, baseURL, []string{"checkIn"}, "[null]", "/", "", hdhiveDugouActionFallback)
		if dogErr == nil {
			return body, status, nil
		}
	}
	return nil, 0, err
}

func hdhiveCustomerCheckIn(ctx context.Context, client *http.Client, baseURL string) ([]byte, int, error) {
	body, status, err := doJSONRequest(ctx, client, http.MethodPost, httpx.JoinURL(baseURL, "/api/customer/user/checkin"), map[string]any{}, nil)
	if err != nil {
		return nil, 0, err
	}
	if status >= 200 && status < 300 {
		return body, status, nil
	}
	return body, status, fmt.Errorf("%s (HTTP %d)", messageFromBody(body, "影巢网页登录签到失败"), status)
}

func doFormRequest(ctx context.Context, client *http.Client, rawURL string, form url.Values, decorate func(*http.Request)) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", hdhiveBrowserUA)
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

func postNextAction(ctx context.Context, client *http.Client, baseURL string, names []string, payload string, pagePath string, pageBody string, fallbacks ...string) ([]byte, int, error) {
	ids := actionIDCandidates(ctx, client, baseURL, names, pagePath, pageBody, fallbacks...)
	if len(ids) == 0 {
		return nil, 0, fmt.Errorf("未发现 Next.js Server Action ID")
	}
	var lastBody []byte
	var lastStatus int
	var lastErr error
	for _, endpoint := range actionEndpoints(names) {
		for _, id := range ids {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpx.JoinURL(baseURL, endpoint), strings.NewReader(payload))
			if err != nil {
				return nil, 0, err
			}
			setNextActionHeaders(req, baseURL, endpoint, id, names)
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
				continue
			}
			lastBody, lastStatus = raw, resp.StatusCode
			if resp.StatusCode < 500 && !looksLikeNotFound(resp.StatusCode, raw) {
				return raw, resp.StatusCode, nil
			}
		}
	}
	if len(lastBody) > 0 {
		if looksLikeNotFound(lastStatus, lastBody) {
			return nil, lastStatus, fmt.Errorf("影巢 Server Action 调用失败 (HTTP %d: Action 已失效或站点路由已变更)", lastStatus)
		}
		return lastBody, lastStatus, fmt.Errorf("%s (HTTP %d)", messageFromBody(lastBody, "影巢 Server Action 调用失败"), lastStatus)
	}
	if lastErr != nil {
		return nil, 0, lastErr
	}
	return nil, 0, fmt.Errorf("影巢 Server Action 调用失败")
}

func tryNextAction(ctx context.Context, client *http.Client, baseURL string, names []string, payload string, pagePath string, pageBody string, fallbacks ...string) error {
	body, status, err := postNextAction(ctx, client, baseURL, names, payload, pagePath, pageBody, fallbacks...)
	if err != nil {
		return err
	}
	if status >= 400 || !likelySuccess(body) {
		return fmt.Errorf("%s (HTTP %d)", messageFromBody(body, "Server Action 返回失败"), status)
	}
	return nil
}

func setNextActionHeaders(req *http.Request, baseURL, endpoint, actionID string, names []string) {
	req.Header.Set("User-Agent", hdhiveBrowserUA)
	req.Header.Set("Accept", "text/x-component")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Next-Action", actionID)
	req.Header.Set("Next-Url", endpoint)
	req.Header.Set("Origin", strings.TrimRight(baseURL, "/"))
	req.Header.Set("Referer", httpx.JoinURL(baseURL, endpoint))
	req.Header.Set("Priority", "u=1, i")
	req.Header.Set("Sec-CH-UA", `"Google Chrome";v="147", "Not=A?Brand";v="8", "Chromium";v="147"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("x-nextjs-post", "1")
	if isLoginAction(names) {
		req.Header.Set("Next-Router-State-Tree", hdhiveLoginRouterStateTree)
		req.Header.Set("DNT", "1")
		return
	}
	req.Header.Set("Next-Router-State-Tree", hdhiveAppRouterStateTree)
}

func rememberAttempt(lastBody *[]byte, lastStatus *int, lastErr *error, body []byte, status int, err error) {
	if err != nil {
		*lastErr = err
		return
	}
	if len(body) == 0 {
		return
	}
	if looksLikeNotFound(status, body) {
		return
	}
	msg := messageFromBody(body, "")
	if msg == "" {
		msg = firstLine(string(body))
	}
	if msg != "" {
		*lastBody = body
		*lastStatus = status
	}
}

func isMeaningfulLoginError(err error) bool {
	if err == nil {
		return false
	}
	return containsAny(err.Error(), []string{
		"用户名", "账号", "邮箱", "密码", "验证", "验证码", "过期", "禁用", "频繁",
		"401", "403", "too many", "unauthorized", "forbidden",
	})
}

func actionIDCandidates(ctx context.Context, client *http.Client, baseURL string, names []string, pagePath string, pageBody string, fallbacks ...string) []string {
	var out []string
	if id, err := discoverActionID(ctx, client, baseURL, names, pagePath, pageBody); err == nil && id != "" {
		out = appendUniqueString(out, id)
	}
	if isLoginAction(names) {
		out = appendUniqueString(out, hdhiveLoginActionFallback)
		out = appendUniqueString(out, hdhiveLegacyLoginActionID)
	} else {
		for _, name := range names {
			if strings.Contains(strings.ToLower(name), "checkin") || strings.Contains(strings.ToLower(name), "signin") {
				out = appendUniqueString(out, hdhiveCheckinActionFallback)
				break
			}
		}
	}
	for _, fallback := range fallbacks {
		out = appendUniqueString(out, fallback)
	}
	return out
}

func discoverActionID(ctx context.Context, client *http.Client, baseURL string, names []string, pagePath string, pageBody string) (string, error) {
	var blobs []string
	var scripts []string
	if pageBody != "" {
		blobs = append(blobs, pageBody)
		scripts = append(scripts, scriptSources(pageBody)...)
	}
	pages := []string{"/", "/login", "/user", "/dashboard"}
	if strings.TrimSpace(pagePath) != "" {
		pages = append([]string{pagePath}, pages...)
	}
	seenPages := map[string]bool{}
	for _, page := range pages {
		if seenPages[page] {
			continue
		}
		seenPages[page] = true
		body, err := fetchHDHiveText(ctx, client, baseURL, page, "")
		if err == nil {
			blobs = append(blobs, body)
			scripts = append(scripts, scriptSources(body)...)
		}
	}
	seen := map[string]bool{}
	for _, src := range scripts {
		if seen[src] || len(seen) > hdhiveMaxActionChunkFetches {
			continue
		}
		seen[src] = true
		body, err := fetchHDHiveText(ctx, client, baseURL, src, httpx.JoinURL(baseURL, nonEmptyPath(pagePath, "/")))
		if err == nil {
			blobs = append(blobs, body)
		}
	}
	for _, blob := range blobs {
		if id := findActionID(blob, names); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("未发现 Next.js Server Action ID")
}

func fetchHDHiveText(ctx context.Context, client *http.Client, baseURL string, pathOrURL string, referer string) (string, error) {
	targetURL := joinURL(baseURL, pathOrURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	setHDHiveFetchHeaders(req, pathOrURL, referer)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func setHDHiveFetchHeaders(req *http.Request, pathOrURL string, referer string) {
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-CH-UA", `"Google Chrome";v="147", "Not=A?Brand";v="8", "Chromium";v="147"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	if strings.HasSuffix(strings.ToLower(pathOrURL), ".js") {
		req.Header.Set("Sec-Fetch-Dest", "script")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	} else {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Upgrade-Insecure-Requests", "1")
	}
	req.Header.Set("User-Agent", hdhiveBrowserUA)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
}

func scriptSources(html string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)<(?:script|link)[^>]+(?:src|href)=["']([^"']+\.js(?:\?[^"']*)?)["']`),
		regexp.MustCompile(`(?i)["']((?:\./)?(?:assets/)?[^"']*(?:checkin|signin|login)[^"']*\.js(?:\?[^"']*)?)["']`),
		regexp.MustCompile(`(?i)(/_next/static/chunks/[^"'\\\s<>]+\.js(?:\?[^"'\\\s<>]*)?)`),
	}
	var out []string
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(html, -1)
		for _, m := range matches {
			if len(m) > 1 {
				out = appendUniqueString(out, strings.ReplaceAll(m[1], `\u0026`, "&"))
			}
		}
	}
	return out
}

func findActionID(blob string, names []string) string {
	for _, name := range names {
		quoted := regexp.QuoteMeta(name)
		patterns := []string{
			`(?is)createServerReference\)?\(["']([a-f0-9]{32,80})["'][^)]{0,500}["']` + quoted + `["']\)?`,
			`(?is)["']([a-f0-9]{32,80})["'][^)]{0,500}findSourceMapURL,["']` + quoted + `["']\)?`,
			`(?is)createServerReference\(["']([a-f0-9]{32,80})["'][^)]*["']` + quoted + `["']`,
			`(?is)` + quoted + `.{0,400}?["']([a-f0-9]{32,80})["']`,
			`(?is)["']([a-f0-9]{32,80})["'].{0,400}?` + quoted,
		}
		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			if m := re.FindStringSubmatch(blob); len(m) > 1 {
				return m[1]
			}
		}
		needle := `"` + name + `"`
		searchFrom := 0
		idRe := regexp.MustCompile(`[a-f0-9]{32,80}`)
		for {
			relativeIdx := strings.Index(blob[searchFrom:], needle)
			if relativeIdx < 0 {
				break
			}
			idx := searchFrom + relativeIdx
			start := idx - 700
			if start < 0 {
				start = 0
			}
			end := idx + len(needle) + 120
			if end > len(blob) {
				end = len(blob)
			}
			context := blob[start:end]
			if strings.Contains(context, "createServerReference") {
				ids := idRe.FindAllString(blob[start:idx], -1)
				if len(ids) > 0 {
					return ids[len(ids)-1]
				}
			}
			searchFrom = idx + len(needle)
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

func mergeHDHiveWebResult(result *domain.SignInResult, raw []byte, dog bool) {
	body := unescapeUnicode(string(raw))
	if strings.TrimSpace(body) == "" {
		return
	}
	if msg := parseHDHiveWebCheckinMessage(body, dog); msg != "" {
		result.Message = msg
	}
	if containsAny(body, []string{"已经签到", "今日已参与", "already signed", "already checked"}) {
		result.Success = true
	}
	if result.RewardPoints == 0 {
		result.RewardPoints = intFromString(findValEnhanced(body, []string{
			`(?i)"gained_points":\s*([+-]?\d+)`,
			`(?i)"gained":\s*([+-]?\d+)`,
			`(?i)"points_delta":\s*([+-]?\d+)`,
			`签到成功[^"<>]{0,80}?获得\s*([+-]?\d+)\s*积分`,
			`签到[^"<>]{0,120}?获得\s*([+-]?\d+)\s*积分`,
			`获得积分\s*[:：]?\s*([+-]?\d+)`,
			`获得\s*([+-]?\d+)\s*积分`,
		}))
	}
	if result.TotalPoints == 0 {
		result.TotalPoints = intFromString(findValEnhanced(body, []string{
			`(?i)"user_meta":\s*\{[^{}]*"points":\s*(-?\d+)`,
			`(?i)"total_points":\s*(-?\d+)`,
			`(?i)"points":\s*(\d{4,})`,
			`(?s)积分.*?>(\d+)`,
		}))
	}
	if result.SigninDays == 0 {
		result.SigninDays = intFromString(findValEnhanced(body, []string{
			`(?i)"signin_days_total":\s*(\d+)`,
			`(?i)"signin_days":\s*(\d+)`,
			`(?i)"days":\s*(\d+)`,
			`签到天数.*?(\d+)`,
			`连续签到.*?(\d+)`,
		}))
	}
	if result.Nickname == "" {
		result.Nickname = findValEnhanced(body, []string{
			`class="nickname"[^>]*>([^<]+)`,
			`Hi,\s*([^<!\s]+)`,
			`"nickname":"([^"]+)"`,
			`"first_name":"([^"]+)"`,
			`"full_name":"([^"]+)"`,
			`昵称[:\s]{1,10}([^<!\s]+)`,
		})
	}
	if containsAny(body, []string{"\"success\":false", "失败", "错误", "error", "failed"}) && !containsAny(body, []string{"已经签到", "今日已参与", "already signed"}) {
		result.Success = false
	}
}

func parseHDHiveWebCheckinMessage(body string, dog bool) string {
	explicitMsg := findValEnhanced(body, []string{
		`"description":"([^"]+)"`,
		`"message":"([^"]+)"`,
		`"error":"([^"]+)"`,
	})
	switch {
	case strings.Contains(body, "已经签到") || strings.Contains(body, "already signed") || strings.Contains(body, "签到过了"):
		if dog {
			return "赌狗抽签: 今日已参与"
		}
		return "今日已签到"
	case strings.Contains(body, "不要贪心") || strings.Contains(body, "今日已参与") || strings.Contains(body, "已经参与"):
		return "赌狗抽签: 今日已参与"
	case strings.Contains(body, `"success":true`) || strings.Contains(body, `"points":`) || strings.Contains(body, "签到成功") || strings.Contains(body, "成功"):
		if explicitMsg != "" {
			if dog && !strings.Contains(explicitMsg, "赌狗") {
				return "赌狗抽签: " + explicitMsg
			}
			return explicitMsg
		}
		if dog {
			return "赌狗抽签: 成功获得奖励"
		}
		return "签到成功"
	default:
		if explicitMsg != "" {
			return explicitMsg
		}
		return ""
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
	if v, ok := obj["error"]; ok {
		switch x := v.(type) {
		case nil:
		case bool:
			if x {
				return false
			}
		case string:
			if strings.TrimSpace(x) != "" {
				return false
			}
		case map[string]any:
			if len(x) > 0 {
				return false
			}
		default:
			return false
		}
	}
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
	for _, key := range []string{"error", "data", "result"} {
		if child, ok := obj[key].(map[string]any); ok {
			if msg := messageFromMap(child, ""); msg != "" {
				return msg
			}
		}
	}
	if data, ok := obj["data"].(string); ok && data != "" {
		return data
	}
	return fallback
}

func messageFromBody(raw []byte, fallback string) string {
	if len(raw) == 0 {
		return fallback
	}
	if isHTMLDocument(raw) {
		if strings.TrimSpace(fallback) != "" {
			return fallback
		}
		return "接口返回 HTML 页面"
	}
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return messageFromMap(obj, fallback)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.IndexByte(line, ':'); idx > 0 && looksLikeFramePrefix(line[:idx]) {
			line = strings.TrimSpace(line[idx+1:])
		}
		obj := map[string]any{}
		if err := json.Unmarshal([]byte(line), &obj); err == nil {
			if msg := messageFromMap(obj, ""); msg != "" {
				return msg
			}
		}
	}
	if msg := firstLine(string(raw)); msg != "" {
		return msg
	}
	return fallback
}

func isHTMLDocument(raw []byte) bool {
	text := strings.TrimSpace(strings.ToLower(string(raw)))
	return strings.HasPrefix(text, "<!doctype html") || strings.HasPrefix(text, "<html")
}

func looksLikeFramePrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

func intFromString(raw string) int {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "+"))
	v, _ := strconv.Atoi(raw)
	return v
}

func findValEnhanced(source string, patterns []string) string {
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if m := re.FindStringSubmatch(source); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func likelySuccess(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	text := strings.ToLower(string(raw))
	if containsAny(text, []string{"\"success\":false", "\"ok\":false", "invalid", "unauthorized", "forbidden", "failed", "失败", "错误", "密码错误", "不正确", "不存在", "too many"}) {
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

func unescapeUnicode(s string) string {
	re := regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)
	s = re.ReplaceAllStringFunc(s, func(m string) string {
		r, _ := strconv.ParseInt(m[2:], 16, 32)
		return string(rune(r))
	})
	return strings.ReplaceAll(s, `\"`, `"`)
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

func actionEndpoints(names []string) []string {
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "login") {
			return []string{"/login", "/"}
		}
	}
	return []string{"/"}
}

func isLoginAction(names []string) bool {
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "login") {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func nonEmptyPath(path, fallback string) string {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	return path
}

func joinURL(baseURL, pathOrURL string) string {
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		return pathOrURL
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return httpx.JoinURL(baseURL, pathOrURL)
	}
	ref, err := url.Parse(pathOrURL)
	if err != nil {
		return httpx.JoinURL(baseURL, pathOrURL)
	}
	return base.ResolveReference(ref).String()
}

func nextActionPayload(args ...any) string {
	raw, _ := json.Marshal(args)
	return string(raw)
}

func hostCandidates(baseURL string) []string {
	baseURL = resolveHDHiveSignBaseURL(baseURL)
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = resolveHDHiveSignBaseURL(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	add(baseURL)
	return out
}

func resolveHDHiveSignBaseURL(raw string) string {
	raw = httpx.NormalizeBaseURL(raw, "https://hdhive.com")
	parsed, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(parsed.Hostname()) == "" {
		return "https://hdhive.com"
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if strings.HasSuffix(host, "hdhive.com") || strings.HasSuffix(host, "hdhive.org") || strings.HasSuffix(host, "hdhive.online") {
		return "https://hdhive.com"
	}
	return raw
}

func setCookieHeader(client *http.Client, base *url.URL, raw string) {
	if client.Jar == nil || base == nil {
		return
	}
	client.Jar.SetCookies(base, parseCookiePairs(raw))
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

func authCookieString(client *http.Client, baseURL string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return cookieString(client, base)
}

func parseCookiePairs(raw string) []*http.Cookie {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	attributeNames := map[string]struct{}{
		"path": {}, "domain": {}, "expires": {}, "max-age": {}, "samesite": {}, "priority": {}, "secure": {}, "httponly": {},
	}
	var cookies []*http.Cookie
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		if _, isAttr := attributeNames[strings.ToLower(name)]; isAttr {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	return cookies
}

func credentialPatch(account domain.Account, client *http.Client, base *url.URL) *domain.AccountCredential {
	cookie := cookieString(client, base)
	if cookie == "" || cookie == account.Credential.Cookie {
		return nil
	}
	next := account.Credential
	next.Cookie = cookie
	return &next
}

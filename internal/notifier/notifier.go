package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"qiandao/internal/domain"
	"qiandao/internal/httpx"
)

type Notifier struct{}

func New() *Notifier {
	return &Notifier{}
}

func (n *Notifier) Send(ctx context.Context, result domain.SignInResult, account domain.Account, settings domain.Settings) error {
	var errs []string
	if account.NotifyTG && strings.TrimSpace(settings.Notify.TelegramBotToken) != "" && strings.TrimSpace(settings.Notify.TelegramAdminID) != "" {
		if err := sendTelegram(ctx, settings, formatMessage(result)); err != nil {
			errs = append(errs, "Telegram: "+err.Error())
		}
	}
	if account.NotifyWebhook && strings.TrimSpace(settings.Notify.WebhookURL) != "" {
		if err := sendWebhook(ctx, settings.Notify.WebhookURL, result, formatMessage(result)); err != nil {
			errs = append(errs, "Webhook: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func sendTelegram(ctx context.Context, settings domain.Settings, text string) error {
	client, err := httpx.NewClient(settings.Notify.TelegramProxyURL, 20*time.Second)
	if err != nil {
		return err
	}
	endpoint := "https://api.telegram.org/bot" + url.PathEscape(settings.Notify.TelegramBotToken) + "/sendMessage"
	payload := map[string]any{
		"chat_id": settings.Notify.TelegramAdminID,
		"text":    text,
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func sendWebhook(ctx context.Context, webhookURL string, result domain.SignInResult, content string) error {
	client, err := httpx.NewClient("", 20*time.Second)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"event":         "signin_result",
		"platform":      result.Platform,
		"account_id":    result.AccountID,
		"account_name":  result.AccountName,
		"success":       result.Success,
		"message":       result.Message,
		"mode":          result.Mode,
		"username":      result.Username,
		"nickname":      result.Nickname,
		"email":         result.Email,
		"signin_days":   result.SigninDays,
		"reward_points": result.RewardPoints,
		"total_points":  result.TotalPoints,
		"content":       content,
		"timestamp":     result.FinishedAt.Format(time.RFC3339),
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func formatMessage(result domain.SignInResult) string {
	status := "失败"
	if result.Success {
		status = "成功"
	}
	platform := result.Platform
	switch result.Platform {
	case domain.PlatformHDHive:
		platform = "影巢"
	case domain.PlatformJuYing:
		platform = "聚影"
	}
	lines := []string{
		platform + "签到结果",
		"账号: " + result.AccountName,
		"状态: " + status,
		"模式: " + nonEmpty(result.Mode, "-"),
		"详情: " + nonEmpty(result.Message, "-"),
	}
	if result.Username != "" {
		lines = append(lines, "用户名: "+result.Username)
	}
	if result.Nickname != "" {
		lines = append(lines, "昵称: "+result.Nickname)
	}
	if result.SigninDays != 0 {
		lines = append(lines, fmt.Sprintf("签到天数: %d", result.SigninDays))
	}
	if result.RewardPoints != 0 {
		lines = append(lines, fmt.Sprintf("本次积分: %+d", result.RewardPoints))
	}
	if result.TotalPoints != 0 {
		lines = append(lines, fmt.Sprintf("累计积分: %d", result.TotalPoints))
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

package oneonefivecookie

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) GetQRCodeToken(ctx context.Context, app string) (*QRTokenData, error) {
	endpoint := fmt.Sprintf("%s/api/1.0/%s/1.0/token/", c.qrCodeBaseURL, url.PathEscape(app))
	var envelope APIEnvelope[QRTokenData]
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &envelope); err != nil {
		return nil, annotateNetworkFailure(err, endpoint)
	}
	if err := checkEnvelope(envelope, endpoint); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (c *Client) GetQRCodeImageDataURL(ctx context.Context, app, uid string) (string, error) {
	endpoint := fmt.Sprintf("%s/api/1.0/%s/1.0/qrcode?uid=%s", c.qrCodeBaseURL, url.PathEscape(app), url.QueryEscape(uid))
	body, contentType, err := c.doRaw(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", annotateNetworkFailure(err, endpoint)
	}
	if contentType == "" {
		contentType = "image/png"
	}
	if idx := strings.Index(contentType, ";"); idx > 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	return fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(body)), nil
}

func (c *Client) GetQRCodeStatus(ctx context.Context, uid string, issuedAt int64, sign string) (*QRStatusData, error) {
	query := url.Values{}
	query.Set("uid", uid)
	query.Set("time", strconv.FormatInt(issuedAt, 10))
	query.Set("sign", sign)
	endpoint := c.qrCodeBaseURL + "/get/status/?" + query.Encode()

	var envelope APIEnvelope[QRStatusData]
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &envelope); err != nil {
		return nil, annotateNetworkFailure(err, endpoint)
	}
	if err := checkEnvelope(envelope, endpoint); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (c *Client) GetLoginResult(ctx context.Context, app, uid string) (*LoginData, error) {
	spec := loginAppSpecFor(app)
	data, err := c.getLoginResult(ctx, c.qrCodeBaseURL, spec, uid)
	if err == nil {
		return data, nil
	}

	fallbackData, fallbackErr := c.getLoginResult(ctx, c.passportBaseURL, spec, uid)
	if fallbackErr == nil {
		return fallbackData, nil
	}
	return nil, fmt.Errorf("qrcodeapi login result: %w; passportapi fallback: %v", err, fallbackErr)
}

func (c *Client) getLoginResult(ctx context.Context, baseURL string, spec loginAppSpec, uid string) (*LoginData, error) {
	endpoint := fmt.Sprintf("%s/app/1.0/%s/1.0/login/qrcode/", baseURL, url.PathEscape(spec.App))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("account", uid); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	headers := textproto.MIMEHeader{}
	headers.Set("Content-Type", writer.FormDataContentType())
	if spec.UserAgent != "" {
		headers.Set("User-Agent", spec.UserAgent)
	}
	var envelope APIEnvelope[LoginData]
	if err := c.doJSON(ctx, http.MethodPost, endpoint, &requestBody{reader: &body, headers: headers}, &envelope); err != nil {
		return nil, annotateNetworkFailure(err, endpoint)
	}
	if err := checkEnvelope(envelope, endpoint); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

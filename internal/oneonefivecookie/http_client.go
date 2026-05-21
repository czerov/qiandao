package oneonefivecookie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"time"
)

type requestBody struct {
	reader  io.Reader
	headers textproto.MIMEHeader
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body *requestBody, dest any) error {
	raw, _, err := c.doRaw(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("empty 115 API response body")
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("decode 115 API response: %w", err)
	}
	return nil
}

func (c *Client) doRaw(ctx context.Context, method, endpoint string, body *requestBody) ([]byte, string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = body.reader
	}
	req, err := http.NewRequestWithContext(requestCtx, method, endpoint, reader)
	if err != nil {
		return nil, "", err
	}

	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		for key, values := range body.headers {
			for index, value := range values {
				if index == 0 {
					req.Header.Set(key, value)
				} else {
					req.Header.Add(key, value)
				}
			}
		}
	}

	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = NewDirectHTTPClient(30 * time.Second)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if isTimeoutError(err) {
			return nil, "", fmt.Errorf("request timeout: %w", err)
		}
		return nil, "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read 115 API response body: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, "", &APIError{
			URL:        endpoint,
			StatusCode: resp.StatusCode,
			Message:    "upstream returned non-success status",
			Body:       truncateBody(raw),
		}
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

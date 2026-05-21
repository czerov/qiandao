package oneonefivecookie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func NewService(cfg Config) (*Service, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:      normalized,
		client:   newClient(normalized),
		sessions: newSessionStore(),
	}, nil
}

func (s *Service) CreateSession(ctx context.Context, app string) (*SessionResponse, error) {
	session, err := s.createSession(ctx, app)
	if err != nil {
		return nil, err
	}

	resp := buildSessionResponse(session, true)
	return &resp, nil
}

func (s *Service) GetSession(id string) (SessionResponse, bool) {
	session, ok := s.sessions.Get(strings.TrimSpace(id))
	if !ok {
		return SessionResponse{}, false
	}
	return buildSessionResponse(session, false), true
}

func (s *Service) CancelAll() {
	s.sessions.CancelAll()
}

func (s *Service) createSession(ctx context.Context, app string) (*AuthSession, error) {
	app = normalizeApp(app)
	if app == "" {
		app = "alipaymini"
	}
	if !IsValidApp(app) {
		return nil, fmt.Errorf("unsupported 115 login app: %s", app)
	}

	token, err := s.client.GetQRCodeToken(ctx, app)
	if err != nil {
		return nil, fmt.Errorf("get 115 qrcode token: %w", err)
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}

	imageDataURL, err := s.client.GetQRCodeImageDataURL(ctx, app, token.UID)
	if err != nil {
		return nil, fmt.Errorf("get 115 qrcode image: %w", err)
	}

	now := time.Now()
	session := &AuthSession{
		ID:               generateSessionID(),
		App:              app,
		UID:              token.UID,
		RemoteIssuedAt:   token.Time,
		RemoteSign:       token.Sign,
		QRCodePNGDataURL: imageDataURL,
		Status:           sessionStatusPending,
		Message:          "二维码已生成，等待扫码",
		RawStatus:        0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	sessionCtx, cancel := context.WithTimeout(context.Background(), s.cfg.SessionTimeout)
	session.cancel = cancel
	s.sessions.Put(session)

	go s.pollAuthorization(sessionCtx, session.ID)
	return s.sessions.MustGet(session.ID), nil
}

func (s *Service) pollAuthorization(ctx context.Context, sessionID string) {
	defer func() {
		if session, ok := s.sessions.Get(sessionID); ok && session.cancel != nil {
			session.cancel()
		}
		if session, ok := s.sessions.Get(sessionID); ok && isTerminalStatus(session.Status) {
			s.sessions.RedactQRCode(sessionID)
			s.sessions.DeleteAfter(sessionID, s.cfg.SessionRetention)
		}
	}()

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		done := s.pollAuthorizationOnce(ctx, sessionID, &consecutiveErrors)
		if done {
			return
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.sessions.Update(sessionID, func(session *AuthSession) {
					if isTerminalStatus(session.Status) {
						return
					}
					now := time.Now()
					session.Status = sessionStatusExpired
					session.Message = "等待授权超时，请重新获取二维码"
					session.LastError = "cookie qrcode session timeout"
					session.UpdatedAt = now
					session.CompletedAt = &now
				})
			}
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) pollAuthorizationOnce(ctx context.Context, sessionID string, consecutiveErrors *int) bool {
	session, ok := s.sessions.Get(sessionID)
	if !ok || isTerminalStatus(session.Status) {
		return true
	}

	status, err := s.client.GetQRCodeStatus(ctx, session.UID, session.RemoteIssuedAt, session.RemoteSign)
	if err != nil {
		*consecutiveErrors++
		message := fmt.Sprintf("轮询 115 扫码状态失败（%d/3）", *consecutiveErrors)
		if isTimeoutError(err) {
			message = fmt.Sprintf("轮询 115 扫码状态超时（%d/3）", *consecutiveErrors)
		}

		if *consecutiveErrors >= 3 {
			s.sessions.Update(sessionID, func(current *AuthSession) {
				now := time.Now()
				current.Status = sessionStatusFailed
				current.Message = message
				current.LastError = err.Error()
				current.UpdatedAt = now
				current.CompletedAt = &now
			})
			return true
		}

		s.sessions.Update(sessionID, func(current *AuthSession) {
			current.Message = message
			current.LastError = err.Error()
			current.UpdatedAt = time.Now()
		})
		return false
	}

	*consecutiveErrors = 0
	mappedStatus, mappedMessage := mapQRStatus(status.Status)
	switch mappedStatus {
	case sessionStatusPending, sessionStatusScanned:
		s.sessions.Update(sessionID, func(current *AuthSession) {
			current.Status = mappedStatus
			current.Message = mappedMessage
			current.RawStatus = status.Status
			current.LastError = ""
			current.UpdatedAt = time.Now()
		})
		return false
	case sessionStatusCanceled, sessionStatusExpired:
		s.sessions.Update(sessionID, func(current *AuthSession) {
			now := time.Now()
			current.Status = mappedStatus
			current.Message = mappedMessage
			current.RawStatus = status.Status
			current.LastError = ""
			current.UpdatedAt = now
			current.CompletedAt = &now
		})
		return true
	case sessionStatusAuthorized:
		loginData, err := s.client.GetLoginResult(ctx, session.App, session.UID)
		if err != nil {
			s.sessions.Update(sessionID, func(current *AuthSession) {
				now := time.Now()
				current.Status = sessionStatusFailed
				current.Message = "扫码成功，但获取 Cookie 失败"
				current.LastError = err.Error()
				current.UpdatedAt = now
				current.CompletedAt = &now
			})
			return true
		}
		if err := loginData.Cookie.Validate(); err != nil {
			s.sessions.Update(sessionID, func(current *AuthSession) {
				now := time.Now()
				current.Status = sessionStatusFailed
				current.Message = "扫码成功，但 115 返回的 Cookie 不完整"
				current.LastError = err.Error()
				current.UpdatedAt = now
				current.CompletedAt = &now
			})
			return true
		}

		cookieText := FormatCookieText(loginData.Cookie)
		cookieJSON := FormatCookieJSON(loginData.Cookie)
		s.sessions.Update(sessionID, func(current *AuthSession) {
			now := time.Now()
			current.Status = sessionStatusAuthorized
			current.Message = "扫码成功，CK 已生成"
			current.RawStatus = status.Status
			current.Cookie = &loginData.Cookie
			current.CookieText = cookieText
			current.CookieJSON = cookieJSON
			current.LastError = ""
			current.UpdatedAt = now
			current.CompletedAt = &now
		})
		return true
	default:
		return false
	}
}

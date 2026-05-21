package oneonefivecookie

import (
	"fmt"
	"time"
)

func newSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*AuthSession),
	}
}

func (s *SessionStore) Put(session *AuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
}

func (s *SessionStore) Get(id string) (*AuthSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	return cloneSession(session), true
}

func (s *SessionStore) MustGet(id string) *AuthSession {
	session, _ := s.Get(id)
	return session
}

func (s *SessionStore) Update(id string, fn func(*AuthSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return
	}
	fn(session)
}

func (s *SessionStore) RedactQRCode(id string) {
	s.Update(id, func(session *AuthSession) {
		session.RemoteSign = ""
		session.QRCodePNGDataURL = ""
	})
}

func (s *SessionStore) DeleteAfter(id string, delay time.Duration) {
	if delay <= 0 {
		s.Delete(id)
		return
	}
	time.AfterFunc(delay, func() {
		s.Delete(id)
	})
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *SessionStore) CancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, session := range s.sessions {
		if session.cancel != nil {
			session.cancel()
		}
		delete(s.sessions, id)
	}
}

func cloneSession(session *AuthSession) *AuthSession {
	if session == nil {
		return nil
	}
	clone := *session
	if session.Cookie != nil {
		cookie := *session.Cookie
		clone.Cookie = &cookie
	}
	if session.CookieJSON != nil {
		clone.CookieJSON = append([]CookieEntry(nil), session.CookieJSON...)
	}
	return &clone
}

func buildSessionResponse(session *AuthSession, includeQRCode bool) SessionResponse {
	resp := SessionResponse{
		ID:          session.ID,
		App:         session.App,
		UID:         session.UID,
		Status:      session.Status,
		Message:     session.Message,
		RawStatus:   session.RawStatus,
		CookieText:  session.CookieText,
		CookieJSON:  append([]CookieEntry(nil), session.CookieJSON...),
		CreatedAt:   session.CreatedAt,
		UpdatedAt:   session.UpdatedAt,
		CompletedAt: session.CompletedAt,
		LastError:   session.LastError,
	}
	if includeQRCode {
		resp.QRCodePNGDataURL = session.QRCodePNGDataURL
	}
	return resp
}

func mapQRStatus(status int) (string, string) {
	switch status {
	case 0:
		return sessionStatusPending, "等待扫码"
	case 1:
		return sessionStatusScanned, "已扫码，请在 115 App 中确认"
	case 2:
		return sessionStatusAuthorized, "登录成功，正在获取 CK"
	case -1:
		return sessionStatusExpired, "二维码已失效，请重新获取"
	case -2:
		return sessionStatusCanceled, "扫码登录已取消"
	default:
		return sessionStatusPending, fmt.Sprintf("等待扫码确认（status=%d）", status)
	}
}

func isTerminalStatus(status string) bool {
	switch status {
	case sessionStatusAuthorized, sessionStatusCanceled, sessionStatusExpired, sessionStatusFailed:
		return true
	default:
		return false
	}
}

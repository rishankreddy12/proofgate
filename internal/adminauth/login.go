package adminauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// CheckLoginLockout checks if a username is currently locked out due to excessive failed attempts.
func (s *Service) CheckLoginLockout(ctx context.Context, username string) (bool, time.Duration, error) {
	key := loginFailKey(username)
	attempts, ttl, err := s.sessions.GetWithTTL(ctx, key)
	if err != nil {
		return false, 0, err
	}

	if int(attempts) >= s.cfg.MaxLoginAttempts {
		if ttl <= 0 {
			ttl = s.cfg.LockoutDuration
		}
		return true, ttl, nil
	}
	return false, 0, nil
}

// RecordFailedLogin records a failed login attempt and sets the lockout expiration on first attempt.
func (s *Service) RecordFailedLogin(ctx context.Context, username string) (int64, error) {
	key := loginFailKey(username)
	return s.sessions.IncrWithTTL(ctx, key, s.cfg.LockoutDuration)
}

// ResetFailedLogin clears failed login records on successful authentication.
func (s *Service) ResetFailedLogin(ctx context.Context, username string) error {
	return s.sessions.Del(ctx, loginFailKey(username))
}

// Login verifies credentials, checks rate limiting, logs audit events, and returns a new session token.
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (string, *AdminSession, error) {
	locked, retryAfter, err := s.CheckLoginLockout(ctx, username)
	if err != nil {
		return "", nil, err
	}
	if locked {
		_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "retry_after_s": int(retryAfter.Seconds())}, ip, "denied")
		return "", nil, ErrAccountLocked
	}

	user, err := s.store.GetAdminUser(ctx, username)
	if err != nil {
		attempts, _ := s.RecordFailedLogin(ctx, username)
		_ = s.store.RecordAdminAudit(ctx, username, "login_failed", username, map[string]any{"ip": ip, "reason": "not_found", "attempts": attempts}, ip, "denied")
		if int(attempts) >= s.cfg.MaxLoginAttempts {
			_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "attempts": attempts}, ip, "denied")
		}
		return "", nil, ErrInvalidCredentials
	}

	if !user.Enabled {
		_ = s.store.RecordAdminAudit(ctx, username, "login_failed", username, map[string]any{"ip": ip, "reason": "disabled"}, ip, "denied")
		return "", nil, ErrAccountDisabled
	}

	if err := CheckPassword(user.PasswordHash, password); err != nil {
		attempts, _ := s.RecordFailedLogin(ctx, username)
		_ = s.store.RecordAdminAudit(ctx, username, "login_failed", username, map[string]any{"ip": ip, "reason": "invalid_password", "attempts": attempts}, ip, "denied")
		if int(attempts) >= s.cfg.MaxLoginAttempts {
			_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "attempts": attempts}, ip, "denied")
		}
		return "", nil, ErrInvalidCredentials
	}

	_ = s.ResetFailedLogin(ctx, username)
	token, sess, err := s.CreateSession(ctx, user, ip, userAgent)
	if err != nil {
		return "", nil, err
	}

	_ = s.store.RecordAdminAudit(ctx, username, "login", username, map[string]any{"ip": ip, "session_id": sess.ID}, ip, "ok")
	return token, sess, nil
}

// Logout invalidates the active session and logs the audit event.
func (s *Service) Logout(ctx context.Context, token, ip string) error {
	if !strings.HasPrefix(token, "pgadmin_") {
		return ErrInvalidSessionToken
	}

	sum := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(sum[:])
	key := sessionKey(hashHex)

	val, err := s.sessions.Get(ctx, key)
	var username string
	if err == nil {
		var sess AdminSession
		if err := json.Unmarshal(val, &sess); err == nil {
			username = sess.Username
		}
	}

	_ = s.sessions.Del(ctx, key)
	if username != "" {
		_ = s.store.RecordAdminAudit(ctx, username, "logout", username, map[string]any{"ip": ip, "session_id": hashHex}, ip, "ok")
	}
	return nil
}

// Package adminauth provides enterprise-grade capabilities, configuration, and structural components for the adminauth subsystem.
package adminauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

var validUsernameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// ValidateUsername checks if a username is bounded and uses allowed characters.
func ValidateUsername(username string) bool {
	return validUsernameRe.MatchString(username)
}

var dummyHash []byte

func init() {
	var err error
	dummyHash, err = HashPassword("proofgate-constant-work-dummy-token")
	if err != nil {
		panic("failed to precompute dummy bcrypt hash: " + err.Error())
	}
}

// CheckLoginLockout checks if a username/ip combination is currently locked out.
func (s *Service) CheckLoginLockout(ctx context.Context, username string, optionalIP ...string) (bool, time.Duration, error) {
	ip := ""
	if len(optionalIP) > 0 {
		ip = optionalIP[0]
	}
	if ip != "" {
		key := loginFailUserIPKey(username, ip)
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
	}

	// Legacy / user fallback
	legacyKey := loginFailKey(username)
	attempts, ttl, err := s.sessions.GetWithTTL(ctx, legacyKey)
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

// CheckIPLockout checks if an IP has exceeded the 30 failures ceiling.
func (s *Service) CheckIPLockout(ctx context.Context, ip string) (bool, time.Duration, error) {
	key := loginFailIPKey(ip)
	attempts, ttl, err := s.sessions.GetWithTTL(ctx, key)
	if err != nil {
		return false, 0, err
	}
	if int(attempts) >= 30 {
		if ttl <= 0 {
			ttl = s.cfg.LockoutDuration
		}
		return true, ttl, nil
	}
	return false, 0, nil
}

// ResetFailedLogin clears failed login records on successful authentication or administrative reset.
func (s *Service) ResetFailedLogin(ctx context.Context, username string, optionalIP ...string) error {
	_ = s.sessions.Del(ctx, loginFailKey(username))
	_ = s.sessions.Del(ctx, loginFailUserAlertKey(username))
	if len(optionalIP) > 0 && optionalIP[0] != "" {
		_ = s.sessions.Del(ctx, loginFailUserIPKey(username, optionalIP[0]))
		_ = s.sessions.Del(ctx, loginFailIPKey(optionalIP[0]))
	} else {
		// Clear all userip keys matching this user
		keys, err := s.sessions.Scan(ctx, "proofgate:login_fail:userip:"+username+"|*")
		if err == nil && len(keys) > 0 {
			_ = s.sessions.Del(ctx, keys...)
		}
	}
	return nil
}

// Login verifies credentials, checks rate limiting, logs audit events, and returns a new session token.
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (string, *AdminSession, error) {
	// 1. Bound key creation: reject invalid usernames before touching Redis
	if !ValidateUsername(username) {
		return "", nil, ErrInvalidCredentials
	}

	// 2. Fast check: is IP ceiling or account already locked?
	if ipLocked, retryAfter, err := s.CheckIPLockout(ctx, ip); err != nil {
		return "", nil, err
	} else if ipLocked {
		_ = s.store.RecordAdminAudit(ctx, username, "login_ip_locked", username, map[string]any{"ip": ip, "retry_after_s": int(retryAfter.Seconds())}, ip, "denied")
		return "", nil, ErrIPRateLimited
	}

	if locked, retryAfter, err := s.CheckLoginLockout(ctx, username, ip); err != nil {
		return "", nil, err
	} else if locked {
		_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "retry_after_s": int(retryAfter.Seconds())}, ip, "denied")
		return "", nil, ErrAccountLocked
	}

	// 3. Atomic increment FIRST (fixes INCR/EXPIRE gap and TOCTOU)
	userIPKey := loginFailUserIPKey(username, ip)
	ipKey := loginFailIPKey(ip)
	userAlertKey := loginFailUserAlertKey(username)
	legacyKey := loginFailKey(username)

	nUserIP, err := s.sessions.IncrWithTTL(ctx, userIPKey, s.cfg.LockoutDuration)
	if err != nil {
		return "", nil, err
	}
	nIP, _ := s.sessions.IncrWithTTL(ctx, ipKey, s.cfg.LockoutDuration)
	nUser, _ := s.sessions.IncrWithTTL(ctx, userAlertKey, s.cfg.LockoutDuration)
	nLegacy, _ := s.sessions.IncrWithTTL(ctx, legacyKey, s.cfg.LockoutDuration)

	if nUser >= 100 {
		slog.Warn("security alert: excessive failed logins targeting username", "username", username, "attempts", nUser)
	}

	// If locked by this increment:
	if nIP > 30 {
		_ = s.store.RecordAdminAudit(ctx, username, "login_ip_locked", username, map[string]any{"ip": ip, "attempts": nIP}, ip, "denied")
		return "", nil, ErrIPRateLimited
	}
	if nUserIP > int64(s.cfg.MaxLoginAttempts) || nLegacy > int64(s.cfg.MaxLoginAttempts) {
		_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "attempts": nUserIP}, ip, "denied")
		return "", nil, ErrAccountLocked
	}

	// 4. Verify credentials with constant work
	user, err := s.store.GetAdminUser(ctx, username)
	if err != nil {
		// User does not exist: compare against dummyHash for timing defense
		_ = CheckPassword(dummyHash, password)
		_ = s.store.RecordAdminAudit(ctx, username, "login_failed", username, map[string]any{"ip": ip, "reason": "not_found", "attempts": nUserIP}, ip, "denied")
		if nUserIP == int64(s.cfg.MaxLoginAttempts) || nLegacy == int64(s.cfg.MaxLoginAttempts) {
			_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "attempts": nUserIP}, ip, "denied")
		}
		return "", nil, ErrInvalidCredentials
	}

	// Account disabled: still verify password, but return generic ErrInvalidCredentials
	if !user.Enabled {
		_ = CheckPassword(user.PasswordHash, password)
		_ = s.store.RecordAdminAudit(ctx, username, "login_failed", username, map[string]any{"ip": ip, "reason": "disabled", "attempts": nUserIP}, ip, "denied")
		if nUserIP == int64(s.cfg.MaxLoginAttempts) || nLegacy == int64(s.cfg.MaxLoginAttempts) {
			_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "attempts": nUserIP}, ip, "denied")
		}
		return "", nil, ErrInvalidCredentials
	}

	// Verify password
	if err := CheckPassword(user.PasswordHash, password); err != nil {
		_ = s.store.RecordAdminAudit(ctx, username, "login_failed", username, map[string]any{"ip": ip, "reason": "invalid_password", "attempts": nUserIP}, ip, "denied")
		if nUserIP == int64(s.cfg.MaxLoginAttempts) || nLegacy == int64(s.cfg.MaxLoginAttempts) {
			_ = s.store.RecordAdminAudit(ctx, username, "login_locked", username, map[string]any{"ip": ip, "attempts": nUserIP}, ip, "denied")
		}
		return "", nil, ErrInvalidCredentials
	}

	// 5. Success! Clear failed login attempts for this user and IP
	_ = s.sessions.Del(ctx, userIPKey, legacyKey)

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
		if json.Unmarshal(val, &sess) == nil {
			username = sess.Username
		}
	}

	_ = s.RevokeSession(ctx, hashHex)
	if username != "" {
		_ = s.store.RecordAdminAudit(ctx, username, "logout", username, map[string]any{"ip": ip, "session_id": hashHex}, ip, "ok")
	}
	return nil
}

package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/proofgate/proofgate/internal/telemetry"
	"github.com/redis/go-redis/v9"
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrSessionExpired      = errors.New("session expired")
	ErrSessionIdle         = errors.New("session idle timeout")
	ErrInvalidSessionToken = errors.New("invalid session token")
	ErrAccountLocked       = errors.New("account locked")
	ErrIPRateLimited       = errors.New("ip rate limited")
	ErrAccountDisabled     = errors.New("account disabled")
	ErrInvalidCredentials  = errors.New("invalid credentials")
)

type Service struct {
	store    AdminStore
	sessions SessionStore
	cfg      config.AdminAuthConfig
	scrubber *telemetry.Scrubber
}

func NewService(store AdminStore, rdb redis.Cmdable, cfg config.AdminAuthConfig, scrubber *telemetry.Scrubber) *Service {
	return NewServiceWithStore(store, NewRedisStore(rdb), cfg, scrubber)
}

func NewServiceWithStore(store AdminStore, sessions SessionStore, cfg config.AdminAuthConfig, scrubber *telemetry.Scrubber) *Service {
	return &Service{
		store:    store,
		sessions: sessions,
		cfg:      cfg,
		scrubber: scrubber,
	}
}

func (s *Service) AbsTimeout() time.Duration {
	return s.cfg.SessionAbsTimeout
}

func sessionKey(hashHex string) string {
	return "proofgate:session:" + hashHex
}

func userSessionsKey(username string) string {
	return "proofgate:user_sessions:{" + username + "}"
}

func loginFailKey(username string) string {
	return "proofgate:login_fail:" + username
}

func allUsersKey() string {
	return "proofgate:all_session_users"
}

func loginFailUserIPKey(username, ip string) string {
	return "proofgate:login_fail:userip:" + username + "|" + ip
}

func loginFailIPKey(ip string) string {
	return "proofgate:login_fail:ip:" + ip
}

func loginFailUserAlertKey(username string) string {
	return "proofgate:login_fail:user:" + username
}

// GenerateSessionToken creates a 32-byte cryptographically random token with prefix "pgadmin_".
func GenerateSessionToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := "pgadmin_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(sum[:])
	return token, hashHex, nil
}

// CreateSession generates a session token and persists the session metadata and user indices.
func (s *Service) CreateSession(ctx context.Context, user store.AdminUser, ip, userAgent string) (string, *AdminSession, error) {
	token, hashHex, err := GenerateSessionToken()
	if err != nil {
		return "", nil, err
	}
	if s.scrubber != nil {
		s.scrubber.Register(token)
	}

	now := time.Now().UTC()
	sess := &AdminSession{
		ID:           hashHex,
		UserID:       user.ID,
		Username:     user.Username,
		Role:         user.Role,
		CreatedAt:    now,
		LastActiveAt: now,
		IP:           ip,
		UserAgent:    userAgent,
	}

	data, err := json.Marshal(sess)
	if err != nil {
		return "", nil, err
	}

	if err := s.sessions.Set(ctx, sessionKey(hashHex), data, s.cfg.SessionAbsTimeout); err != nil {
		return "", nil, err
	}
	_ = s.sessions.SAdd(ctx, userSessionsKey(user.Username), hashHex)
	_ = s.sessions.SAdd(ctx, allUsersKey(), user.Username)

	return token, sess, nil
}

// ValidateSession verifies a session token, checks idle and absolute timeouts, and refreshes last_active_at atomically.
func (s *Service) ValidateSession(ctx context.Context, token string) (*AdminSession, error) {
	if !strings.HasPrefix(token, "pgadmin_") {
		return nil, ErrInvalidSessionToken
	}

	sum := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(sum[:])
	key := sessionKey(hashHex)

	val, err := s.sessions.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	var sess AdminSession
	if err := json.Unmarshal(val, &sess); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// Check absolute timeout
	if now.Sub(sess.CreatedAt) > s.cfg.SessionAbsTimeout {
		_ = s.RevokeSession(ctx, key)
		return nil, ErrSessionExpired
	}

	// Check idle timeout
	if now.Sub(sess.LastActiveAt) > s.cfg.SessionIdleTimeout {
		_ = s.RevokeSession(ctx, key)
		return nil, ErrSessionIdle
	}

	// Update activity
	sess.LastActiveAt = now
	remaining := sess.CreatedAt.Add(s.cfg.SessionAbsTimeout).Sub(now)
	if remaining <= 0 {
		_ = s.RevokeSession(ctx, key)
		return nil, ErrSessionExpired
	}

	updated, err := json.Marshal(&sess)
	if err != nil {
		return nil, err
	}

	// SetXX prevents resurrection race if revoked concurrently
	ok, err := s.sessions.SetXX(ctx, key, updated, remaining)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}

	return &sess, nil
}

// RevokeSession deletes a session by token or session ID (hash hex) and cleans up user index.
func (s *Service) RevokeSession(ctx context.Context, tokenOrID string) error {
	id := tokenOrID
	if strings.HasPrefix(tokenOrID, "pgadmin_") {
		sum := sha256.Sum256([]byte(tokenOrID))
		id = hex.EncodeToString(sum[:])
	}
	val, _ := s.sessions.Get(ctx, sessionKey(id))
	_ = s.sessions.Del(ctx, sessionKey(id))
	if len(val) > 0 {
		var sess AdminSession
		if json.Unmarshal(val, &sess) == nil && sess.Username != "" {
			_ = s.sessions.SRem(ctx, userSessionsKey(sess.Username), id)
		}
	}
	return nil
}

// RevokeAllUserSessions removes all active sessions for a given username (or all users if username is empty) via set lookups.
func (s *Service) RevokeAllUserSessions(ctx context.Context, username string) (int, error) {
	if username != "" {
		sids, err := s.sessions.SMembers(ctx, userSessionsKey(username))
		if err != nil {
			return 0, err
		}
		var count int
		for _, id := range sids {
			if err := s.sessions.Del(ctx, sessionKey(id)); err == nil {
				count++
			}
		}
		_ = s.sessions.Del(ctx, userSessionsKey(username))
		_ = s.sessions.SRem(ctx, allUsersKey(), username)
		return count, nil
	}

	users, err := s.sessions.SMembers(ctx, allUsersKey())
	if err != nil {
		return 0, err
	}
	var total int
	for _, u := range users {
		sids, _ := s.sessions.SMembers(ctx, userSessionsKey(u))
		for _, id := range sids {
			if err := s.sessions.Del(ctx, sessionKey(id)); err == nil {
				total++
			}
		}
		_ = s.sessions.Del(ctx, userSessionsKey(u))
	}
	_ = s.sessions.Del(ctx, allUsersKey())
	return total, nil
}

// ListSessions lists active sessions matching the username filter (empty string returns all sessions) without SCAN.
func (s *Service) ListSessions(ctx context.Context, username string) ([]AdminSession, error) {
	var usernames []string
	if username != "" {
		usernames = []string{username}
	} else {
		var err error
		usernames, err = s.sessions.SMembers(ctx, allUsersKey())
		if err != nil {
			return nil, err
		}
	}

	var sessions []AdminSession
	for _, u := range usernames {
		sids, err := s.sessions.SMembers(ctx, userSessionsKey(u))
		if err != nil {
			continue
		}
		for _, id := range sids {
			val, err := s.sessions.Get(ctx, sessionKey(id))
			if errors.Is(err, ErrSessionNotFound) {
				_ = s.sessions.SRem(ctx, userSessionsKey(u), id)
				continue
			}
			if err == nil {
				var sess AdminSession
				if json.Unmarshal(val, &sess) == nil {
					sessions = append(sessions, sess)
				}
			}
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}

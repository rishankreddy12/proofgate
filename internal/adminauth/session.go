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

func sessionKey(hashHex string) string {
	return "proofgate:session:" + hashHex
}

func loginFailKey(username string) string {
	return "proofgate:login_fail:" + username
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

// CreateSession generates a session token and persists the session metadata.
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
	return token, sess, nil
}

// ValidateSession verifies a session token, checks idle and absolute timeouts, and refreshes last_active_at.
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
		_ = s.sessions.Del(ctx, key)
		return nil, ErrSessionExpired
	}

	// Check idle timeout
	if now.Sub(sess.LastActiveAt) > s.cfg.SessionIdleTimeout {
		_ = s.sessions.Del(ctx, key)
		return nil, ErrSessionIdle
	}

	// Update activity
	sess.LastActiveAt = now
	remaining := sess.CreatedAt.Add(s.cfg.SessionAbsTimeout).Sub(now)
	if remaining <= 0 {
		_ = s.sessions.Del(ctx, key)
		return nil, ErrSessionExpired
	}

	updated, err := json.Marshal(&sess)
	if err != nil {
		return nil, err
	}
	if err := s.sessions.Set(ctx, key, updated, remaining); err != nil {
		return nil, err
	}

	return &sess, nil
}

// RevokeSession deletes a session by token or session ID (hash hex).
func (s *Service) RevokeSession(ctx context.Context, tokenOrID string) error {
	id := tokenOrID
	if strings.HasPrefix(tokenOrID, "pgadmin_") {
		sum := sha256.Sum256([]byte(tokenOrID))
		id = hex.EncodeToString(sum[:])
	}
	return s.sessions.Del(ctx, sessionKey(id))
}

// RevokeAllUserSessions removes all active sessions for a given username (or all users if username is empty).
func (s *Service) RevokeAllUserSessions(ctx context.Context, username string) (int, error) {
	keys, err := s.sessions.Scan(ctx, "proofgate:session:*")
	if err != nil {
		return 0, err
	}
	var count int
	for _, k := range keys {
		val, err := s.sessions.Get(ctx, k)
		if err == nil {
			var sess AdminSession
			if err := json.Unmarshal(val, &sess); err == nil {
				if username == "" || sess.Username == username {
					if err := s.sessions.Del(ctx, k); err == nil {
						count++
					}
				}
			}
		}
	}
	return count, nil
}

// ListSessions lists active sessions matching the username filter (empty string returns all sessions).
func (s *Service) ListSessions(ctx context.Context, username string) ([]AdminSession, error) {
	keys, err := s.sessions.Scan(ctx, "proofgate:session:*")
	if err != nil {
		return nil, err
	}
	var sessions []AdminSession
	for _, k := range keys {
		val, err := s.sessions.Get(ctx, k)
		if err == nil {
			var sess AdminSession
			if err := json.Unmarshal(val, &sess); err == nil {
				if username == "" || sess.Username == username {
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

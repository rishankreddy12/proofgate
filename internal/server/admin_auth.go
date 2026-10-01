package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/adminauth"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

type WhoamiResponse struct {
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (deps *ControlPlaneDeps) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "invalid JSON body",
		})
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "username and password are required",
		})
		return
	}

	ip := clientIP(r)
	token, sess, err := deps.AuthService.Login(r.Context(), req.Username, req.Password, ip, r.UserAgent())
	if err != nil {
		if errors.Is(err, adminauth.ErrAccountLocked) {
			_, ttl, _ := deps.AuthService.CheckLoginLockout(r.Context(), req.Username)
			if ttl > 0 {
				w.Header().Set("Retry-After", time.Duration(ttl.Seconds()).String())
			}
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error":   "rate_limited",
				"message": "account is locked due to too many failed attempts",
			})
			return
		}
		if errors.Is(err, adminauth.ErrAccountDisabled) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error":   "forbidden",
				"message": "account is disabled",
			})
			return
		}
		if errors.Is(err, adminauth.ErrInvalidCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error":   "unauthorized",
				"message": "invalid username or password",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "internal_error",
			"message": "failed to process login",
		})
		return
	}

	writeJSON(w, http.StatusOK, LoginResponse{
		Token:     token,
		UserID:    sess.UserID,
		Username:  sess.Username,
		Role:      sess.Role,
		ExpiresAt: sess.CreatedAt.Add(12 * time.Hour),
	})
}

func (deps *ControlPlaneDeps) handleLogout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	parts := strings.SplitN(authHeader, " ", 2)
	var token string
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		token = strings.TrimSpace(parts[1])
	}

	ip := clientIP(r)
	if err := deps.AuthService.Logout(r.Context(), token, ip); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (deps *ControlPlaneDeps) handleWhoami(w http.ResponseWriter, r *http.Request) {
	sess := adminauth.GetAdminPrincipal(r.Context())
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error":   "unauthorized",
			"message": "not authenticated",
		})
		return
	}

	writeJSON(w, http.StatusOK, WhoamiResponse{
		UserID:       sess.UserID,
		Username:     sess.Username,
		Role:         sess.Role,
		CreatedAt:    sess.CreatedAt,
		LastActiveAt: sess.LastActiveAt,
	})
}

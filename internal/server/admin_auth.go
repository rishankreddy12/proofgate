package server

import (
	"errors"
	"math"
	"net/http"
	"strconv"
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

func (deps *ControlPlaneDeps) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "username and password are required",
		})
		return
	}

	if !adminauth.ValidateUsername(req.Username) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error":   "unauthorized",
			"message": "invalid credentials",
		})
		return
	}

	ip := deps.clientIP(r)
	token, sess, err := deps.AuthService.Login(r.Context(), req.Username, req.Password, ip, r.UserAgent())
	if err != nil {
		if errors.Is(err, adminauth.ErrIPRateLimited) {
			_, ttl, _ := deps.AuthService.CheckIPLockout(r.Context(), ip)
			if ttl > 0 {
				secs := int(math.Ceil(ttl.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
			}
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error":   "rate_limited",
				"message": "too many failed login attempts from this IP",
			})
			return
		}
		if errors.Is(err, adminauth.ErrAccountLocked) {
			_, ttl, _ := deps.AuthService.CheckLoginLockout(r.Context(), req.Username, ip)
			if ttl > 0 {
				secs := int(math.Ceil(ttl.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
			}
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error":   "rate_limited",
				"message": "account is locked due to too many failed attempts",
			})
			return
		}
		if errors.Is(err, adminauth.ErrInvalidCredentials) || errors.Is(err, adminauth.ErrAccountDisabled) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error":   "unauthorized",
				"message": "invalid credentials",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "internal_error",
			"message": "failed to process login",
		})
		return
	}

	absTimeout := deps.AuthService.AbsTimeout()
	if absTimeout == 0 {
		absTimeout = 12 * time.Hour
	}
	writeJSON(w, http.StatusOK, LoginResponse{
		Token:     token,
		UserID:    sess.UserID,
		Username:  sess.Username,
		Role:      sess.Role,
		ExpiresAt: sess.CreatedAt.Add(absTimeout),
	})
}

func (deps *ControlPlaneDeps) handleLogout(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	parts := strings.SplitN(authHeader, " ", 2)
	var token string
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		token = strings.TrimSpace(parts[1])
	}

	ip := deps.clientIP(r)
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

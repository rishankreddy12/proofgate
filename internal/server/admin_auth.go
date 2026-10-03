// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
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

// LoginRequest defines the expected JSON schema for the admin login endpoint.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse defines the JSON schema returned upon a successful authentication event.
// It encapsulates the JWT/Session token along with RBAC assertions.
type LoginResponse struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WhoamiResponse defines the JSON schema for identity introspection.
// It is used by the frontend admin console to verify session validity on reload.
type WhoamiResponse struct {
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// handleLogin orchestrates the authentication workflow for the admin control plane.
// It validates credentials, interfaces with the underlying AuthService to mint sessions,
// and enforces security invariants such as IP-based rate limiting and account lockouts
// to mitigate brute-force attacks.
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

	// Delegate core credential verification and session minting to the auth package.
	token, sess, err := deps.AuthService.Login(r.Context(), req.Username, req.Password, ip, r.UserAgent())
	if err != nil {
		// Handle IP-level lockouts (too many attempts from a single origin)
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

		// Handle Account-level lockouts (too many attempts against a single username)
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

// handleLogout extracts the Bearer token from the incoming request and delegates
// its invalidation to the AuthService, ensuring that the token is immediately
// added to the revocation list and can no longer be used for authorization.
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

// handleWhoami provides a secure introspection endpoint for the frontend.
// It relies on upstream middleware to have already verified the session and injected
// the adminauth.Principal into the request context.
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

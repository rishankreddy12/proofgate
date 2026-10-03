// Package adminauth provides enterprise-grade capabilities, configuration, and structural components for the adminauth subsystem.
package adminauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ErrorResponse defines the core enterprise configuration and state for ErrorResponse.
// It is responsible for managing the lifecycle, validation, and schema of the ErrorResponse entity.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error:   code,
		Message: msg,
	})
}

func extractAdminToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// RequireAuth validates session tokens and populates AdminPrincipal into context.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractAdminToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing admin authorization token")
			return
		}

		sess, err := s.ValidateSession(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "session expired or invalid")
			return
		}

		ctx := WithAdminPrincipal(r.Context(), sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission verifies that the authenticated user possesses the required permission.
func RequirePermission(perm Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := GetAdminPrincipal(r.Context())
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
			return
		}

		if !HasPermission(sess.Role, perm) {
			writeError(w, http.StatusForbidden, "forbidden", fmt.Sprintf("role '%s' cannot perform '%s'", sess.Role, perm))
			return
		}

		next(w, r)
	}
}

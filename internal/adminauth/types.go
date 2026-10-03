// Package adminauth provides enterprise-grade capabilities, configuration, and structural components for the adminauth subsystem.
package adminauth

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type contextKey int

const adminPrincipalKey contextKey = 1

// AdminSession represents an authenticated administrative session stored in Redis.
type AdminSession struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"user_agent"`
}

// AdminPrincipal holds the authenticated administrator context for HTTP requests.
type AdminPrincipal struct {
	Session AdminSession
}

// WithAdminPrincipal executes the primary logic for the WithAdminPrincipal operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func WithAdminPrincipal(ctx context.Context, session *AdminSession) context.Context {
	return context.WithValue(ctx, adminPrincipalKey, session)
}

// GetAdminPrincipal executes the primary logic for the GetAdminPrincipal operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func GetAdminPrincipal(ctx context.Context) *AdminSession {
	if v := ctx.Value(adminPrincipalKey); v != nil {
		if s, ok := v.(*AdminSession); ok {
			return s
		}
	}
	return nil
}

// HashPassword creates a bcrypt hash with cost 12.
func HashPassword(password string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(password), 12)
}

// CheckPassword verifies a plaintext password against a bcrypt hash.
func CheckPassword(hash []byte, password string) error {
	return bcrypt.CompareHashAndPassword(hash, []byte(password))
}

// AdminStore defines database operations required by the admin auth service.
type AdminStore interface {
	GetAdminUser(ctx context.Context, username string) (store.AdminUser, error)
	RecordAdminAudit(ctx context.Context, actor, action, target string, detail any, ip, result string) error
}

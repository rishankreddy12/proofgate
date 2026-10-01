package adminauth

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrPasswordTooShort       = errors.New("password must be at least 12 characters")
	ErrPasswordTooLong        = errors.New("password cannot exceed 72 bytes")
	ErrPasswordEqualsUsername = errors.New("password cannot equal username")
	ErrPasswordBreached       = errors.New("password appears in known data breaches and cannot be used")
)

// BreachedPasswordChecker checks whether a password appears in compromised password databases.
type BreachedPasswordChecker func(ctx context.Context, password string) bool

// BreachedChecker is an optional global hook for breached-password checking.
var BreachedChecker BreachedPasswordChecker

// ValidatePassword validates password complexity and security constraints.
func ValidatePassword(password, username string) error {
	if utf8.RuneCountInString(password) < 12 {
		return ErrPasswordTooShort
	}
	if len([]byte(password)) > 72 {
		return ErrPasswordTooLong
	}
	if u := strings.TrimSpace(username); u != "" {
		if strings.EqualFold(strings.TrimSpace(password), u) {
			return ErrPasswordEqualsUsername
		}
	}
	if BreachedChecker != nil && BreachedChecker(context.Background(), password) {
		return ErrPasswordBreached
	}
	return nil
}

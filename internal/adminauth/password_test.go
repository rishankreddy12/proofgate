package adminauth

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePassword(t *testing.T) {
	// Too short (< 12 chars)
	require.ErrorIs(t, ValidatePassword("short", "alice"), ErrPasswordTooShort)
	require.ErrorIs(t, ValidatePassword("12345678901", "alice"), ErrPasswordTooShort)

	// Too long (> 72 bytes)
	longPw := strings.Repeat("a", 73)
	require.ErrorIs(t, ValidatePassword(longPw, "alice"), ErrPasswordTooLong)

	// Equals username
	require.ErrorIs(t, ValidatePassword("alice1234567", "alice1234567"), ErrPasswordEqualsUsername)
	require.ErrorIs(t, ValidatePassword("Alice1234567", "alice1234567"), ErrPasswordEqualsUsername)

	// Valid password
	require.NoError(t, ValidatePassword("correcthorsebatterystaple", "alice"))
	require.NoError(t, ValidatePassword(strings.Repeat("b", 72), "alice"))

	// Breached password hook
	origChecker := BreachedChecker
	defer func() { BreachedChecker = origChecker }()

	BreachedChecker = func(ctx context.Context, p string) bool {
		return p == "password123456"
	}
	require.ErrorIs(t, ValidatePassword("password123456", "bob"), ErrPasswordBreached)
	require.NoError(t, ValidatePassword("unique-password-789", "bob"))
}

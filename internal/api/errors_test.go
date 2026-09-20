package api

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, RateLimited("rate_limited", 1500*time.Millisecond))
	require.Equal(t, 429, rec.Code)
	require.Equal(t, "2", rec.Header().Get("Retry-After"))
	require.JSONEq(t, `{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":"rate_limited"}}`, rec.Body.String())
}

func TestWriteErrorWrappedAndUnknown(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, fmt.Errorf("ctx: %w", BudgetExceeded("monthly budget reached")))
	require.Equal(t, 402, rec.Code)

	rec = httptest.NewRecorder()
	WriteError(rec, errors.New("secret internal detail"))
	require.Equal(t, 500, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret internal detail")
}

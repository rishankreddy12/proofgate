package provider

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetryableStatus(t *testing.T) {
	for _, s := range []int{408, 409, 429, 500, 502, 503, 504, 529} {
		require.True(t, RetryableStatus(s), s)
	}
	for _, s := range []int{400, 401, 403, 404, 413, 422} {
		require.False(t, RetryableStatus(s), s)
	}
}

func TestFromHTTPExtractsMessage(t *testing.T) {
	resp := &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"overloaded"}}`))}
	e := FromHTTP("openai", resp)
	require.Equal(t, 503, e.Status)
	require.True(t, e.Retryable)
	require.Equal(t, "overloaded", e.Message)
	require.Equal(t, "openai: 503 overloaded", e.Error())
}

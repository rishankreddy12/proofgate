package httpx

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRedirectDoesNotFollowAndLeakHeaders(t *testing.T) {
	var targetCalls int32

	// Attacker-controlled target server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()

	// Compromised or redirecting provider server that redirects to targetServer
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/leaked", http.StatusFound)
	}))
	defer redirectServer.Close()

	client := New(WithTimeout(2 * time.Second))

	req, err := http.NewRequest(http.MethodGet, redirectServer.URL+"/api/chat", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "secret-key-12345")
	req.Header.Set("x-goog-api-key", "gemini-secret-999")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The client must return the 302 response directly without following the redirect
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, targetServer.URL+"/leaked", resp.Header.Get("Location"))

	// The target server must never have received any requests!
	require.Equal(t, int32(0), atomic.LoadInt32(&targetCalls))
}

func TestLimitReaderEnforcesCap(t *testing.T) {
	// Source with 100 bytes
	src := strings.Repeat("A", 100)

	// Case 1: Limit larger than src
	lrOk := LimitReader(strings.NewReader(src), 200)
	data, err := io.ReadAll(lrOk)
	require.NoError(t, err)
	require.Len(t, data, 100)

	// Case 2: Limit equal to src
	lrExact := LimitReader(strings.NewReader(src), 100)
	data, err = io.ReadAll(lrExact)
	require.NoError(t, err)
	require.Len(t, data, 100)

	// Case 3: Limit smaller than src -> returns ErrResponseTooLarge
	lrSmall := LimitReader(strings.NewReader(src), 50)
	_, err = io.ReadAll(lrSmall)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrResponseTooLarge), "expected ErrResponseTooLarge, got %v", err)
}

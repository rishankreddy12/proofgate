package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientRequestsAndErrorHandling(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /test/ok", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test_token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "success"})
	})

	mux.HandleFunc("GET /test/unauthorized", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: "unauthorized", Message: "token expired"})
	})

	mux.HandleFunc("GET /test/forbidden", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: "forbidden", Message: "viewer cannot do this"})
	})

	mux.HandleFunc("GET /test/ratelimit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: "rate_limited", Message: "too many attempts"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &Client{
		BaseURL:    srv.URL,
		Token:      "test_token",
		HTTPClient: srv.Client(),
	}

	// 1. Success test
	var resp map[string]string
	status, err := client.Do(context.Background(), "GET", "/test/ok", nil, &resp)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "success", resp["result"])

	// 2. Unauthorized test
	_, err = client.Do(context.Background(), "GET", "/test/unauthorized", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not authenticated")

	// 3. Forbidden test
	_, err = client.Do(context.Background(), "GET", "/test/forbidden", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")

	// 4. Rate limit test
	_, err = client.Do(context.Background(), "GET", "/test/ratelimit", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "rate limited")
}

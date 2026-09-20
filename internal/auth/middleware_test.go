package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeLookup struct {
	calls atomic.Int32
	keys  map[string]store.KeyRecord
}

func (f *fakeLookup) KeyByHash(_ context.Context, h []byte) (store.KeyRecord, error) {
	f.calls.Add(1)
	if r, ok := f.keys[string(h)]; ok {
		return r, nil
	}
	return store.KeyRecord{}, store.ErrNotFound
}

func TestMiddleware(t *testing.T) {
	key, _, hash, _ := GenerateKey("test")
	f := &fakeLookup{keys: map[string]store.KeyRecord{string(hash): {
		Key:    store.APIKey{ID: "k1", Name: "ci", AllowedRoutes: []string{"default"}},
		Tenant: store.Tenant{ID: "t1", Name: "acme", Policy: store.TenantPolicy{RPM: 5}},
	}}}
	mw := NewMiddleware(f, time.Minute, time.Minute)
	var got Principal
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		w.WriteHeader(200)
	}))

	do := func(auth string) int {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, 401, do(""))
	require.Equal(t, 401, do("Bearer pg_test_wrong"))
	require.Equal(t, 401, do("Bearer pg_test_wrong"))
	require.EqualValues(t, 1, f.calls.Load(), "unknown key is negatively cached")

	require.Equal(t, 200, do("Bearer "+key))
	require.Equal(t, 200, do("Bearer "+key))
	require.EqualValues(t, 2, f.calls.Load(), "valid key is cached")
	require.Equal(t, "acme", got.TenantName)
	require.Equal(t, 5, got.Tenant.RPM)
	require.Equal(t, []string{"default"}, got.AllowedRoutes)
}

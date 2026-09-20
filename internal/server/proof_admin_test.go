package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/proof"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type mockCurveStore struct {
	sims    []float64
	labeled []proof.LabeledPoint
}

func (m *mockCurveStore) CurveInputs(_ context.Context, _ string, _ time.Time) ([]float64, []proof.LabeledPoint, error) {
	return m.sims, m.labeled, nil
}

type memOverrideSetter struct {
	overrides []store.Override
}

func (m *memOverrideSetter) SetOverride(_ context.Context, o store.Override) error {
	m.overrides = append(m.overrides, o)
	return nil
}

func TestAdmin_CurveEndpoint(t *testing.T) {
	cStore := &mockCurveStore{
		sims: []float64{0.75, 0.85, 0.90, 0.95},
		labeled: []proof.LabeledPoint{
			{Similarity: 0.75, Acceptable: false, Rater: "judge"},
			{Similarity: 0.85, Acceptable: true, Rater: "judge"},
			{Similarity: 0.90, Acceptable: true, Rater: "judge"},
			{Similarity: 0.95, Acceptable: true, Rater: "judge"},
		},
	}

	admin := &ProofAdminHandlers{
		Store:    cStore,
		AdminKey: "secret-key",
	}

	// 1. Missing route parameter -> 400
	req := httptest.NewRequest("GET", "/admin/proof/cache/curve", nil)
	req.Header.Set("X-ProofGate-Admin-Key", "secret-key")
	w := httptest.NewRecorder()
	admin.CacheCurve(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)

	// 2. Unauthorized if key mismatch
	adminReq := httptest.NewRequest("GET", "/admin/proof/cache/curve?route=faq", nil)
	adminReq.Header.Set("X-ProofGate-Admin-Key", "wrong")
	w = httptest.NewRecorder()
	admin.CacheCurve(w, adminReq)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	// 3. Authorized request with valid key
	adminReq.Header.Set("X-ProofGate-Admin-Key", "secret-key")
	w = httptest.NewRecorder()
	admin.CacheCurve(w, adminReq)
	require.Equal(t, http.StatusOK, w.Code)

	var resp CacheCurveResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "faq", resp.Route)
	require.Equal(t, 0.01, resp.TargetFalseHitRate)
	require.Equal(t, 4, resp.DataPoints)
	require.Len(t, resp.Curve, 29)
}

func TestAdmin_ApplyEndpoint(t *testing.T) {
	setter := &memOverrideSetter{}
	reloaded := false

	admin := &ProofAdminHandlers{
		Setter:   setter,
		AdminKey: "secret-key",
		OnReload: func() { reloaded = true },
	}

	// 1. Unauthorized
	req := httptest.NewRequest("POST", "/admin/proof/cache/apply?route=faq&threshold=0.87", nil)
	w := httptest.NewRecorder()
	admin.CacheApply(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	// 2. Authorized via Bearer header with JSON body
	body := `{"route": "faq", "threshold": 0.88}`
	req = httptest.NewRequest("POST", "/admin/proof/cache/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-key")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	admin.CacheApply(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp CacheApplyResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "applied", resp.Status)
	require.Equal(t, "faq", resp.Route)
	require.Equal(t, "on", resp.CacheMode)
	require.Equal(t, 0.88, resp.CacheThreshold)

	// Verify overrides stored
	require.Len(t, setter.overrides, 2)
	require.Equal(t, "cache.mode", setter.overrides[0].Key)
	require.Equal(t, "on", setter.overrides[0].Value)
	require.Equal(t, "cache.threshold", setter.overrides[1].Key)
	require.Equal(t, "0.8800", setter.overrides[1].Value)

	// Verify runtime reload callback triggered
	require.True(t, reloaded)
}

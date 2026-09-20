package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/proof"
	"github.com/proofgate/proofgate/internal/store"
)

type ProofAdminHandlers struct {
	Store    proof.CurveInputStore
	Setter   OverrideSetter
	AdminKey string
	OnReload func()
}

type OverrideSetter interface {
	SetOverride(ctx context.Context, o store.Override) error
}

func (h *ProofAdminHandlers) authorize(r *http.Request) bool {
	if h.AdminKey == "" {
		return true // no key required if not configured
	}
	if r.Header.Get("X-ProofGate-Admin-Key") == h.AdminKey {
		return true
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") && strings.TrimPrefix(auth, "Bearer ") == h.AdminKey {
		return true
	}
	return false
}

type CacheCurveResponse struct {
	Route                string             `json:"route"`
	RecommendedThreshold float64            `json:"recommended_threshold"`
	TargetFalseHitRate   float64            `json:"target_false_hit_rate"`
	DataPoints           int                `json:"data_points"`
	Curve                []proof.CurvePoint `json:"curve"`
}

func (h *ProofAdminHandlers) CacheCurve(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	route := r.URL.Query().Get("route")
	if route == "" {
		http.Error(w, `{"error": "route query parameter is required"}`, http.StatusBadRequest)
		return
	}

	targetFHR := 0.01
	if s := r.URL.Query().Get("target_fhr"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			targetFHR = v
		}
	}

	rec, curve, n, err := proof.TuneCache(r.Context(), h.Store, route, targetFHR, time.Time{})
	if err != nil {
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	resp := CacheCurveResponse{
		Route:                route,
		RecommendedThreshold: rec,
		TargetFalseHitRate:   targetFHR,
		DataPoints:           n,
		Curve:                curve,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type CacheApplyRequest struct {
	Route     string  `json:"route"`
	Threshold float64 `json:"threshold"`
}

type CacheApplyResponse struct {
	Status         string  `json:"status"`
	Route          string  `json:"route"`
	CacheMode      string  `json:"cache_mode"`
	CacheThreshold float64 `json:"cache_threshold"`
}

func (h *ProofAdminHandlers) CacheApply(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	route := r.URL.Query().Get("route")
	var threshold float64

	if r.Method == http.MethodPost && r.Body != nil && r.ContentLength > 0 {
		var req CacheApplyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			if req.Route != "" {
				route = req.Route
			}
			if req.Threshold > 0 {
				threshold = req.Threshold
			}
		}
	}

	if threshold == 0 {
		if s := r.URL.Query().Get("threshold"); s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				threshold = v
			}
		}
	}

	if route == "" {
		http.Error(w, `{"error": "route is required"}`, http.StatusBadRequest)
		return
	}
	if threshold < 0.50 || threshold > 1.00 {
		http.Error(w, `{"error": "threshold must be between 0.50 and 1.00"}`, http.StatusBadRequest)
		return
	}

	if h.Setter == nil {
		http.Error(w, `{"error": "override store not available"}`, http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	// Set cache.mode = "on"
	err1 := h.Setter.SetOverride(ctx, store.Override{
		Route:  route,
		Key:    "cache.mode",
		Value:  "on",
		Reason: "calibrated threshold applied",
		Actor:  "admin",
	})
	if err1 != nil {
		http.Error(w, `{"error": "`+err1.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	// Set cache.threshold
	err2 := h.Setter.SetOverride(ctx, store.Override{
		Route:  route,
		Key:    "cache.threshold",
		Value:  strconv.FormatFloat(threshold, 'f', 4, 64),
		Reason: "calibrated threshold applied",
		Actor:  "admin",
	})
	if err2 != nil {
		http.Error(w, `{"error": "`+err2.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	if h.OnReload != nil {
		h.OnReload()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CacheApplyResponse{
		Status:         "applied",
		Route:          route,
		CacheMode:      "on",
		CacheThreshold: threshold,
	})
}

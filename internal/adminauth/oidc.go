// Package adminauth provides enterprise-grade capabilities, configuration, and structural components for the adminauth subsystem.
package adminauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig defines the core enterprise configuration and state for OIDCConfig.
// It is responsible for managing the lifecycle, validation, and schema of the OIDCConfig entity.
type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	CookieDomain string
	CookieSecure bool
}

// OIDCProvider defines the core enterprise configuration and state for OIDCProvider.
// It is responsible for managing the lifecycle, validation, and schema of the OIDCProvider entity.
type OIDCProvider struct {
	provider *oidc.Provider
	oauth2   oauth2.Config
	verifier *oidc.IDTokenVerifier
	config   OIDCConfig
	store    SessionStore
}

// NewOIDCProvider executes the primary logic for the NewOIDCProvider operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewOIDCProvider(ctx context.Context, config OIDCConfig, store SessionStore) (*OIDCProvider, error) {
	if config.IssuerURL == "" || config.ClientID == "" {
		return nil, errors.New("OIDC config requires IssuerURL and ClientID")
	}

	p, err := oidc.NewProvider(ctx, config.IssuerURL)
	if err != nil {
		return nil, err
	}

	oauth2Config := oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  config.RedirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}

	verifier := p.Verifier(&oidc.Config{ClientID: config.ClientID})

	return &OIDCProvider{
		provider: p,
		oauth2:   oauth2Config,
		verifier: verifier,
		config:   config,
		store:    store,
	}, nil
}

// HandleLogin executes the primary logic for the HandleLogin operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (op *OIDCProvider) HandleLogin(w http.ResponseWriter, r *http.Request) {
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := base64.URLEncoding.EncodeToString(stateBytes)

	// In a real implementation, we should save the state in a cookie or redis to verify it on callback
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		MaxAge:   int(10 * time.Minute.Seconds()),
		Secure:   op.config.CookieSecure,
		HttpOnly: true,
		Domain:   op.config.CookieDomain,
		Path:     "/",
	})

	url := op.oauth2.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusFound)
}

// HandleCallback executes the primary logic for the HandleCallback operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (op *OIDCProvider) HandleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state parameter", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	oauth2Token, err := op.oauth2.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token field in oauth2 token", http.StatusInternalServerError)
		return
	}

	idToken, err := op.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "failed to verify ID token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var claims struct {
		Email  string   `json:"email"`
		Groups []string `json:"groups"` // Google Workspace, Okta
		Roles  []string `json:"roles"`  // Entra ID
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "failed to parse claims: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Map OIDC groups/roles to ProofGate RBAC Roles
	role := "viewer"
	allClaims := append(claims.Groups, claims.Roles...)
	for _, g := range allClaims {
		if g == "proofgate_admin" {
			role = "admin"
			break
		}
		if g == "proofgate_operator" && role != "admin" {
			role = "operator"
		}
	}

	sessionIDBytes := make([]byte, 32)
	rand.Read(sessionIDBytes)
	sessionID := base64.URLEncoding.EncodeToString(sessionIDBytes)

	session := &AdminSession{
		ID:           sessionID,
		Username:     claims.Email,
		Role:         role,
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
		IP:           r.RemoteAddr,
		UserAgent:    r.UserAgent(),
	}

	data, err := json.Marshal(session)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := op.store.Set(ctx, sessionKey(session.ID), data, 12*time.Hour); err != nil {
		http.Error(w, "failed to store session", http.StatusInternalServerError)
		return
	}
	if err := op.store.SAdd(ctx, userSessionsKey(claims.Email), session.ID); err != nil {
		// non-fatal
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "proofgate_admin",
		Value:    session.ID,
		Path:     "/",
		Expires:  time.Now().Add(12 * time.Hour),
		HttpOnly: true,
		Secure:   op.config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Domain:   op.config.CookieDomain,
	})

	http.Redirect(w, r, "/admin", http.StatusFound)
}

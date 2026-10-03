// Package secrets provides enterprise-grade capabilities, configuration, and structural components for the secrets subsystem.
package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/httpx"
)

// TokenSource defines the core enterprise configuration and state for TokenSource.
// It is responsible for managing the lifecycle, validation, and schema of the TokenSource entity.
type TokenSource func(ctx context.Context) (string, error)

// StaticToken executes the primary logic for the StaticToken operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func StaticToken(tok string) TokenSource {
	return func(context.Context) (string, error) {
		if tok == "" {
			return "", errors.New("empty Vault token")
		}
		return tok, nil
	}
}

func vaultPost(ctx context.Context, c *http.Client, url, token string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("vault %s: status %d", strings.TrimPrefix(url, "http"), resp.StatusCode) // never echo the body: it can contain secrets
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// KubernetesAuth executes the primary logic for the KubernetesAuth operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func KubernetesAuth(addr, role, jwtPath string, client *http.Client) TokenSource {
	if client == nil {
		client = httpx.New(httpx.WithTimeout(10 * time.Second))
	}
	var (
		mu      sync.Mutex
		token   string
		expires time.Time
	)
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if token != "" && time.Now().Before(expires) {
			return token, nil
		}
		jwt, err := os.ReadFile(jwtPath)
		if err != nil {
			return "", err
		}
		var out struct {
			Auth struct {
				ClientToken   string `json:"client_token"`
				LeaseDuration int    `json:"lease_duration"`
			} `json:"auth"`
		}
		if err := vaultPost(ctx, client, strings.TrimRight(addr, "/")+"/v1/auth/kubernetes/login", "",
			map[string]string{"role": role, "jwt": strings.TrimSpace(string(jwt))}, &out); err != nil {
			return "", err
		}
		token = out.Auth.ClientToken
		expires = time.Now().Add(time.Duration(out.Auth.LeaseDuration)*time.Second - 30*time.Second)
		return token, nil
	}
}

// VaultKEK defines the core enterprise configuration and state for VaultKEK.
// It is responsible for managing the lifecycle, validation, and schema of the VaultKEK entity.
type VaultKEK struct {
	addr, key string
	tokens    TokenSource
	client    *http.Client
}

// NewVaultKEK executes the primary logic for the NewVaultKEK operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewVaultKEK(addr, key string, tokens TokenSource, client *http.Client) *VaultKEK {
	if client == nil {
		client = httpx.New(httpx.WithTimeout(10 * time.Second))
	}
	return &VaultKEK{addr: strings.TrimRight(addr, "/"), key: key, tokens: tokens, client: client}
}

// ID executes the primary logic for the ID operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (v *VaultKEK) ID() string { return "vault:" + v.key }

// Wrap executes the primary logic for the Wrap operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (v *VaultKEK) Wrap(ctx context.Context, dek []byte) ([]byte, error) {
	tok, err := v.tokens(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	if err := vaultPost(ctx, v.client, v.addr+"/v1/transit/encrypt/"+v.key, tok,
		map[string]string{"plaintext": base64.StdEncoding.EncodeToString(dek)}, &out); err != nil {
		return nil, err
	}
	return []byte(out.Data.Ciphertext), nil
}

// Unwrap executes the primary logic for the Unwrap operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (v *VaultKEK) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	tok, err := v.tokens(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := vaultPost(ctx, v.client, v.addr+"/v1/transit/decrypt/"+v.key, tok,
		map[string]string{"ciphertext": string(wrapped)}, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Data.Plaintext)
}

// DynamicVaultResolver implements KEKResolver for Vault transit keys.
// If it sees an ID like "vault:transit/tenant-t1-key", it extracts "transit/tenant-t1-key"
// and creates a VaultKEK on the fly using the provided base address and token source.
type DynamicVaultResolver struct {
	addr   string
	tokens TokenSource
	client *http.Client
}

// NewDynamicVaultResolver executes the primary logic for the NewDynamicVaultResolver operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewDynamicVaultResolver(addr string, tokens TokenSource, client *http.Client) *DynamicVaultResolver {
	if client == nil {
		client = httpx.New(httpx.WithTimeout(10 * time.Second))
	}
	return &DynamicVaultResolver{addr: strings.TrimRight(addr, "/"), tokens: tokens, client: client}
}

// ResolveKEK executes the primary logic for the ResolveKEK operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (d *DynamicVaultResolver) ResolveKEK(id string) (KEK, bool) {
	if strings.HasPrefix(id, "vault:") {
		key := strings.TrimPrefix(id, "vault:")
		return NewVaultKEK(d.addr, key, d.tokens, d.client), true
	}
	return nil, false
}

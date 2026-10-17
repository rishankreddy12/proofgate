// Package config provides enterprise-grade capabilities, configuration, and structural components for the config subsystem.
package config

import (
	"net/url"
	"strings"
)

// Redact sanitizes a populated Config struct by stripping out sensitive
// environment variable references, raw headers, connection credentials, and symmetric key files.
// It returns a safe, shallow copy suitable for serialization, telemetry,
// or unprivileged API exposure without leaking upstream provider credentials.
func Redact(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	cp := *cfg

	// 1. Provider headers and APIKeyEnv: provider header names are user-defined, so
	// redact every value rather than relying on a finite list of secret names.
	if cfg.Providers != nil {
		cp.Providers = make([]ProviderConfig, len(cfg.Providers))
		for i, p := range cfg.Providers {
			cp.Providers[i] = p
			if p.Headers != nil {
				cp.Providers[i].Headers = make(map[string]string, len(p.Headers))
				for k := range p.Headers {
					cp.Providers[i].Headers[k] = "<redacted>"
				}
			}
			if p.APIKeyEnv != "" {
				cp.Providers[i].APIKeyEnv = "<redacted>"
			}
		}
	}

	// 2. MCP server headers: redact ALL values
	if cfg.MCPServers != nil {
		cp.MCPServers = make([]MCPServerConfig, len(cfg.MCPServers))
		for i, m := range cfg.MCPServers {
			cp.MCPServers[i] = m
			if m.Headers != nil {
				cp.MCPServers[i].Headers = make(map[string]string, len(m.Headers))
				for k := range m.Headers {
					cp.MCPServers[i].Headers[k] = "<redacted>"
				}
			}
		}
	}

	// 3. Infrastructure URLs and DSNs: redact userinfo/passwords
	cp.Database.URL = redactURL(cfg.Database.URL)
	cp.Redis.URL = redactURL(cfg.Redis.URL)
	cp.Analytics.ClickHouseDSN = redactURL(cfg.Analytics.ClickHouseDSN)

	// 4. Health gossip secret
	if cfg.Health.GossipSecret != "" {
		cp.Health.GossipSecret = "<redacted>"
	}

	// 5. Secrets keys and files
	if cfg.Secrets.LocalKEKFile != "" {
		cp.Secrets.LocalKEKFile = "<redacted>"
	}
	if cfg.Secrets.VaultTokenFile != "" {
		cp.Secrets.VaultTokenFile = "<redacted>"
	}
	if cfg.Secrets.VaultKey != "" {
		cp.Secrets.VaultKey = "<redacted>"
	}
	if len(cfg.Secrets.PreviousKEKs) > 0 {
		cp.Secrets.PreviousKEKs = make([]SecretsConfig, len(cfg.Secrets.PreviousKEKs))
		for i, prev := range cfg.Secrets.PreviousKEKs {
			cp.Secrets.PreviousKEKs[i] = prev
			if prev.LocalKEKFile != "" {
				cp.Secrets.PreviousKEKs[i].LocalKEKFile = "<redacted>"
			}
			if prev.VaultTokenFile != "" {
				cp.Secrets.PreviousKEKs[i].VaultTokenFile = "<redacted>"
			}
			if prev.VaultKey != "" {
				cp.Secrets.PreviousKEKs[i].VaultKey = "<redacted>"
			}
		}
	}

	return &cp
}

// redactURL strips userinfo and secret query params from a connection string/URL.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<redacted>"
	}
	if u.User != nil {
		u.User = url.UserPassword("redacted", "redacted")
	}
	if q := u.Query(); len(q) > 0 {
		changed := false
		for k := range q {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "pass") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
				q.Set(k, "redacted")
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
	}
	return u.String()
}

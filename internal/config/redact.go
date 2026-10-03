// Package config provides enterprise-grade capabilities, configuration, and structural components for the config subsystem.
package config

// Redact sanitizes a populated Config struct by stripping out sensitive
// environment variable references, raw headers, and symmetric key files.
// It returns a safe, shallow copy suitable for serialization, telemetry,
// or unprivileged API exposure without leaking upstream provider credentials.
func Redact(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	if cfg.Providers != nil {
		cp.Providers = make([]ProviderConfig, len(cfg.Providers))
		for i, p := range cfg.Providers {
			cp.Providers[i] = p
			if p.Headers != nil {
				cp.Providers[i].Headers = make(map[string]string, len(p.Headers))
				for k, v := range p.Headers {
					cp.Providers[i].Headers[k] = v
				}
			}
			if p.APIKeyEnv != "" {
				cp.Providers[i].APIKeyEnv = "<redacted>"
			}
		}
	}
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
	if cfg.Secrets.LocalKEKFile != "" {
		cp.Secrets.LocalKEKFile = "<redacted>"
	}
	return &cp
}

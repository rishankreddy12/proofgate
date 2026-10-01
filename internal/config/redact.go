package config

// Redact returns a shallow copy of cfg with sensitive provider headers, API key environment variable names,
// MCP server headers, and secret file paths replaced with "<redacted>".
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

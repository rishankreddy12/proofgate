// Package config loads and validates proofgate.yaml.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/provider"
	"gopkg.in/yaml.v3"
)

var mcpNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers_env"` // header name -> env var holding its value
}

type ServerConfig struct {
	Addr      string `yaml:"addr"`
	AdminAddr string `yaml:"admin_addr"`
}

type ProviderConfig struct {
	Name      string            `yaml:"name"`
	Type      string            `yaml:"type"`
	BaseURL   string            `yaml:"base_url"`
	APIKeyEnv string            `yaml:"api_key_env"`
	Headers   map[string]string `yaml:"headers"`
}

type Price struct {
	Input       float64 `yaml:"input"`
	Output      float64 `yaml:"output"`
	CachedInput float64 `yaml:"cached_input"`
}

type TargetConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BaseDelay   time.Duration `yaml:"base_delay"`
}

type CacheConfig struct {
	Mode           string        `yaml:"mode"`
	Exact          bool          `yaml:"-"`
	ExactRaw       *bool         `yaml:"exact"`
	Semantic       bool          `yaml:"semantic"`
	Threshold      float64       `yaml:"threshold"`
	TTL            time.Duration `yaml:"ttl"`
	Version        int           `yaml:"version"`
	EmbeddingRoute string        `yaml:"embedding_route"`
	PerUser        bool          `yaml:"per_user"`
	MaxEntryBytes  int           `yaml:"max_entry_bytes"`
}

type InjectionGuardConfig struct {
	Enabled   bool    `yaml:"enabled"`
	Threshold float64 `yaml:"threshold"` // default 0.70
	Action    string  `yaml:"action"`    // "block" | "warn", default "block"
}

type PIIGuardConfig struct {
	Enabled bool   `yaml:"enabled"`
	Mode    string `yaml:"mode"` // "redact" | "mask", default "redact"
}

type GuardConfig struct {
	Injection InjectionGuardConfig `yaml:"injection"`
	PII       PIIGuardConfig       `yaml:"pii"`
}

type SmartRouteConfig struct {
	Mode                string  `yaml:"mode"` // "off" | "shadow" | "on", default "off"
	CheapTarget         string  `yaml:"cheap_target"`
	StrongTarget        string  `yaml:"strong_target"`
	MaxTokensForCheap   int     `yaml:"max_tokens_for_cheap"` // default 1500
	KNNEnabled          bool    `yaml:"knn_enabled"`
	KNNRoute            string  `yaml:"knn_route"`
	KNNK                int     `yaml:"knn_k"`
	ConfidenceThreshold float64 `yaml:"confidence_threshold"`
}

type HedgeConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Delay    time.Duration `yaml:"delay"`
	MaxExtra float64       `yaml:"max_extra"` // max share of requests that may be hedged, default 0.10
}

type RouteConfig struct {
	Name              string           `yaml:"name"`
	Targets           []TargetConfig   `yaml:"targets"`
	Strategy          string           `yaml:"strategy"`
	Retry             RetryConfig      `yaml:"retry"`
	Timeout           time.Duration    `yaml:"timeout"`
	StreamIdleTimeout time.Duration    `yaml:"stream_idle_timeout"`
	Embeddings        bool             `yaml:"embeddings"` // route serves /v1/embeddings
	Cache             CacheConfig      `yaml:"cache"`
	Guard             GuardConfig      `yaml:"guard"`
	SmartRoute        SmartRouteConfig `yaml:"smart_route"`
	Hedge             HedgeConfig      `yaml:"hedge"`
}

type Defaults struct {
	MaxTokensReserve int `yaml:"max_tokens_reserve"` // cap on completion tokens pre-charged by the rate limiter
	DefaultMaxTokens int `yaml:"default_max_tokens"`
}

type SLO struct {
	TTFTMs       float64 `yaml:"ttft_ms"`
	MinTPS       float64 `yaml:"min_tps"`
	MaxErrorRate float64 `yaml:"max_error_rate"`
}

type HealthConfig struct {
	Alpha         float64       `yaml:"alpha"`
	Breaches      int           `yaml:"breaches"`
	Recover       time.Duration `yaml:"recover"`
	MinSamples    int           `yaml:"min_samples"`
	ProbeInterval time.Duration `yaml:"probe_interval"`
}

type Config struct {
	Server           ServerConfig      `yaml:"server"`
	Providers        []ProviderConfig  `yaml:"providers"`
	Pricing          map[string]Price  `yaml:"pricing"`
	Routes           []RouteConfig     `yaml:"routes"`
	Defaults         Defaults          `yaml:"defaults"`
	SLOs             map[string]SLO    `yaml:"slos"`
	Health           HealthConfig      `yaml:"health"`
	MCPServers       []MCPServerConfig `yaml:"mcp_servers"`
	MCPInsecureHosts []string          `yaml:"mcp_insecure_hosts"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true) // typos in config keys are errors, not silent defaults
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Server.AdminAddr == "" {
		c.Server.AdminAddr = "127.0.0.1:9090"
	}
	if c.Defaults.MaxTokensReserve == 0 {
		c.Defaults.MaxTokensReserve = 4096
	}
	if c.Defaults.DefaultMaxTokens == 0 {
		c.Defaults.DefaultMaxTokens = 1024
	}
	for i := range c.Routes {
		r := &c.Routes[i]
		if r.Strategy == "" {
			r.Strategy = "fallback"
		}
		if r.Retry.MaxAttempts == 0 {
			r.Retry.MaxAttempts = 2
		}
		if r.Retry.BaseDelay == 0 {
			r.Retry.BaseDelay = 200 * time.Millisecond
		}
		if r.Timeout == 0 {
			r.Timeout = 120 * time.Second
		}
		if r.StreamIdleTimeout == 0 {
			r.StreamIdleTimeout = 30 * time.Second
		}
		cc := &r.Cache
		if cc.Mode == "" {
			cc.Mode = "off"
		}
		cc.Exact = cc.ExactRaw == nil || *cc.ExactRaw
		if cc.Threshold == 0 {
			cc.Threshold = 0.95
		}
		if cc.TTL == 0 {
			cc.TTL = 24 * time.Hour
		}
		if cc.Version == 0 {
			cc.Version = 1
		}
		if cc.MaxEntryBytes == 0 {
			cc.MaxEntryBytes = 64 << 10
		}
		if r.Hedge.MaxExtra == 0 {
			r.Hedge.MaxExtra = 0.10
		}
	}
	if c.Health.Alpha == 0 {
		c.Health.Alpha = 0.2
	}
	if c.Health.Breaches == 0 {
		c.Health.Breaches = 3
	}
	if c.Health.Recover == 0 {
		c.Health.Recover = 30 * time.Second
	}
	if c.Health.MinSamples == 0 {
		c.Health.MinSamples = 10
	}
	if c.Health.ProbeInterval == 0 {
		c.Health.ProbeInterval = 10 * time.Second
	}
}

func (c *Config) validate() error {
	var errs []error
	if len(c.Providers) == 0 {
		errs = append(errs, errors.New("at least one provider is required"))
	}
	if len(c.Routes) == 0 {
		errs = append(errs, errors.New("at least one route is required"))
	}
	provs := map[string]bool{}
	for _, p := range c.Providers {
		if provs[p.Name] {
			errs = append(errs, fmt.Errorf("duplicate provider %q", p.Name))
		}
		provs[p.Name] = true
		switch p.Type {
		case "openai", "anthropic", "gemini":
		default:
			errs = append(errs, fmt.Errorf("provider %q: unknown type %q", p.Name, p.Type))
		}
		if p.BaseURL == "" {
			errs = append(errs, fmt.Errorf("provider %q: base_url is required", p.Name))
		}
	}
	for key := range c.SLOs {
		p, m, ok := strings.Cut(key, "/")
		if !ok || p == "" || m == "" {
			errs = append(errs, fmt.Errorf("slo %q: key must be 'provider/model'", key))
		} else if !provs[p] {
			errs = append(errs, fmt.Errorf("slo %q: unknown provider %q", key, p))
		}
	}
	routes := map[string]bool{}
	embedRoutes := map[string]bool{}
	for _, r := range c.Routes {
		if r.Embeddings {
			embedRoutes[r.Name] = true
		}
	}
	for _, r := range c.Routes {
		if r.Name == "" || strings.Contains(r.Name, "/") {
			errs = append(errs, fmt.Errorf("route name %q must be non-empty and contain no '/'", r.Name))
		}
		if routes[r.Name] {
			errs = append(errs, fmt.Errorf("duplicate route %q", r.Name))
		}
		routes[r.Name] = true
		if len(r.Targets) == 0 {
			errs = append(errs, fmt.Errorf("route %q: needs at least one target", r.Name))
		}
		switch r.Strategy {
		case "fallback", "cheapest":
		default:
			errs = append(errs, fmt.Errorf("route %q: unknown strategy %q", r.Name, r.Strategy))
		}
		for _, t := range r.Targets {
			if !provs[t.Provider] {
				errs = append(errs, fmt.Errorf("route %q: unknown provider %q", r.Name, t.Provider))
			}
			if r.Strategy == "cheapest" {
				if _, ok := c.Pricing[t.Provider+"/"+t.Model]; !ok {
					errs = append(errs, fmt.Errorf("route %q: strategy cheapest needs pricing for %s/%s", r.Name, t.Provider, t.Model))
				}
			}
		}
		cc := r.Cache
		switch cc.Mode {
		case "off", "on", "shadow":
		default:
			errs = append(errs, fmt.Errorf("route %q: unknown cache mode %q", r.Name, cc.Mode))
		}
		if cc.Threshold <= 0 || cc.Threshold > 1 {
			errs = append(errs, fmt.Errorf("route %q: cache threshold must be in (0, 1]", r.Name))
		}
		if cc.Semantic {
			if cc.EmbeddingRoute == "" {
				errs = append(errs, fmt.Errorf("route %q: semantic cache needs embedding_route", r.Name))
			} else if !embedRoutes[cc.EmbeddingRoute] {
				errs = append(errs, fmt.Errorf("route %q: embedding_route %q must name a route with embeddings: true", r.Name, cc.EmbeddingRoute))
			}
		}
	}
	mcpNames := map[string]bool{}
	insecure := map[string]bool{}
	for _, h := range c.MCPInsecureHosts {
		insecure[h] = true
	}
	for _, s := range c.MCPServers {
		if !mcpNameRe.MatchString(s.Name) {
			errs = append(errs, fmt.Errorf("mcp server name %q must match ^[a-z0-9_-]{1,32}$", s.Name))
		}
		if mcpNames[s.Name] {
			errs = append(errs, fmt.Errorf("duplicate mcp server %q", s.Name))
		}
		mcpNames[s.Name] = true
		u, err := url.Parse(s.URL)
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp server %q: invalid url %q: %w", s.Name, s.URL, err))
			continue
		}
		hostname := u.Hostname()
		if u.Scheme == "https" {
			// ok
		} else if u.Scheme == "http" {
			if hostname != "localhost" && !strings.HasSuffix(hostname, ".local") && !insecure[hostname] {
				errs = append(errs, fmt.Errorf("mcp server %q: url must be https (http allowed only for localhost, .local or mcp_insecure_hosts)", s.Name))
			}
		} else {
			errs = append(errs, fmt.Errorf("mcp server %q: url must have https or http scheme", s.Name))
		}
	}
	return errors.Join(errs...)
}

// ProviderSpecs resolves API keys from the environment. Plan 5 adds encrypted database credentials.
func (c *Config) ProviderSpecs(getenv func(string) string) []provider.Spec {
	out := make([]provider.Spec, 0, len(c.Providers))
	for _, p := range c.Providers {
		key := ""
		if p.APIKeyEnv != "" {
			key = getenv(p.APIKeyEnv)
		}
		out = append(out, provider.Spec{Name: p.Name, Type: p.Type, BaseURL: p.BaseURL, APIKey: key, Headers: p.Headers})
	}
	return out
}

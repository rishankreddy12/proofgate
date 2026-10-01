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
	Addr                string        `yaml:"addr"`
	AdminAddr           string        `yaml:"admin_addr"`
	MaxRequestBodyBytes int64         `yaml:"max_request_body_bytes"`
	ReadHeaderTimeout   time.Duration `yaml:"read_header_timeout"`
	IdleTimeout         time.Duration `yaml:"idle_timeout"`
	DrainTimeout        time.Duration `yaml:"drain_timeout"`
}

type DatabaseConfig struct {
	URL      string `yaml:"url"`
	MaxConns int    `yaml:"max_conns"`
}

type RedisConfig struct {
	URL string `yaml:"url"`
}

type BatcherConfig struct {
	Capacity      int           `yaml:"capacity"`
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
}

type AnalyticsConfig struct {
	ClickHouseDSN string        `yaml:"clickhouse_dsn"`
	UsageBatcher  BatcherConfig `yaml:"usage_batcher"`
	MCPBatcher    BatcherConfig `yaml:"mcp_batcher"`
	FlushTimeout  time.Duration `yaml:"flush_timeout"`
}

type TelemetryConfig struct {
	OTLPEndpoint string `yaml:"otlp_endpoint"`
	ServiceName  string `yaml:"service_name"`
}

type BreakersConfig struct {
	Threshold int           `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

type AuthConfig struct {
	CacheTTL         time.Duration `yaml:"cache_ttl"`
	NegativeCacheTTL time.Duration `yaml:"negative_cache_ttl"`
	MaxCachedKeys    int           `yaml:"max_cached_keys"`
}

type MCPProxyConfig struct {
	MaxRequestBodyBytes int64 `yaml:"max_request_body_bytes"`
}

type ProviderConfig struct {
	Name                 string            `yaml:"name"`
	Type                 string            `yaml:"type"`
	BaseURL              string            `yaml:"base_url"`
	APIKeyEnv            string            `yaml:"api_key_env"`
	APIKeyDB             bool              `yaml:"api_key_db"`
	Headers              map[string]string `yaml:"headers"`
	AllowInsecureBaseURL bool              `yaml:"allow_insecure_base_url"`
}

type SecretsConfig struct {
	KEK            string                   `yaml:"kek"`            // "local" | "vault"
	LocalKEKFile   string                   `yaml:"local_kek_file"` // 32 random bytes, base64
	VaultAddr      string                   `yaml:"vault_addr"`
	VaultKey       string                   `yaml:"vault_key"`      // transit key name
	VaultAuth      string                   `yaml:"vault_auth"`     // "token" (VAULT_TOKEN env) | "kubernetes"
	VaultRole      string                   `yaml:"vault_role"`
	VaultTokenFile string                   `yaml:"vault_token_file"`
	CacheTTL       time.Duration            `yaml:"cache_ttl"`      // default 60s
	TenantKEKs     map[string]SecretsConfig `yaml:"tenant_keks"`
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
	Mode                    string        `yaml:"mode"`
	Exact                   bool          `yaml:"-"`
	ExactRaw                *bool         `yaml:"exact"`
	Semantic                bool          `yaml:"semantic"`
	Threshold               float64       `yaml:"threshold"`
	TTL                     time.Duration `yaml:"ttl"`
	Version                 int           `yaml:"version"`
	EmbeddingRoute          string        `yaml:"embedding_route"`
	PerUser                 bool          `yaml:"per_user"`
	MaxEntryBytes           int           `yaml:"max_entry_bytes"`
	AcknowledgeUncalibrated bool          `yaml:"acknowledge_uncalibrated"`
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
	MaxTokensReserve    int           `yaml:"max_tokens_reserve"` // cap on completion tokens pre-charged by the rate limiter
	DefaultMaxTokens    int           `yaml:"default_max_tokens"`
	AgentRunTTL         time.Duration `yaml:"agent_run_ttl"`
	AgentLoopRepeats    int           `yaml:"agent_loop_repeats"`
	AgentLoopWindow     int           `yaml:"agent_loop_window"`
	AgentFuzzyThreshold float64       `yaml:"agent_fuzzy_threshold"`
	EmbedderCacheSize   int           `yaml:"embedder_cache_size"`
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
	ProbeTimeout  time.Duration `yaml:"probe_timeout"`
	GossipAddr    string        `yaml:"gossip_addr"`
	GossipPeers   []string      `yaml:"gossip_peers"`
}

type CapabilityConfig struct {
	MaxContextTokens int   `yaml:"max_context_tokens"`
	SupportsVision   bool  `yaml:"supports_vision"`
	SupportsTools    *bool `yaml:"supports_tools"`
}

type AdminAuthConfig struct {
	Enabled            bool          `yaml:"enabled"`
	MaxLoginAttempts   int           `yaml:"max_login_attempts"`
	LockoutDuration    time.Duration `yaml:"lockout_duration"`
	SessionIdleTimeout time.Duration `yaml:"session_idle_timeout"`
	SessionAbsTimeout  time.Duration `yaml:"session_absolute_timeout"`
}

type BudgetConfig struct {
	Reserve        string `yaml:"reserve"`         // "strict" | "off", default "strict"
	RequirePricing *bool  `yaml:"require_pricing"` // default true
}

func (b BudgetConfig) IsReserveStrict() bool {
	return strings.ToLower(b.Reserve) != "off"
}

func (b BudgetConfig) IsRequirePricing() bool {
	if b.RequirePricing == nil {
		return true
	}
	return *b.RequirePricing
}

type Config struct {
	Server           ServerConfig                `yaml:"server"`
	Database         DatabaseConfig              `yaml:"database"`
	Redis            RedisConfig                 `yaml:"redis"`
	Analytics        AnalyticsConfig             `yaml:"analytics"`
	Telemetry        TelemetryConfig             `yaml:"telemetry"`
	Breakers         BreakersConfig              `yaml:"breakers"`
	Auth             AuthConfig                  `yaml:"auth"`
	AdminAuth        AdminAuthConfig             `yaml:"admin_auth"`
	MCP              MCPProxyConfig              `yaml:"mcp"`
	Providers        []ProviderConfig            `yaml:"providers"`
	Pricing          map[string]Price            `yaml:"pricing"`
	Budget           BudgetConfig                `yaml:"budget"`
	Proof            ProofConfig                 `yaml:"proof"`
	Routes           []RouteConfig               `yaml:"routes"`
	Capabilities     map[string]CapabilityConfig `yaml:"capabilities"`
	Defaults         Defaults                    `yaml:"defaults"`
	SLOs             map[string]SLO              `yaml:"slos"`
	Health           HealthConfig                `yaml:"health"`
	MCPServers       []MCPServerConfig           `yaml:"mcp_servers"`
	MCPInsecureHosts []string                    `yaml:"mcp_insecure_hosts"`
	Secrets          SecretsConfig               `yaml:"secrets"`
	WatcherInterval  time.Duration               `yaml:"watcher_interval"`
	OverrideInterval time.Duration               `yaml:"override_interval"`
}

type ProofConfig struct {
	MinThreshold float64 `yaml:"min_threshold"` // default 0.86
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
	c.applyEnvOverrides()
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyEnvOverrides() {
	if env := os.Getenv("DATABASE_URL"); env != "" && c.Database.URL == "" {
		c.Database.URL = env
	}
	if env := os.Getenv("REDIS_URL"); env != "" && c.Redis.URL == "" {
		c.Redis.URL = env
	}
	if env := os.Getenv("CLICKHOUSE_DSN"); env != "" && c.Analytics.ClickHouseDSN == "" {
		c.Analytics.ClickHouseDSN = env
	}
	if env := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); env != "" && c.Telemetry.OTLPEndpoint == "" {
		c.Telemetry.OTLPEndpoint = env
	}
	if env := os.Getenv("PROOFGATE_ADDR"); env != "" {
		c.Server.Addr = env
	}
	if env := os.Getenv("PROOFGATE_ADMIN_ADDR"); env != "" {
		c.Server.AdminAddr = env
	}
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Server.AdminAddr == "" {
		c.Server.AdminAddr = "127.0.0.1:9090"
	}
	if c.Server.MaxRequestBodyBytes == 0 {
		c.Server.MaxRequestBodyBytes = 10 << 20 // 10MB
	}
	if c.Server.ReadHeaderTimeout == 0 {
		c.Server.ReadHeaderTimeout = 10 * time.Second
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = 120 * time.Second
	}
	if c.Server.DrainTimeout == 0 {
		c.Server.DrainTimeout = 30 * time.Second
	}
	if c.Database.MaxConns == 0 {
		c.Database.MaxConns = 20
	}
	if c.Analytics.UsageBatcher.Capacity == 0 {
		c.Analytics.UsageBatcher.Capacity = 50_000
	}
	if c.Analytics.UsageBatcher.BatchSize == 0 {
		c.Analytics.UsageBatcher.BatchSize = 5_000
	}
	if c.Analytics.UsageBatcher.FlushInterval == 0 {
		c.Analytics.UsageBatcher.FlushInterval = time.Second
	}
	if c.Analytics.MCPBatcher.Capacity == 0 {
		c.Analytics.MCPBatcher.Capacity = 20_000
	}
	if c.Analytics.MCPBatcher.BatchSize == 0 {
		c.Analytics.MCPBatcher.BatchSize = 2_000
	}
	if c.Analytics.MCPBatcher.FlushInterval == 0 {
		c.Analytics.MCPBatcher.FlushInterval = time.Second
	}
	if c.Analytics.FlushTimeout == 0 {
		c.Analytics.FlushTimeout = 10 * time.Second
	}
	if c.Telemetry.ServiceName == "" {
		c.Telemetry.ServiceName = "proofgate"
	}
	if c.Breakers.Threshold == 0 {
		c.Breakers.Threshold = 5
	}
	if c.Breakers.Cooldown == 0 {
		c.Breakers.Cooldown = 30 * time.Second
	}
	if c.Auth.CacheTTL == 0 {
		c.Auth.CacheTTL = 30 * time.Second
	}
	if c.Auth.NegativeCacheTTL == 0 {
		c.Auth.NegativeCacheTTL = 5 * time.Second
	}
	if c.Auth.MaxCachedKeys == 0 {
		c.Auth.MaxCachedKeys = 100_000
	}
	if c.MCP.MaxRequestBodyBytes == 0 {
		c.MCP.MaxRequestBodyBytes = 4 << 20 // 4MB
	}
	if c.Health.ProbeTimeout == 0 {
		c.Health.ProbeTimeout = 10 * time.Second
	}
	if c.Secrets.VaultAuth == "kubernetes" && c.Secrets.VaultTokenFile == "" {
		c.Secrets.VaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	if c.WatcherInterval == 0 {
		c.WatcherInterval = 5 * time.Second
	}
	if c.OverrideInterval == 0 {
		c.OverrideInterval = 10 * time.Second
	}
	if c.Defaults.MaxTokensReserve == 0 {
		c.Defaults.MaxTokensReserve = 4096
	}
	if c.Defaults.DefaultMaxTokens == 0 {
		c.Defaults.DefaultMaxTokens = 1024
	}
	if c.Defaults.AgentRunTTL == 0 {
		c.Defaults.AgentRunTTL = time.Hour
	}
	if c.Defaults.AgentLoopRepeats == 0 {
		c.Defaults.AgentLoopRepeats = 3
	}
	if c.Defaults.AgentLoopWindow == 0 {
		c.Defaults.AgentLoopWindow = 20
	}
	if c.Defaults.AgentFuzzyThreshold == 0 {
		c.Defaults.AgentFuzzyThreshold = 0.95
	}
	if c.Defaults.EmbedderCacheSize == 0 {
		c.Defaults.EmbedderCacheSize = 10_000
	}
	if c.AdminAuth.MaxLoginAttempts == 0 {
		c.AdminAuth.MaxLoginAttempts = 5
	}
	if c.AdminAuth.LockoutDuration == 0 {
		c.AdminAuth.LockoutDuration = 15 * time.Minute
	}
	if c.AdminAuth.SessionIdleTimeout == 0 {
		c.AdminAuth.SessionIdleTimeout = 30 * time.Minute
	}
	if c.AdminAuth.SessionAbsTimeout == 0 {
		c.AdminAuth.SessionAbsTimeout = 12 * time.Hour
	}
	if c.Budget.Reserve == "" {
		c.Budget.Reserve = "strict"
	}
	if c.Proof.MinThreshold == 0 {
		c.Proof.MinThreshold = 0.86
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
	if c.Secrets.KEK == "" {
		c.Secrets.KEK = "local"
	}
	if c.Secrets.CacheTTL == 0 {
		c.Secrets.CacheTTL = 60 * time.Second
	}
}

func (c *Config) validate() error {
	var errs []error
	if c.Server.MaxRequestBodyBytes < 0 {
		errs = append(errs, errors.New("server.max_request_body_bytes must be >= 0"))
	}
	if c.Server.ReadHeaderTimeout < 0 {
		errs = append(errs, errors.New("server.read_header_timeout must be >= 0"))
	}
	if c.Server.IdleTimeout < 0 {
		errs = append(errs, errors.New("server.idle_timeout must be >= 0"))
	}
	if c.Server.DrainTimeout < 0 {
		errs = append(errs, errors.New("server.drain_timeout must be >= 0"))
	}
	if c.Database.MaxConns < 0 {
		errs = append(errs, errors.New("database.max_conns must be >= 0"))
	}
	if c.Analytics.UsageBatcher.Capacity < 0 || c.Analytics.UsageBatcher.BatchSize < 0 || c.Analytics.UsageBatcher.FlushInterval < 0 {
		errs = append(errs, errors.New("analytics.usage_batcher parameters must be >= 0"))
	}
	if c.Analytics.MCPBatcher.Capacity < 0 || c.Analytics.MCPBatcher.BatchSize < 0 || c.Analytics.MCPBatcher.FlushInterval < 0 {
		errs = append(errs, errors.New("analytics.mcp_batcher parameters must be >= 0"))
	}
	if c.Analytics.FlushTimeout < 0 {
		errs = append(errs, errors.New("analytics.flush_timeout must be >= 0"))
	}
	if c.Breakers.Threshold < 0 {
		errs = append(errs, errors.New("breakers.threshold must be >= 0"))
	}
	if c.Breakers.Cooldown < 0 {
		errs = append(errs, errors.New("breakers.cooldown must be >= 0"))
	}
	if c.Auth.CacheTTL < 0 || c.Auth.NegativeCacheTTL < 0 || c.Auth.MaxCachedKeys < 0 {
		errs = append(errs, errors.New("auth settings must be >= 0"))
	}
	if c.MCP.MaxRequestBodyBytes < 0 {
		errs = append(errs, errors.New("mcp.max_request_body_bytes must be >= 0"))
	}
	if c.Defaults.MaxTokensReserve < 0 || c.Defaults.DefaultMaxTokens < 0 || c.Defaults.EmbedderCacheSize < 0 {
		errs = append(errs, errors.New("defaults limits must be >= 0"))
	}
	if c.Defaults.AgentLoopRepeats < 0 || c.Defaults.AgentLoopWindow < 0 {
		errs = append(errs, errors.New("defaults.agent_loop settings must be >= 0"))
	}
	if c.Defaults.AgentFuzzyThreshold < 0 || c.Defaults.AgentFuzzyThreshold > 1.0 {
		errs = append(errs, errors.New("defaults.agent_fuzzy_threshold must be between 0 and 1.0"))
	}
	if c.Health.ProbeTimeout < 0 {
		errs = append(errs, errors.New("health.probe_timeout must be >= 0"))
	}
	if c.AdminAuth.SessionIdleTimeout < time.Minute {
		errs = append(errs, errors.New("admin_auth.session_idle_timeout minimum is 1m"))
	}
	if c.AdminAuth.SessionAbsTimeout < c.AdminAuth.SessionIdleTimeout {
		errs = append(errs, errors.New("admin_auth.session_absolute_timeout must be >= session_idle_timeout"))
	}
	if c.AdminAuth.MaxLoginAttempts < 1 {
		errs = append(errs, errors.New("admin_auth.max_login_attempts must be >= 1"))
	}
	if c.AdminAuth.LockoutDuration < time.Minute {
		errs = append(errs, errors.New("admin_auth.lockout_duration minimum is 1m"))
	}
	if c.WatcherInterval < 0 || c.OverrideInterval < 0 {
		errs = append(errs, errors.New("watcher_interval and override_interval must be >= 0"))
	}
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
		} else {
			u, err := url.Parse(p.BaseURL)
			if err != nil {
				errs = append(errs, fmt.Errorf("provider %q: base_url %q is invalid: %w", p.Name, p.BaseURL, err))
			} else {
				h := u.Hostname()
				isLoopback := h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".local") || strings.Contains(h, "docker") || strings.HasPrefix(p.Name, "mock") || strings.Contains(h, "ollama") || !strings.Contains(h, ".")
				if u.Scheme != "http" && u.Scheme != "https" {
					errs = append(errs, fmt.Errorf("provider %q: base_url %q must have http or https scheme", p.Name, p.BaseURL))
				} else if u.Scheme == "http" && !isLoopback && !p.AllowInsecureBaseURL {
					errs = append(errs, fmt.Errorf("provider %q: base_url %q must use https unless loopback or allow_insecure_base_url is true", p.Name, p.BaseURL))
				}
			}
		}
		if p.APIKeyEnv != "" && p.APIKeyDB {
			errs = append(errs, fmt.Errorf("provider %q: api_key_env and api_key_db are mutually exclusive", p.Name))
		}
	}
	for key := range c.Pricing {
		p, m, ok := strings.Cut(key, "/")
		if !ok || p == "" || m == "" {
			errs = append(errs, fmt.Errorf("pricing %q: key must be 'provider/model'", key))
		} else if !provs[p] {
			errs = append(errs, fmt.Errorf("pricing %q: unknown provider %q", key, p))
		}
	}
	if c.Health.Alpha < 0 || c.Health.Alpha > 1.0 {
		errs = append(errs, errors.New("health.alpha must be in [0, 1]"))
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
		if r.Retry.MaxAttempts < 0 {
			errs = append(errs, fmt.Errorf("route %q: retry.max_attempts must be >= 0", r.Name))
		}
		if r.Retry.BaseDelay < 0 {
			errs = append(errs, fmt.Errorf("route %q: retry.base_delay must be >= 0", r.Name))
		}
		if r.Timeout < 0 {
			errs = append(errs, fmt.Errorf("route %q: timeout must be >= 0", r.Name))
		}
		if r.StreamIdleTimeout < 0 {
			errs = append(errs, fmt.Errorf("route %q: stream_idle_timeout must be >= 0", r.Name))
		}
		if r.Hedge.Enabled {
			if r.Hedge.MaxExtra < 0 || r.Hedge.MaxExtra > 1.0 {
				errs = append(errs, fmt.Errorf("route %q: hedge.max_extra must be in [0, 1]", r.Name))
			}
		}
		if r.Cache.MaxEntryBytes < 0 {
			errs = append(errs, fmt.Errorf("route %q: cache.max_entry_bytes must be >= 0", r.Name))
		}
		switch r.Guard.Injection.Action {
		case "", "block", "warn":
		default:
			errs = append(errs, fmt.Errorf("route %q: unknown injection guard action %q", r.Name, r.Guard.Injection.Action))
		}
		switch r.Guard.PII.Mode {
		case "", "redact", "mask":
		default:
			errs = append(errs, fmt.Errorf("route %q: unknown pii guard mode %q", r.Name, r.Guard.PII.Mode))
		}
		switch r.SmartRoute.Mode {
		case "", "off", "shadow", "on":
		default:
			errs = append(errs, fmt.Errorf("route %q: unknown smart_route mode %q", r.Name, r.SmartRoute.Mode))
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

// ProviderSpecs resolves API keys from the environment and database key resolver.
func (c *Config) ProviderSpecs(getenv func(string) string, keys func(provider string) provider.KeyFunc) []provider.Spec {
	out := make([]provider.Spec, 0, len(c.Providers))
	for _, p := range c.Providers {
		key := ""
		if p.APIKeyEnv != "" {
			key = getenv(p.APIKeyEnv)
		}
		var kf provider.KeyFunc
		if p.APIKeyDB && keys != nil {
			kf = keys(p.Name)
		}
		out = append(out, provider.Spec{Name: p.Name, Type: p.Type, BaseURL: p.BaseURL, APIKey: key, KeyFunc: kf, Headers: p.Headers})
	}
	return out
}

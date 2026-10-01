package config

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadValidWithDefaults(t *testing.T) {
	c, err := Load("testdata/valid.yaml")
	require.NoError(t, err)
	require.Equal(t, ":8080", c.Server.Addr)
	require.Equal(t, "127.0.0.1:9090", c.Server.AdminAddr)
	d := c.Routes[0]
	require.Equal(t, "fallback", d.Strategy)
	require.Equal(t, 2, d.Retry.MaxAttempts)
	require.Equal(t, 200*time.Millisecond, d.Retry.BaseDelay)
	require.Equal(t, 120*time.Second, d.Timeout)
	require.Equal(t, 30*time.Second, d.StreamIdleTimeout)
	require.Equal(t, 50*time.Millisecond, c.Routes[1].Retry.BaseDelay)
	require.Equal(t, 1.0, c.Pricing["anthropic/claude-haiku-4-5"].Input)

	specs := c.ProviderSpecs(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk-ant"
		}
		return ""
	}, nil)
	require.Equal(t, "sk-ant", specs[1].APIKey)
	require.Equal(t, "", specs[0].APIKey)
}

func writeCfg(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"unknown provider": `
providers: [{name: a, type: openai, base_url: http://x}]
routes: [{name: r, targets: [{provider: b, model: m}]}]`,
		"bad type": `
providers: [{name: a, type: bedrock, base_url: http://x}]
routes: [{name: r, targets: [{provider: a, model: m}]}]`,
		"cheapest needs pricing": `
providers: [{name: a, type: openai, base_url: http://x}]
routes: [{name: r, strategy: cheapest, targets: [{provider: a, model: m}]}]`,
		"slash in route name": `
providers: [{name: a, type: openai, base_url: http://x}]
routes: [{name: a/b, targets: [{provider: a, model: m}]}]`,
		"no routes": `
providers: [{name: a, type: openai, base_url: http://x}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeCfg(t, body))
			require.Error(t, err)
		})
	}
}

func TestWatcherReloadsOnlyValidChanges(t *testing.T) {
	good, _ := os.ReadFile("testdata/valid.yaml")
	p := writeCfg(t, string(good))
	var calls atomic.Int32
	w := NewWatcher(p, 20*time.Millisecond, func(*Config) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	time.Sleep(60 * time.Millisecond)
	require.EqualValues(t, 0, calls.Load(), "no change, no callback")

	require.NoError(t, os.WriteFile(p, []byte("providers: ["), 0o600)) // invalid YAML
	time.Sleep(60 * time.Millisecond)
	require.EqualValues(t, 0, calls.Load(), "invalid config must be ignored")

	require.NoError(t, os.WriteFile(p, append(good, []byte("\n# edit\n")...), 0o600))
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, 10*time.Millisecond)
}

func TestOperationalConfigDefaults(t *testing.T) {
	c, err := Parse([]byte(`
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]
`))
	require.NoError(t, err)
	// Server defaults
	require.Equal(t, int64(10<<20), c.Server.MaxRequestBodyBytes)
	require.Equal(t, 10*time.Second, c.Server.ReadHeaderTimeout)
	require.Equal(t, 120*time.Second, c.Server.IdleTimeout)
	require.Equal(t, 30*time.Second, c.Server.DrainTimeout)

	// Database defaults
	require.Equal(t, 20, c.Database.MaxConns)

	// Analytics defaults
	require.Equal(t, 50_000, c.Analytics.UsageBatcher.Capacity)
	require.Equal(t, 5_000, c.Analytics.UsageBatcher.BatchSize)
	require.Equal(t, time.Second, c.Analytics.UsageBatcher.FlushInterval)
	require.Equal(t, 20_000, c.Analytics.MCPBatcher.Capacity)
	require.Equal(t, 2_000, c.Analytics.MCPBatcher.BatchSize)
	require.Equal(t, time.Second, c.Analytics.MCPBatcher.FlushInterval)
	require.Equal(t, 10*time.Second, c.Analytics.FlushTimeout)

	// Breakers defaults
	require.Equal(t, 5, c.Breakers.Threshold)
	require.Equal(t, 30*time.Second, c.Breakers.Cooldown)

	// Auth defaults
	require.Equal(t, 30*time.Second, c.Auth.CacheTTL)
	require.Equal(t, 5*time.Second, c.Auth.NegativeCacheTTL)
	require.Equal(t, 100_000, c.Auth.MaxCachedKeys)

	// MCP defaults
	require.Equal(t, int64(4<<20), c.MCP.MaxRequestBodyBytes)

	// Defaults block
	require.Equal(t, time.Hour, c.Defaults.AgentRunTTL)
	require.Equal(t, 3, c.Defaults.AgentLoopRepeats)
	require.Equal(t, 20, c.Defaults.AgentLoopWindow)
	require.Equal(t, 0.95, c.Defaults.AgentFuzzyThreshold)
	require.Equal(t, 10_000, c.Defaults.EmbedderCacheSize)

	// Intervals
	require.Equal(t, 5*time.Second, c.WatcherInterval)
	require.Equal(t, 10*time.Second, c.OverrideInterval)
	require.Equal(t, 10*time.Second, c.Health.ProbeTimeout)
}

func TestOperationalConfigValidation(t *testing.T) {
	cases := map[string]string{
		"negative server max body": `
server: {max_request_body_bytes: -1}
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]`,
		"negative db max conns": `
database: {max_conns: -5}
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]`,
		"negative breaker threshold": `
breakers: {threshold: -1}
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]`,
		"invalid fuzzy threshold": `
defaults: {agent_fuzzy_threshold: 1.5}
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(body))
			require.Error(t, err)
		})
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://custom:pass@dbhost:5432/customdb")
	t.Setenv("REDIS_URL", "redis://redishost:6380/2")
	t.Setenv("CLICKHOUSE_DSN", "clickhouse://chhost:9001/chdb")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otelhost:4318")
	t.Setenv("PROOFGATE_ADDR", ":18080")
	t.Setenv("PROOFGATE_ADMIN_ADDR", "0.0.0.0:19090")
	t.Setenv("PROOFGATE_METRICS_ADDR", "127.0.0.1:19091")
	t.Setenv("PROOFGATE_ENABLE_PPROF", "true")

	c, err := Parse([]byte(`
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]
`))
	require.NoError(t, err)
	require.Equal(t, "postgres://custom:pass@dbhost:5432/customdb", c.Database.URL)
	require.Equal(t, "redis://redishost:6380/2", c.Redis.URL)
	require.Equal(t, "clickhouse://chhost:9001/chdb", c.Analytics.ClickHouseDSN)
	require.Equal(t, "http://otelhost:4318", c.Telemetry.OTLPEndpoint)
	require.Equal(t, ":18080", c.Server.Addr)
	require.Equal(t, "0.0.0.0:19090", c.Server.AdminAddr)
	require.Equal(t, "127.0.0.1:19091", c.Server.MetricsAddr)
	require.True(t, c.Server.EnablePprof)
}

func TestIsLoopbackAddr(t *testing.T) {
	loopbacks := []string{
		"127.0.0.1:9090",
		"127.0.0.2:8080",
		"localhost:9090",
		"[::1]:9090",
		"127.0.0.1",
		"localhost",
		"::1",
	}
	for _, a := range loopbacks {
		require.True(t, IsLoopbackAddr(a), "expected %q to be loopback", a)
	}

	nonLoopbacks := []string{
		"0.0.0.0:9090",
		":9090",
		"[::]:9090",
		"192.168.1.1:9090",
		"example.com:9090",
		"0.0.0.0",
		"",
	}
	for _, a := range nonLoopbacks {
		require.False(t, IsLoopbackAddr(a), "expected %q to NOT be loopback", a)
	}
}

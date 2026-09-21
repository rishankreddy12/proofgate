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

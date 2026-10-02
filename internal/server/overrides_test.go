package server

import (
	"testing"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestApplyOverrides(t *testing.T) {
	cfg, err := config.Parse([]byte(`
providers: [{name: a, type: openai, base_url: "http://a"}]
routes:
  - {name: embed, embeddings: true, targets: [{provider: a, model: e}]}
  - name: faq
    cache: {mode: shadow, semantic: true, embedding_route: embed, threshold: 0.95}
    smart_route: {mode: on, cheap_target: mock-a/small, strong_target: mock-b/large}
    targets: [{provider: a, model: m}]
`))
	require.NoError(t, err)

	out, errs := ApplyOverrides(cfg, []store.Override{
		{Route: "faq", Key: "cache.mode", Value: "on"},
		{Route: "faq", Key: "cache.threshold", Value: "0.91"},
		{Route: "faq", Key: "smart_route.mode", Value: "off"},
		{Route: "faq", Key: "cache.audit_sample_rate", Value: "0.02"},
		// Invalid entries
		{Route: "faq", Key: "cache.threshold", Value: "banana"},
		{Route: "faq", Key: "smart_route.mode", Value: "turbo"},
		{Route: "faq", Key: "cache.audit_sample_rate", Value: "2.5"},
		{Route: "nope", Key: "cache.mode", Value: "on"},
		{Route: "faq", Key: "unknown.key", Value: "val"},
	})
	require.Len(t, errs, 5)
	require.Equal(t, "on", out.Routes[1].Cache.Mode)
	require.Equal(t, 0.91, out.Routes[1].Cache.Threshold)
	require.Equal(t, "off", out.Routes[1].SmartRoute.Mode)
	require.Equal(t, 0.02, out.Routes[1].Cache.AuditSampleRate)
	require.Equal(t, "shadow", cfg.Routes[1].Cache.Mode, "input config is not mutated")
	require.Equal(t, "on", cfg.Routes[1].SmartRoute.Mode, "input config is not mutated")
}

func TestApplyOverrides_Table(t *testing.T) {
	cfg, err := config.Parse([]byte(`
providers: [{name: a, type: openai, base_url: "http://a"}]
routes:
  - name: test
    cache: {mode: shadow, threshold: 0.86}
    smart_route: {mode: shadow}
    targets: [{provider: a, model: m}]
`))
	require.NoError(t, err)

	tests := []struct {
		name      string
		key       string
		val       string
		wantErr   bool
		checkFunc func(t *testing.T, r *config.RouteConfig)
	}{
		{
			name: "valid cache.mode on",
			key:  "cache.mode", val: "on",
			checkFunc: func(t *testing.T, r *config.RouteConfig) { require.Equal(t, "on", r.Cache.Mode) },
		},
		{
			name: "valid cache.mode off",
			key:  "cache.mode", val: "off",
			checkFunc: func(t *testing.T, r *config.RouteConfig) { require.Equal(t, "off", r.Cache.Mode) },
		},
		{
			name: "invalid cache.mode",
			key:  "cache.mode", val: "invalid",
			wantErr: true,
		},
		{
			name: "valid smart_route.mode off",
			key:  "smart_route.mode", val: "off",
			checkFunc: func(t *testing.T, r *config.RouteConfig) { require.Equal(t, "off", r.SmartRoute.Mode) },
		},
		{
			name: "valid smart_route.mode on",
			key:  "smart_route.mode", val: "on",
			checkFunc: func(t *testing.T, r *config.RouteConfig) { require.Equal(t, "on", r.SmartRoute.Mode) },
		},
		{
			name: "invalid smart_route.mode",
			key:  "smart_route.mode", val: "bad",
			wantErr: true,
		},
		{
			name: "valid cache.threshold",
			key:  "cache.threshold", val: "0.95",
			checkFunc: func(t *testing.T, r *config.RouteConfig) { require.InDelta(t, 0.95, r.Cache.Threshold, 1e-6) },
		},
		{
			name: "invalid cache.threshold non-number",
			key:  "cache.threshold", val: "abc",
			wantErr: true,
		},
		{
			name: "invalid cache.threshold out of bounds",
			key:  "cache.threshold", val: "1.5",
			wantErr: true,
		},
		{
			name: "valid cache.audit_sample_rate",
			key:  "cache.audit_sample_rate", val: "0.05",
			checkFunc: func(t *testing.T, r *config.RouteConfig) { require.InDelta(t, 0.05, r.Cache.AuditSampleRate, 1e-6) },
		},
		{
			name: "invalid cache.audit_sample_rate negative",
			key:  "cache.audit_sample_rate", val: "-0.1",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, errs := ApplyOverrides(cfg, []store.Override{{Route: "test", Key: tc.key, Value: tc.val}})
			if tc.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
				tc.checkFunc(t, &out.Routes[0])
			}
		})
	}
}

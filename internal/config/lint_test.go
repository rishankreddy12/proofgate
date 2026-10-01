package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLint_CacheCalibration(t *testing.T) {
	// 1. cache.mode: on without calibration should fail lint
	rawUncalibrated := `
providers:
  - name: mock-a
    type: openai
    base_url: http://localhost:8081/v1
pricing:
  mock-a/m1: {input: 1, output: 2}
routes:
  - name: default
    targets: [{provider: mock-a, model: m1}]
    cache:
      mode: on
      exact: true
      threshold: 0.90
`
	cfg1, err := config.Parse([]byte(rawUncalibrated))
	require.NoError(t, err)
	issues1 := config.Lint(cfg1)
	require.NotEmpty(t, issues1)
	foundCalibError := false
	for _, issue := range issues1 {
		if issue.Rule == "cache/calibration" && issue.Severity == config.SeverityError {
			foundCalibError = true
		}
	}
	require.True(t, foundCalibError, "cache.mode: on without calibration must produce a cache/calibration error")

	// 2. acknowledge_uncalibrated: true should clear calibration error
	rawAcknowledged := `
providers:
  - name: mock-a
    type: openai
    base_url: http://localhost:8081/v1
pricing:
  mock-a/m1: {input: 1, output: 2}
routes:
  - name: default
    targets: [{provider: mock-a, model: m1}]
    cache:
      mode: on
      exact: true
      threshold: 0.90
      acknowledge_uncalibrated: true
`
	cfg2, err := config.Parse([]byte(rawAcknowledged))
	require.NoError(t, err)
	issues2 := config.Lint(cfg2)
	for _, issue := range issues2 {
		require.NotEqual(t, "cache/calibration", issue.Rule)
	}

	// 3. semantic cache threshold below floor (0.86) produces warning
	rawLowThreshold := `
providers:
  - name: mock-a
    type: openai
    base_url: http://localhost:8081/v1
pricing:
  mock-a/m1: {input: 1, output: 2}
  mock-a/emb: {input: 0.01, output: 0}
routes:
  - name: default
    targets: [{provider: mock-a, model: m1}]
    cache:
      mode: shadow
      semantic: true
      threshold: 0.75
      embedding_route: embed
  - name: embed
    embeddings: true
    targets: [{provider: mock-a, model: emb}]
`
	cfg3, err := config.Parse([]byte(rawLowThreshold))
	require.NoError(t, err)
	issues3 := config.Lint(cfg3)
	foundThresholdWarn := false
	for _, issue := range issues3 {
		if issue.Rule == "cache/threshold" {
			foundThresholdWarn = true
		}
	}
	require.True(t, foundThresholdWarn, "threshold below 0.86 must trigger a cache/threshold warning")
}

func TestLint_PricingCoverage(t *testing.T) {
	rawMissingPricing := `
providers:
  - name: mock-a
    type: openai
    base_url: http://localhost:8081/v1
budget:
  require_pricing: true
routes:
  - name: default
    targets: [{provider: mock-a, model: unpriced-model}]
`
	cfg, err := config.Parse([]byte(rawMissingPricing))
	require.NoError(t, err)
	issues := config.Lint(cfg)
	foundPricingError := false
	for _, issue := range issues {
		if issue.Rule == "budget/pricing_coverage" && issue.Severity == config.SeverityError {
			foundPricingError = true
		}
	}
	require.True(t, foundPricingError, "missing pricing under require_pricing must produce a budget/pricing_coverage error")
}

func TestLint_AdminExposure(t *testing.T) {
	rawExposedAdmin := `
server:
  addr: ":8080"
  admin_addr: "0.0.0.0:9090"
admin_auth:
  enabled: false
providers:
  - name: mock-a
    type: openai
    base_url: http://localhost:8081/v1
pricing:
  mock-a/m1: {input: 1, output: 2}
routes:
  - name: default
    targets: [{provider: mock-a, model: m1}]
`
	cfg, err := config.Parse([]byte(rawExposedAdmin))
	require.NoError(t, err)
	issues := config.Lint(cfg)
	foundAdminError := false
	for _, issue := range issues {
		if issue.Rule == "admin/exposure" && issue.Severity == config.SeverityError {
			foundAdminError = true
		}
	}
	require.True(t, foundAdminError, "non-loopback admin without auth enabled must produce an admin/exposure error")
}

func TestLint_ShippedConfigs(t *testing.T) {
	// Test deploy/proofgate.yaml
	deployYAMLPath := filepath.Join("..", "..", "deploy", "proofgate.yaml")
	b, err := os.ReadFile(deployYAMLPath)
	require.NoError(t, err)

	cfg, err := config.Parse(b)
	require.NoError(t, err, "deploy/proofgate.yaml must be valid YAML")

	issues := config.Lint(cfg, config.LintOpts{Strict: true})
	require.Empty(t, issues, "deploy/proofgate.yaml must pass strict linting with zero issues")

	// Test deploy/helm/proofgate/values.yaml embedded config
	helmValuesPath := filepath.Join("..", "..", "deploy", "helm", "proofgate", "values.yaml")
	hb, err := os.ReadFile(helmValuesPath)
	require.NoError(t, err)

	var valuesMap struct {
		Config string `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal(hb, &valuesMap))
	require.NotEmpty(t, valuesMap.Config)

	helmCfg, err := config.Parse([]byte(valuesMap.Config))
	require.NoError(t, err, "Helm embedded config must be valid YAML")

	helmIssues := config.Lint(helmCfg, config.LintOpts{Strict: true})
	require.Empty(t, helmIssues, "Helm values.yaml config block must pass strict linting")
}

func TestValidationCompleteness_R13(t *testing.T) {
	tests := []struct {
		name        string
		cfgYAML     string
		expectError string
	}{
		{
			name: "external provider plain http rejected",
			cfgYAML: `
providers:
  - name: ext
    type: openai
    base_url: http://api.external.com/v1
routes:
  - name: default
    targets: [{provider: ext, model: m}]
`,
			expectError: "must use https",
		},
		{
			name: "external provider plain http allowed with flag",
			cfgYAML: `
providers:
  - name: ext
    type: openai
    base_url: http://api.external.com/v1
    allow_insecure_base_url: true
routes:
  - name: default
    targets: [{provider: ext, model: m}]
`,
			expectError: "",
		},
		{
			name: "invalid injection action enum",
			cfgYAML: `
providers: [{name: a, type: openai, base_url: "http://localhost:8081/v1"}]
routes:
  - name: default
    targets: [{provider: a, model: m}]
    guard:
      injection:
        enabled: true
        action: destroy
`,
			expectError: "unknown injection guard action",
		},
		{
			name: "invalid pii mode enum",
			cfgYAML: `
providers: [{name: a, type: openai, base_url: "http://localhost:8081/v1"}]
routes:
  - name: default
    targets: [{provider: a, model: m}]
    guard:
      pii:
        enabled: true
        mode: delete
`,
			expectError: "unknown pii guard mode",
		},
		{
			name: "invalid smart_route mode enum",
			cfgYAML: `
providers: [{name: a, type: openai, base_url: "http://localhost:8081/v1"}]
routes:
  - name: default
    targets: [{provider: a, model: m}]
    smart_route:
      mode: always_fast
`,
			expectError: "unknown smart_route mode",
		},
		{
			name: "health alpha out of range",
			cfgYAML: `
health:
  alpha: 1.5
providers: [{name: a, type: openai, base_url: "http://localhost:8081/v1"}]
routes:
  - name: default
    targets: [{provider: a, model: m}]
`,
			expectError: "health.alpha must be in [0, 1]",
		},
		{
			name: "hedge max_extra out of range",
			cfgYAML: `
providers: [{name: a, type: openai, base_url: "http://localhost:8081/v1"}]
routes:
  - name: default
    targets: [{provider: a, model: m}]
    hedge:
      enabled: true
      max_extra: 2.0
`,
			expectError: "hedge.max_extra must be in [0, 1]",
		},
		{
			name: "pricing key invalid provider",
			cfgYAML: `
providers: [{name: a, type: openai, base_url: "http://localhost:8081/v1"}]
pricing:
  unknown_prov/gpt-4o: {input: 1, output: 2}
routes:
  - name: default
    targets: [{provider: a, model: m}]
`,
			expectError: "unknown provider \"unknown_prov\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tt.cfgYAML))
			if tt.expectError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

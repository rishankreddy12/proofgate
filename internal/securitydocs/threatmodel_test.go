// Package securitydocs checks that the threat model points at tests that exist.
package securitydocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type threat struct {
	ID       string   `yaml:"id"`
	Category string   `yaml:"category"`
	Asset    string   `yaml:"asset"`
	Threat   string   `yaml:"threat"`
	Controls []string `yaml:"controls"`
	Tests    []string `yaml:"tests"`
	Residual string   `yaml:"residual"`
}

func TestThreatModelIsBackedByTests(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "threat-model.yaml"))
	require.NoError(t, err)
	var threats []threat
	require.NoError(t, yaml.Unmarshal(b, &threats))
	require.NotEmpty(t, threats)
	seen := map[string]bool{}
	for _, th := range threats {
		require.False(t, seen[th.ID], "duplicate id %s", th.ID)
		seen[th.ID] = true
		require.Contains(t, "STRIDE", th.Category, th.ID)
		require.NotEmpty(t, th.Controls, "%s has no controls", th.ID)
		require.NotEmpty(t, th.Tests, "%s has no tests", th.ID)
		for _, ref := range th.Tests {
			file, fn, _ := strings.Cut(ref, ":")
			src, err := os.ReadFile(filepath.Join(root, file))
			require.NoError(t, err, "%s: missing %s", th.ID, file)
			if fn != "" {
				require.Contains(t, string(src), "func "+fn+"(", "%s: %s has no %s", th.ID, file, fn)
			}
		}
	}
}

package router

import (
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

func cfg(t *testing.T) *config.Config {
	c, err := config.Parse([]byte(`
providers:
  - {name: a, type: openai, base_url: "http://a"}
  - {name: b, type: openai, base_url: "http://b"}
pricing:
  a/big:   {input: 3, output: 15}
  b/small: {input: 0.1, output: 0.4}
routes:
  - name: default
    targets: [{provider: a, model: big}, {provider: b, model: small}]
  - name: cheap
    strategy: cheapest
    targets: [{provider: a, model: big}, {provider: b, model: small}]
`))
	require.NoError(t, err)
	return c
}

func TestResolve(t *testing.T) {
	r := New(cfg(t), NewBreakers(5, time.Second, time.Now))
	rt, err := r.Resolve("default", false)
	require.NoError(t, err)
	require.Equal(t, "default", rt.Name)

	_, err = r.Resolve("a/big", false)
	require.ErrorContains(t, err, "direct targets are not allowed")
	rt, err = r.Resolve("a/big", true)
	require.NoError(t, err)
	require.Equal(t, []Target{{"a", "big"}}, rt.Targets)

	_, err = r.Resolve("zzz/m", true)
	require.ErrorContains(t, err, "unknown provider")
	_, err = r.Resolve("nope", false)
	require.ErrorContains(t, err, "unknown route")
}

func TestPlanStrategiesAndBreakers(t *testing.T) {
	br := NewBreakers(1, time.Minute, time.Now)
	r := New(cfg(t), br)
	def, _ := r.Resolve("default", false)
	cheap, _ := r.Resolve("cheap", false)
	require.Equal(t, []Target{{"a", "big"}, {"b", "small"}}, r.Plan(def))
	require.Equal(t, []Target{{"b", "small"}, {"a", "big"}}, r.Plan(cheap))

	br.Failure(Target{"a", "big"}) // opens (threshold 1)
	require.Equal(t, []Target{{"b", "small"}, {"a", "big"}}, r.Plan(def), "open breaker moves target to the end")
}

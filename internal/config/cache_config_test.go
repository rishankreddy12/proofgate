package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const base = `
providers: [{name: a, type: openai, base_url: "http://a"}]
routes:
  - name: embed
    embeddings: true
    targets: [{provider: a, model: e}]
`

func TestCacheDefaults(t *testing.T) {
	c, err := Parse([]byte(base + `
  - name: chat
    targets: [{provider: a, model: m}]
`))
	require.NoError(t, err)
	cc := c.Routes[1].Cache
	require.Equal(t, "off", cc.Mode)
	require.True(t, cc.Exact)
	require.False(t, cc.Semantic)
	require.Equal(t, 0.95, cc.Threshold)
	require.Equal(t, 24*time.Hour, cc.TTL)
	require.Equal(t, 1, cc.Version)
	require.Equal(t, 65536, cc.MaxEntryBytes)
}

func TestCacheValidation(t *testing.T) {
	_, err := Parse([]byte(base + `
  - name: chat
    cache: {mode: on, semantic: true}
    targets: [{provider: a, model: m}]
`))
	require.ErrorContains(t, err, "embedding_route")

	_, err = Parse([]byte(base + `
  - name: chat
    cache: {mode: on, semantic: true, embedding_route: chat}
    targets: [{provider: a, model: m}]
`))
	require.ErrorContains(t, err, "embeddings: true")

	_, err = Parse([]byte(base + `
  - name: chat
    cache: {mode: sometimes}
    targets: [{provider: a, model: m}]
`))
	require.ErrorContains(t, err, "cache mode")

	c, err := Parse([]byte(base + `
  - name: chat
    cache: {mode: on, semantic: true, embedding_route: embed, threshold: 0.9, exact: false}
    targets: [{provider: a, model: m}]
`))
	require.NoError(t, err)
	require.False(t, c.Routes[1].Cache.Exact, "an explicit false must survive defaulting")
}

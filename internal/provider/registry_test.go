package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	r, err := NewRegistry([]Spec{
		{Name: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1"},
		{Name: "anthropic", Type: "anthropic", BaseURL: "https://api.anthropic.com"},
		{Name: "gemini", Type: "gemini", BaseURL: "https://generativelanguage.googleapis.com"},
	})
	require.NoError(t, err)
	p, ok := r.Get("anthropic")
	require.True(t, ok)
	require.Equal(t, "anthropic", p.Name())
	require.Equal(t, []string{"anthropic", "gemini", "openai"}, r.Names())

	_, err = NewRegistry([]Spec{{Name: "x", Type: "bedrock"}})
	require.ErrorContains(t, err, `unknown provider type "bedrock"`)
	_, err = NewRegistry([]Spec{{Name: "a", Type: "openai"}, {Name: "a", Type: "openai"}})
	require.ErrorContains(t, err, "duplicate")
}

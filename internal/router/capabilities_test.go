package router

import (
	"strings"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCapabilityCompatibility(t *testing.T) {
	cap32k := Capability{MaxContextTokens: 32000, SupportsVision: false}
	cap128k := Capability{MaxContextTokens: 128000, SupportsVision: true}

	// 1. Small request fits in both
	smallReq := &api.ChatRequest{
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: "hello"}}},
	}
	require.True(t, Compatible(smallReq, cap32k))
	require.True(t, Compatible(smallReq, cap128k))

	// 2. 50k token prompt exceeds 32k context, fits in 128k
	// EstimatePromptTokens calculates approx len(text)/4
	largeReq := &api.ChatRequest{
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: strings.Repeat("word ", 40000)}}},
	}
	require.False(t, Compatible(largeReq, cap32k), "should exceed 32k context window")
	require.True(t, Compatible(largeReq, cap128k), "should fit in 128k context window")

	// 3. Vision requirement
	visionReq := &api.ChatRequest{
		Messages: []api.Message{{
			Role: "user",
			Content: api.Content{
				Parts: []api.ContentPart{
					{Type: "image_url", ImageURL: &api.ImageURL{URL: "https://example.com/img.png"}},
				},
			},
		}},
	}
	require.False(t, Compatible(visionReq, cap32k), "text-only model cannot process images")
	require.True(t, Compatible(visionReq, cap128k), "vision-enabled model can process images")

	// 4. Tools requirement
	noTools := false
	capNoTools := Capability{MaxContextTokens: 128000, SupportsVision: true, SupportsTools: &noTools}
	toolReq := &api.ChatRequest{
		Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "get_weather"}}},
	}
	require.False(t, Compatible(toolReq, capNoTools), "target explicitly does not support tools")
	require.True(t, Compatible(toolReq, cap128k), "default model supports tools")
}

func TestPlanFiltersByCapabilities(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai"},
			{Name: "anthropic"},
			{Name: "local"},
		},
		Routes: []config.RouteConfig{
			{
				Name: "chat",
				Targets: []config.TargetConfig{
					{Provider: "openai", Model: "gpt-4o"},         // 128k, vision
					{Provider: "local", Model: "llama-3-8b"},       // 32k, no vision
					{Provider: "anthropic", Model: "claude-3-sonnet"}, // 200k, vision
				},
				Strategy: "fallback",
			},
		},
		Capabilities: map[string]config.CapabilityConfig{
			"openai/gpt-4o": {
				MaxContextTokens: 128000,
				SupportsVision:   true,
			},
			"local/llama-3-8b": {
				MaxContextTokens: 32000,
				SupportsVision:   false,
			},
			"anthropic/claude-3-sonnet": {
				MaxContextTokens: 200000,
				SupportsVision:   true,
			},
		},
	}

	r := New(cfg, nil)
	rt, err := r.Resolve("chat", false)
	require.NoError(t, err)

	// Normal small request: all 3 targets in plan
	smallReq := &api.ChatRequest{
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: "hi"}}},
	}
	p1 := r.Plan(rt, smallReq)
	require.Equal(t, []Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "local", Model: "llama-3-8b"},
		{Provider: "anthropic", Model: "claude-3-sonnet"},
	}, p1)

	// Large request (> 32k tokens): skips local/llama-3-8b
	largeReq := &api.ChatRequest{
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: strings.Repeat("word ", 40000)}}},
	}
	p2 := r.Plan(rt, largeReq)
	require.Equal(t, []Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-3-sonnet"},
	}, p2, "should filter out 32k model for 40k+ token request")

	// Vision request: skips local/llama-3-8b
	visionReq := &api.ChatRequest{
		Messages: []api.Message{{
			Role: "user",
			Content: api.Content{
				Parts: []api.ContentPart{
					{Type: "image_url", ImageURL: &api.ImageURL{URL: "https://example.com/test.jpg"}},
				},
			},
		}},
	}
	p3 := r.Plan(rt, visionReq)
	require.Equal(t, []Target{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-3-sonnet"},
	}, p3, "should filter out text-only model for vision request")
}

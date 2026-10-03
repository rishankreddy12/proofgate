// Package api provides enterprise-grade capabilities, configuration, and structural components for the api subsystem.
package api

// EstimateTokens provides a fast, heuristic approximation of token counts for a given string.
// It assumes an average of 4 characters per token (ceil(len/4)).
// This is used exclusively for rate-limit pre-charges (reserving bucket tokens before the upstream request)
// and for tenant billing ONLY if the upstream provider fails to return actual usage telemetry.
// Authentic provider usage always overrides this estimate when available.
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// EstimatePromptTokens calculates a heuristic token count for the entire ChatRequest payload.
// It concatenates all messages and adds 4 tokens of structural overhead per message to account
// for role boundaries, which aligns loosely with OpenAI's tokenizer overhead rules.
func (r *ChatRequest) EstimatePromptTokens() int {
	return EstimateTokens(r.PromptText()) + 4*len(r.Messages)
}

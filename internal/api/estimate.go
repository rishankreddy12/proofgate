package api

// EstimateTokens approximates tokens as ceil(len/4). It is only used for rate-limit pre-charges and for
// billing when a provider omits usage; real usage always replaces it when available.
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// EstimatePromptTokens adds 4 tokens of per-message overhead to the text estimate.
func (r *ChatRequest) EstimatePromptTokens() int {
	return EstimateTokens(r.PromptText()) + 4*len(r.Messages)
}

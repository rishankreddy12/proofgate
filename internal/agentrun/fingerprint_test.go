package agentrun

import (
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/stretchr/testify/require"
)

func step(callID, args, result string) *api.ChatRequest {
	return &api.ChatRequest{Messages: []api.Message{
		{Role: "user", Content: api.Content{Text: "find the weather"}},
		{Role: "assistant", ToolCalls: []api.ToolCall{{ID: callID, Type: "function", Function: api.FunctionCall{Name: "search", Arguments: args}}}},
		{Role: "tool", ToolCallID: callID, Content: api.Content{Text: result}},
	}}
}

func TestFingerprintIgnoresIdsAndCounters(t *testing.T) {
	a := Fingerprint(step("call_1", `{"q":"weather pune"}`, "No results (took 12 ms)"))
	b := Fingerprint(step("call_2", `{"q":"Weather  Pune"}`, "No results (took 97 ms)"))
	require.Equal(t, a, b, "same step, different ids, case, spacing and timing")
	c := Fingerprint(step("call_3", `{"q":"weather mumbai"}`, "No results (took 12 ms)"))
	require.NotEqual(t, a, c)
	require.Len(t, a, 32)
}

func TestNormalize(t *testing.T) {
	require.Equal(t, "at #:# on #-#-#", Normalize("  At 10:42   on 2026-09-19 "))
}

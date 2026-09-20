package proof

import (
	"context"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/stretchr/testify/require"
)

func makeChatResponse(content string) *api.ChatResponse {
	return &api.ChatResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Choices: []api.Choice{{Message: api.Message{Role: "assistant", Content: api.Content{Text: content}}}},
	}
}

func TestJudge_CacheEvaluation_ParseValid(t *testing.T) {
	ctx := context.Background()
	caller := ChatCallerFunc(func(_ context.Context, route string, req *api.ChatRequest) (*api.ChatResponse, error) {
		require.Equal(t, "judge-model", route)
		require.Equal(t, 0.0, *req.Temperature)
		require.Equal(t, 100, *req.MaxTokens)
		return makeChatResponse(`{"acceptable": true, "reason": "Both answers provide identical instructions."}`), nil
	})

	judge := NewJudge(caller, "judge-model")
	res, err := judge.EvaluateCache(ctx, ShadowRecord{
		Query:           "How to install Go?",
		CandidateQuery:  "Install Go",
		CandidateAnswer: "Download from golang.org",
		ActualAnswer:    "Visit golang.org and download the installer.",
	})
	require.NoError(t, err)
	require.True(t, res.Acceptable)
	require.Equal(t, "Both answers provide identical instructions.", res.Reason)
	require.Equal(t, JudgeCachePromptVersion, res.JudgePromptVersion)
}

func TestJudge_CacheEvaluation_ParseInvalid_Retry(t *testing.T) {
	ctx := context.Background()
	calls := 0
	caller := ChatCallerFunc(func(_ context.Context, _ string, _ *api.ChatRequest) (*api.ChatResponse, error) {
		calls++
		if calls == 1 {
			// First call returns malformed JSON
			return makeChatResponse("I think this is acceptable: true"), nil
		}
		// Retry succeeds with markdown-fenced valid JSON
		return makeChatResponse("```json\n{\"acceptable\": false, \"reason\": \"Outdated info.\"}\n```"), nil
	})

	judge := NewJudge(caller, "judge-model")
	res, err := judge.EvaluateCache(ctx, ShadowRecord{
		Query:           "What is latest Go version?",
		CandidateQuery:  "Latest Go version",
		CandidateAnswer: "1.18",
		ActualAnswer:    "1.27",
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.False(t, res.Acceptable)
	require.Equal(t, "Outdated info.", res.Reason)

	// Test persistent failure
	persistentCaller := ChatCallerFunc(func(_ context.Context, _ string, _ *api.ChatRequest) (*api.ChatResponse, error) {
		return makeChatResponse("not json"), nil
	})
	judge2 := NewJudge(persistentCaller, "judge-model")
	res2, err2 := judge2.EvaluateCache(ctx, ShadowRecord{})
	require.NoError(t, err2)
	require.False(t, res2.Acceptable)
	require.Equal(t, "judge_unparseable", res2.Reason)
}

func TestJudge_PairwiseRouting_PositionBiasSwap(t *testing.T) {
	ctx := context.Background()

	// Scenario 1: rand >= 0.5 (NOT swapped)
	// Answer A = cheap, Answer B = strong
	caller1 := ChatCallerFunc(func(_ context.Context, _ string, req *api.ChatRequest) (*api.ChatResponse, error) {
		prompt := req.Messages[0].Content.PlainText()
		require.Contains(t, prompt, "Answer A: cheap text")
		require.Contains(t, prompt, "Answer B: strong text")
		return makeChatResponse(`{"score_a": 0.7, "score_b": 0.95, "reason": "B is more detailed."}`), nil
	})

	judge1 := NewJudge(caller1, "judge-model")
	judge1.SetRand(func() float64 { return 0.8 }) // not swapped

	res1, err := judge1.EvaluateRouting(ctx, "Explain photosynthesis", "cheap text", "strong text")
	require.NoError(t, err)
	require.InDelta(t, 0.7, res1.ScoreCheap, 0.001)
	require.InDelta(t, 0.95, res1.ScoreStrong, 0.001)
	require.Equal(t, "B is more detailed.", res1.Reason)

	// Scenario 2: rand < 0.5 (SWAPPED)
	// Answer A = strong, Answer B = cheap
	caller2 := ChatCallerFunc(func(_ context.Context, _ string, req *api.ChatRequest) (*api.ChatResponse, error) {
		prompt := req.Messages[0].Content.PlainText()
		require.Contains(t, prompt, "Answer A: strong text")
		require.Contains(t, prompt, "Answer B: cheap text")
		// The LLM judges Answer A (strong) as 0.95 and Answer B (cheap) as 0.7
		return makeChatResponse(`{"score_a": 0.95, "score_b": 0.7, "reason": "A is better."}`), nil
	})

	judge2 := NewJudge(caller2, "judge-model")
	judge2.SetRand(func() float64 { return 0.2 }) // swapped

	res2, err := judge2.EvaluateRouting(ctx, "Explain photosynthesis", "cheap text", "strong text")
	require.NoError(t, err)
	// The swap must be accurately undone: ScoreCheap is 0.7, ScoreStrong is 0.95
	require.InDelta(t, 0.7, res2.ScoreCheap, 0.001)
	require.InDelta(t, 0.95, res2.ScoreStrong, 0.001)
}

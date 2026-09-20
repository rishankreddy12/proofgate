package proof

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
)

const (
	JudgeCachePromptVersion   = "judge_cache_v1"
	JudgeRoutingPromptVersion = "judge_routing_v1"
)

type ChatCaller interface {
	ChatInternal(ctx context.Context, route string, req *api.ChatRequest) (*api.ChatResponse, error)
}

type ChatCallerFunc func(ctx context.Context, route string, req *api.ChatRequest) (*api.ChatResponse, error)

func (f ChatCallerFunc) ChatInternal(ctx context.Context, route string, req *api.ChatRequest) (*api.ChatResponse, error) {
	return f(ctx, route, req)
}

type CacheEvalResult struct {
	Acceptable         bool
	Reason             string
	JudgePromptVersion string
}

type RoutingEvalResult struct {
	ScoreCheap         float64
	ScoreStrong        float64
	Reason             string
	JudgePromptVersion string
}

type Judge struct {
	caller     ChatCaller
	modelRoute string
	randFn     func() float64
}

func NewJudge(caller ChatCaller, modelRoute string) *Judge {
	return &Judge{
		caller:     caller,
		modelRoute: modelRoute,
		randFn:     rand.Float64,
	}
}

func (j *Judge) SetRand(fn func() float64) {
	if fn != nil {
		j.randFn = fn
	}
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "```") {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
			lines = lines[:len(lines)-1]
		}
		s = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return s
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int             { return &i }

type cacheEvalJSON struct {
	Acceptable bool   `json:"acceptable"`
	Reason     string `json:"reason"`
}

func (j *Judge) EvaluateCache(ctx context.Context, r ShadowRecord) (CacheEvalResult, error) {
	prompt := fmt.Sprintf(`Given:
User query: %s
Candidate cached answer (from query %s):
%s
Fresh model answer:
%s
Does the candidate answer adequately satisfy the user query compared to the fresh answer?
Answer strictly with JSON: {"acceptable": true|false, "reason": "one sentence"}.`,
		r.Query, r.CandidateQuery, r.CandidateAnswer, r.ActualAnswer)

	req := &api.ChatRequest{
		Model:       j.modelRoute,
		Messages:    []api.Message{{Role: "user", Content: api.Content{Text: prompt}}},
		Temperature: floatPtr(0.0),
		MaxTokens:   intPtr(100),
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := j.caller.ChatInternal(ctx, j.modelRoute, req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp == nil || len(resp.Choices) == 0 {
			lastErr = fmt.Errorf("empty response")
			continue
		}
		raw := resp.Choices[0].Message.Content.PlainText()
		cleaned := extractJSON(raw)
		var out cacheEvalJSON
		if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
			lastErr = err
			continue
		}
		return CacheEvalResult{
			Acceptable:         out.Acceptable,
			Reason:             out.Reason,
			JudgePromptVersion: JudgeCachePromptVersion,
		}, nil
	}

	_ = lastErr
	return CacheEvalResult{
		Acceptable:         false,
		Reason:             "judge_unparseable",
		JudgePromptVersion: JudgeCachePromptVersion,
	}, nil
}

type routingEvalJSON struct {
	ScoreA float64 `json:"score_a"`
	ScoreB float64 `json:"score_b"`
	Reason string  `json:"reason"`
}

func (j *Judge) EvaluateRouting(ctx context.Context, prompt, cheapAnswer, strongAnswer string) (RoutingEvalResult, error) {
	swapped := j.randFn() < 0.5
	answerA := cheapAnswer
	answerB := strongAnswer
	if swapped {
		answerA = strongAnswer
		answerB = cheapAnswer
	}

	evalPrompt := fmt.Sprintf(`Given user prompt: %s
Answer A: %s
Answer B: %s
Evaluate each answer on: correctness, completeness, instruction following.
Score each answer from 0.0 to 1.0.
Answer strictly with JSON: {"score_a": float, "score_b": float, "reason": "one sentence"}.`,
		prompt, answerA, answerB)

	req := &api.ChatRequest{
		Model:       j.modelRoute,
		Messages:    []api.Message{{Role: "user", Content: api.Content{Text: evalPrompt}}},
		Temperature: floatPtr(0.0),
		MaxTokens:   intPtr(100),
	}

	for attempt := 0; attempt < 2; attempt++ {
		resp, err := j.caller.ChatInternal(ctx, j.modelRoute, req)
		if err != nil {
			continue
		}
		if resp == nil || len(resp.Choices) == 0 {
			continue
		}
		raw := resp.Choices[0].Message.Content.PlainText()
		cleaned := extractJSON(raw)
		var out routingEvalJSON
		if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
			continue
		}

		scoreCheap := out.ScoreA
		scoreStrong := out.ScoreB
		if swapped {
			scoreCheap = out.ScoreB
			scoreStrong = out.ScoreA
		}

		return RoutingEvalResult{
			ScoreCheap:         scoreCheap,
			ScoreStrong:        scoreStrong,
			Reason:             out.Reason,
			JudgePromptVersion: JudgeRoutingPromptVersion,
		}, nil
	}

	return RoutingEvalResult{
		ScoreCheap:         0.5,
		ScoreStrong:        0.5,
		Reason:             "judge_unparseable",
		JudgePromptVersion: JudgeRoutingPromptVersion,
	}, nil
}

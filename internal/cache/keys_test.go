package cache

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

var on = config.CacheConfig{Mode: "on", Exact: true, Semantic: true, Version: 1}

func req(msgs ...api.Message) *api.ChatRequest { return &api.ChatRequest{Model: "r", Messages: msgs} }
func sys(s string) api.Message                 { return api.Message{Role: "system", Content: api.Content{Text: s}} }
func user(s string) api.Message                { return api.Message{Role: "user", Content: api.Content{Text: s}} }
func asst(s string) api.Message                { return api.Message{Role: "assistant", Content: api.Content{Text: s}} }

func TestEligibility(t *testing.T) {
	require.Equal(t, Plan{Exact: true, Semantic: true}, Eligibility(req(sys("s"), user("q")), http.Header{}, on))
	require.Equal(t, Plan{Exact: true}, Eligibility(req(user("q"), asst("a"), user("q2")), http.Header{}, on), "multi-turn: exact only")

	img := user("")
	img.Content = api.Content{Parts: []api.ContentPart{{Type: "image_url", ImageURL: &api.ImageURL{URL: "data:x"}}}}
	require.Equal(t, Plan{Exact: true}, Eligibility(req(img), http.Header{}, on))

	withFormat := req(user("q"))
	withFormat.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
	require.Equal(t, Plan{Exact: true}, Eligibility(withFormat, http.Header{}, on))

	withTools := req(user("q"))
	withTools.Tools = []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "f"}}}
	require.Equal(t, Plan{Bypass: "tools"}, Eligibility(withTools, http.Header{}, on))

	h := http.Header{}
	h.Set("X-ProofGate-Cache", "off")
	require.Equal(t, Plan{Bypass: "header"}, Eligibility(req(user("q")), h, on))

	// Internal call bypasses cache
	require.Equal(t, Plan{Bypass: "header"}, Eligibility(req(user("q")), http.Header{}, on, true))

	// Spoofed internal header without internal=true does NOT bypass cache
	spoofedH := http.Header{}
	spoofedH.Set("X-ProofGate-Internal", "true")
	require.Equal(t, Plan{Exact: true, Semantic: true}, Eligibility(req(user("q")), spoofedH, on, false))

	require.Equal(t, Plan{}, Eligibility(req(user("q")), http.Header{}, config.CacheConfig{Mode: "off", Exact: true}))
}

func TestScopeSeparatesWhatMatters(t *testing.T) {
	a := req(sys("be brief"), user("q"))
	base := Scope("t1", "default", a, on)
	require.Len(t, base, 64)
	require.Equal(t, base, Scope("t1", "default", req(sys("be brief"), user("different question")), on), "user text is not in the scope")
	require.NotEqual(t, base, Scope("t2", "default", a, on), "tenant")
	require.NotEqual(t, base, Scope("t1", "other", a, on), "route")
	require.NotEqual(t, base, Scope("t1", "default", req(sys("be verbose"), user("q")), on), "system prompt")
	v2 := on
	v2.Version = 2
	require.NotEqual(t, base, Scope("t1", "default", a, v2), "version")
	hot := req(sys("be brief"), user("q"))
	temp := 1.2
	hot.Temperature = &temp
	require.NotEqual(t, base, Scope("t1", "default", hot, on), "temperature")

	u1, u2 := req(user("q")), req(user("q"))
	u1.User, u2.User = "alice", "bob"
	require.Equal(t, Scope("t", "r", u1, on), Scope("t", "r", u2, on), "user ignored unless per_user")
	pu := on
	pu.PerUser = true
	require.NotEqual(t, Scope("t", "r", u1, pu), Scope("t", "r", u2, pu))
}

func TestChatRequestFieldsClassified(t *testing.T) {
	reqType := reflect.TypeOf(api.ChatRequest{})
	require.Equal(t, 21, reqType.NumField(), "expected ChatRequest to have 21 fields; if new fields are added, classify them")
	for i := 0; i < reqType.NumField(); i++ {
		field := reqType.Field(i).Name
		_, inc := ScopeIncludedFields[field]
		_, exc := ScopeExcludedFields[field]
		require.True(t, inc != exc, "field %q must be in either ScopeIncludedFields or ScopeExcludedFields (not both)", field)
	}
}

func TestScopeIncludesOutputAffectingFields(t *testing.T) {
	base := req(sys("be brief"), user("q"))
	baseScope := Scope("t1", "default", base, on)

	// ReasoningEffort
	withEffort := req(sys("be brief"), user("q"))
	withEffort.ReasoningEffort = "high"
	require.NotEqual(t, baseScope, Scope("t1", "default", withEffort, on))

	// Model
	diffModel := req(sys("be brief"), user("q"))
	diffModel.Model = "other-model"
	require.NotEqual(t, baseScope, Scope("t1", "default", diffModel, on))

	// PresencePenalty
	withPres := req(sys("be brief"), user("q"))
	pres := 0.5
	withPres.PresencePenalty = &pres
	require.NotEqual(t, baseScope, Scope("t1", "default", withPres, on))

	// FrequencyPenalty
	withFreq := req(sys("be brief"), user("q"))
	freq := 0.5
	withFreq.FrequencyPenalty = &freq
	require.NotEqual(t, baseScope, Scope("t1", "default", withFreq, on))

	// ParallelToolCalls
	withParallel := req(sys("be brief"), user("q"))
	parallel := false
	withParallel.ParallelToolCalls = &parallel
	require.NotEqual(t, baseScope, Scope("t1", "default", withParallel, on))

	// N
	withN := req(sys("be brief"), user("q"))
	n := 2
	withN.N = &n
	require.NotEqual(t, baseScope, Scope("t1", "default", withN, on))

	// Logprobs
	withLogprobs := req(sys("be brief"), user("q"))
	logp := true
	withLogprobs.Logprobs = &logp
	require.NotEqual(t, baseScope, Scope("t1", "default", withLogprobs, on))

	// TopLogprobs
	withTopLogprobs := req(sys("be brief"), user("q"))
	topL := 5
	withTopLogprobs.TopLogprobs = &topL
	require.NotEqual(t, baseScope, Scope("t1", "default", withTopLogprobs, on))

	// Stability across calls
	require.Equal(t, baseScope, Scope("t1", "default", base, on))
}

func TestExactHashAndSemanticText(t *testing.T) {
	s := Scope("t", "r", req(user("q")), on)
	require.Equal(t, ExactHash(s, req(user("q"))), ExactHash(s, req(user("q"))))
	require.NotEqual(t, ExactHash(s, req(user("q"))), ExactHash(s, req(user("Q"))))
	require.Equal(t, "second", SemanticText(req(sys("s"), user("first"), asst("a"), user("second"))))
}

func TestSemanticTextFromHeader(t *testing.T) {
	r := req(sys("context instructions"), user("massive 50-page document context and data..."))

	// Without header: fallback to user message
	require.Equal(t, "massive 50-page document context and data...", SemanticTextFromHeader(r, nil))
	require.Equal(t, "massive 50-page document context and data...", SemanticTextFromHeader(r, http.Header{}))

	h := http.Header{}
	h.Set("X-ProofGate-Cache-Query", "What is the refund policy?")

	// When allowClientKey is false: ignores header and falls back
	require.Equal(t, "massive 50-page document context and data...", SemanticTextFromHeader(r, h, false))
	require.Equal(t, "massive 50-page document context and data...", SemanticTextFromHeader(r, h))

	// When allowClientKey is true: uses X-ProofGate-Cache-Query
	require.Equal(t, "What is the refund policy?", SemanticTextFromHeader(r, h, true))

	// With whitespace header when allowed: falls back
	h2 := http.Header{}
	h2.Set("X-ProofGate-Cache-Query", "   ")
	require.Equal(t, "massive 50-page document context and data...", SemanticTextFromHeader(r, h2, true))
}

func TestContextHash(t *testing.T) {
	// Two requests with different context docs but same question text
	r1 := req(sys("Doc A: 14 days refund policy"), user("What is the refund policy?"))
	r2 := req(sys("Doc B: 30 days refund policy"), user("What is the refund policy?"))
	r3 := req(sys("Doc A: 14 days refund policy"), user("What is the refund policy?"))

	require.NotEmpty(t, ContextHash(r1))
	require.NotEqual(t, ContextHash(r1), ContextHash(r2), "different context documents must yield different ContextHash")
	require.Equal(t, ContextHash(r1), ContextHash(r3), "identical context documents must yield identical ContextHash")
}

func TestParseTags(t *testing.T) {
	require.Equal(t, []string{"docs-v2", "faq"}, ParseTags(" docs-v2, faq ,BAD TAG,, "))
	require.Len(t, ParseTags("a,b,c,d,e,f,g,h,i,j"), 8)
}

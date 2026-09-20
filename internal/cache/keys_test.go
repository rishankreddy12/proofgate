package cache

import (
	"encoding/json"
	"net/http"
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

func TestExactHashAndSemanticText(t *testing.T) {
	s := Scope("t", "r", req(user("q")), on)
	require.Equal(t, ExactHash(s, req(user("q"))), ExactHash(s, req(user("q"))))
	require.NotEqual(t, ExactHash(s, req(user("q"))), ExactHash(s, req(user("Q"))))
	require.Equal(t, "second", SemanticText(req(sys("s"), user("first"), asst("a"), user("second"))))
}

func TestParseTags(t *testing.T) {
	require.Equal(t, []string{"docs-v2", "faq"}, ParseTags(" docs-v2, faq ,BAD TAG,, "))
	require.Len(t, ParseTags("a,b,c,d,e,f,g,h,i,j"), 8)
}

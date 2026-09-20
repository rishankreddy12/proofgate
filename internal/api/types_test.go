package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentStringAndParts(t *testing.T) {
	var m Message
	require.NoError(t, json.Unmarshal([]byte(`{"role":"user","content":"hi"}`), &m))
	require.Equal(t, "hi", m.Content.Text)
	require.Nil(t, m.Content.Parts)

	require.NoError(t, json.Unmarshal([]byte(`{"role":"user","content":[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"data:x"}},{"type":"text","text":"b"}]}`), &m))
	require.Len(t, m.Content.Parts, 3)
	require.Equal(t, "a\nb", m.Content.PlainText())

	out, err := json.Marshal(Message{Role: "user", Content: Content{Text: "x"}})
	require.NoError(t, err)
	require.JSONEq(t, `{"role":"user","content":"x"}`, string(out))

	require.NoError(t, json.Unmarshal([]byte(`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}`), &m))
	require.Equal(t, "", m.Content.Text)
	require.Equal(t, "f", m.ToolCalls[0].Function.Name)
}

func TestStringOrSlice(t *testing.T) {
	var r ChatRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"m","messages":[],"stop":"END"}`), &r))
	require.Equal(t, StringOrSlice{"END"}, r.Stop)
	require.NoError(t, json.Unmarshal([]byte(`{"model":"m","messages":[],"stop":["a","b"]}`), &r))
	require.Equal(t, StringOrSlice{"a", "b"}, r.Stop)
}

func TestPromptTextAndEstimates(t *testing.T) {
	r := &ChatRequest{Messages: []Message{
		{Role: "system", Content: Content{Text: "be brief"}},
		{Role: "user", Content: Content{Text: "hello world"}},
	}}
	require.Equal(t, "system: be brief\nuser: hello world", r.PromptText())
	require.Equal(t, 3, EstimateTokens("hello world")) // ceil(11/4)
	require.Equal(t, EstimateTokens(r.PromptText())+4*2, r.EstimatePromptTokens())

	require.Equal(t, 1024, r.EffectiveMaxTokens(1024))
	n := 50
	r.MaxCompletionTokens = &n
	require.Equal(t, 50, r.EffectiveMaxTokens(1024))
}

func TestCloneIsDeep(t *testing.T) {
	r := &ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: Content{Text: "a"}}}}
	c := r.Clone()
	c.Messages[0].Content.Text = "changed"
	require.Equal(t, "a", r.Messages[0].Content.Text)
}

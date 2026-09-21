package mcpproxy

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAndToolCall(t *testing.T) {
	msgs, batch, err := Parse([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_file","arguments":{"path":"a.go"}}}`))
	require.NoError(t, err)
	require.False(t, batch)
	name, args, ok := ToolCall(msgs[0])
	require.True(t, ok)
	require.Equal(t, "get_file", name)
	require.JSONEq(t, `{"path":"a.go"}`, string(args))

	msgs, batch, err = Parse([]byte(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	require.NoError(t, err)
	require.True(t, batch)
	require.Len(t, msgs, 2)
	_, _, ok = ToolCall(msgs[0])
	require.False(t, ok)
}

func TestFilterToolsList(t *testing.T) {
	in := json.RawMessage(`{"tools":[{"name":"get_file","inputSchema":{}},{"name":"delete_repo"}],"nextCursor":"c2"}`)
	out, err := FilterToolsList(in, func(n string) bool { return n == "get_file" })
	require.NoError(t, err)
	require.JSONEq(t, `{"tools":[{"name":"get_file","inputSchema":{}}],"nextCursor":"c2"}`, string(out))
}

func TestErrorMessage(t *testing.T) {
	b, _ := json.Marshal(ErrorMessage(json.RawMessage(`7`), -32001, "tool not allowed"))
	require.JSONEq(t, `{"jsonrpc":"2.0","id":7,"error":{"code":-32001,"message":"tool not allowed"}}`, string(b))
}

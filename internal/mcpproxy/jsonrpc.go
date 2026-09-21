package mcpproxy

import (
	"bytes"
	"encoding/json"
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

const (
	CodeToolDenied = -32001
	CodeRunLimit   = -32002
)

func Parse(body []byte) ([]Message, bool, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var ms []Message
		return ms, true, json.Unmarshal(body, &ms)
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false, err
	}
	return []Message{m}, false, nil
}

func ToolCall(m Message) (string, json.RawMessage, bool) {
	if m.Method != "tools/call" {
		return "", nil, false
	}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.Name == "" {
		return "", nil, false
	}
	return p.Name, p.Arguments, true
}

func FilterToolsList(result json.RawMessage, allow func(string) bool) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil, err
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(obj["tools"], &tools); err != nil {
		return result, nil // not a tools list; leave untouched
	}
	kept := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		var n struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(t, &n) == nil && allow(n.Name) {
			kept = append(kept, t)
		}
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	obj["tools"] = b
	return json.Marshal(obj)
}

func ErrorMessage(id json.RawMessage, code int, msg string) Message {
	return Message{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

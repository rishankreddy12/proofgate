// Package mcpproxy provides enterprise-grade capabilities, configuration, and structural components for the mcpproxy subsystem.
package mcpproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// rejectDuplicateKeys validates that no JSON object in data contains duplicate or case-variant keys.
func rejectDuplicateKeys(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("empty JSON body")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return err
	}

	if delim, ok := tok.(json.Delim); ok {
		switch delim {
		case '{':
			if err := checkObject(dec); err != nil {
				return err
			}
		case '[':
			if err := checkArray(dec); err != nil {
				return err
			}
		default:
			return fmt.Errorf("expected JSON object or array, got %v", delim)
		}
	} else {
		return fmt.Errorf("expected JSON object or array, got %v", tok)
	}

	if dec.More() {
		return fmt.Errorf("unexpected trailing data in JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing data after JSON")
	}
	return nil
}

func checkObject(dec *json.Decoder) error {
	seen := make(map[string]struct{})
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected string object key, got %T", tok)
		}
		lowerKey := strings.ToLower(key)
		if _, exists := seen[lowerKey]; exists {
			return fmt.Errorf("duplicate or case-variant key in JSON object: %q", key)
		}
		seen[lowerKey] = struct{}{}

		valTok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := valTok.(json.Delim); ok {
			switch delim {
			case '{':
				if err := checkObject(dec); err != nil {
					return err
				}
			case '[':
				if err := checkArray(dec); err != nil {
					return err
				}
			}
		}
	}
	endTok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := endTok.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("expected '}', got %v", endTok)
	}
	return nil
}

func checkArray(dec *json.Decoder) error {
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				if err := checkObject(dec); err != nil {
					return err
				}
			case '[':
				if err := checkArray(dec); err != nil {
					return err
				}
			}
		}
	}
	endTok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := endTok.(json.Delim); !ok || delim != ']' {
		return fmt.Errorf("expected ']', got %v", endTok)
	}
	return nil
}

// StrictParse executes the primary logic for the Parse operation with strict duplicate-key validation.
// It rejects duplicate or case-variant keys anywhere in the JSON body.
func StrictParse(body []byte) ([]Message, bool, error) {
	if err := rejectDuplicateKeys(body); err != nil {
		return nil, false, err
	}
	return Parse(body)
}

// RPCError defines the core enterprise configuration and state for RPCError.
// It is responsible for managing the lifecycle, validation, and schema of the RPCError entity.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Message defines the core enterprise configuration and state for Message.
// It is responsible for managing the lifecycle, validation, and schema of the Message entity.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

const (
	// CodeToolDenied defines a specific variation or structural setting for CodeToolDenied.
	CodeToolDenied = -32001
	// CodeRunLimit defines a specific variation or structural setting for CodeRunLimit.
	CodeRunLimit = -32002
	// CodeMethodDenied defines a specific variation or structural setting for CodeMethodDenied.
	CodeMethodDenied = -32003
)

// Parse executes the primary logic for the Parse operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// ToolCall executes the primary logic for the ToolCall operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// FilterToolsList executes the primary logic for the FilterToolsList operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func FilterToolsList(result json.RawMessage, allow func(string) bool) (json.RawMessage, bool, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil, false, err
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(obj["tools"], &tools); err != nil {
		return result, false, nil // not a tools list; leave untouched
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
	filtered := len(kept) < len(tools)
	b, err := json.Marshal(kept)
	if err != nil {
		return nil, false, err
	}
	obj["tools"] = b
	res, err := json.Marshal(obj)
	return res, filtered, err
}

// ErrorMessage executes the primary logic for the ErrorMessage operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func ErrorMessage(id json.RawMessage, code int, msg string) Message {
	return Message{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

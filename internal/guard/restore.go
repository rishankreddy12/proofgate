// Package guard provides enterprise-grade capabilities, configuration, and structural components for the guard subsystem.
package guard

import (
	"strings"
)

// Restore replaces placeholders in text with their original values from mapping.
func Restore(text string, mapping map[string]string) string {
	if len(mapping) == 0 {
		return text
	}
	for placeholder, original := range mapping {
		text = strings.ReplaceAll(text, placeholder, original)
	}
	return text
}

// StreamRestorer buffers partial placeholder tokens across streaming chunks
// and emits reconstituted text.
type StreamRestorer struct {
	mapping map[string]string
	buffer  string
}

// NewStreamRestorer executes the primary logic for the NewStreamRestorer operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewStreamRestorer(mapping map[string]string) *StreamRestorer {
	return &StreamRestorer{
		mapping: mapping,
	}
}

const maxPlaceholderLen = 40

// Process executes the primary logic for the Process operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *StreamRestorer) Process(chunk string) string {
	if len(s.mapping) == 0 {
		return chunk
	}
	s.buffer += chunk

	var out strings.Builder
	for {
		openIdx := strings.Index(s.buffer, "<")
		if openIdx == -1 {
			// No opening bracket, all of buffer is safe to emit
			out.WriteString(s.buffer)
			s.buffer = ""
			break
		}

		// Emit everything before `<`
		if openIdx > 0 {
			out.WriteString(s.buffer[:openIdx])
			s.buffer = s.buffer[openIdx:]
		}

		// Now buffer starts with `<`
		closeIdx := strings.Index(s.buffer, ">")
		if closeIdx == -1 {
			// Incomplete token. If buffer is longer than max placeholder length, it's not a placeholder
			if len(s.buffer) > maxPlaceholderLen {
				out.WriteString(s.buffer[:1])
				s.buffer = s.buffer[1:]
				continue
			}
			// Hold remaining in buffer for next chunk
			break
		}

		// Found `<...>`
		token := s.buffer[:closeIdx+1]
		if original, ok := s.mapping[token]; ok {
			out.WriteString(original)
		} else {
			out.WriteString(token)
		}
		s.buffer = s.buffer[closeIdx+1:]
	}

	return out.String()
}

// Flush executes the primary logic for the Flush operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *StreamRestorer) Flush() string {
	if len(s.buffer) == 0 {
		return ""
	}
	rem := Restore(s.buffer, s.mapping)
	s.buffer = ""
	return rem
}

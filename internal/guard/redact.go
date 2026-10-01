package guard

import (
	"fmt"
	"strings"
)

type RedactResult struct {
	Redacted string
	Mapping  map[string]string // placeholder -> original
}

// Numberer tracks placeholder numbering across calls for continuous indexing.
type Numberer map[PIIType]int

// Redact replaces PII matches with typed placeholders (or static masks if mode == "mask").
// An optional Numberer can be passed to maintain continuous placeholder numbering across multiple calls.
func Redact(text string, matches []PIIMatch, mode string, n ...Numberer) RedactResult {
	if len(matches) == 0 {
		return RedactResult{Redacted: text, Mapping: nil}
	}

	var b strings.Builder
	counts := make(map[PIIType]int)
	if len(n) > 0 && n[0] != nil {
		counts = n[0]
	}
	mapping := make(map[string]string)
	lastIdx := 0

	for _, m := range matches {
		if m.Start > lastIdx {
			b.WriteString(text[lastIdx:m.Start])
		}
		if mode == "mask" {
			b.WriteString(fmt.Sprintf("[REDACTED:%s]", m.Type))
		} else {
			counts[m.Type]++
			placeholder := fmt.Sprintf("<%s_%d>", m.Type, counts[m.Type])
			mapping[placeholder] = m.Value
			b.WriteString(placeholder)
		}
		lastIdx = m.End
	}
	if lastIdx < len(text) {
		b.WriteString(text[lastIdx:])
	}

	if mode == "mask" {
		mapping = nil
	}

	return RedactResult{
		Redacted: b.String(),
		Mapping:  mapping,
	}
}

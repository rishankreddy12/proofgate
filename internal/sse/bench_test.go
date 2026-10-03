package sse

import (
	"strings"
	"testing"
)

func BenchmarkReaderParse(b *testing.B) {
	// Typical SSE stream: 10 events with moderate JSON data payloads.
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("data: {\"id\":\"chatcmpl-abc\",\"choices\":[{\"delta\":{\"content\":\"Hello world\"}}]}\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	input := sb.String()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := NewReader(strings.NewReader(input))
		for {
			_, err := r.Next()
			if err != nil {
				break
			}
		}
	}
}

package guard

import (
	"testing"
)

func BenchmarkDetectPII(b *testing.B) {
	text := "Please send the report to alice@example.com and bob@corp.net. " +
		"My credit card is 4111-1111-1111-1111, SSN 123-45-6789, IP 10.0.0.1. " +
		"Also reach me at +1-555-867-5309. This text has many tokens that should be checked."

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		DetectPII(text)
	}
}

func BenchmarkRedactRestore(b *testing.B) {
	text := "Contact user@example.com with card 4111-1111-1111-1111 and SSN 123-45-6789."
	matches := DetectPII(text)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res := Redact(text, matches, "redact")
		Restore(res.Redacted, res.Mapping)
	}
}

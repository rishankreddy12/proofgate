package config

import (
	"testing"
)

func FuzzParse(f *testing.F) {
	f.Add([]byte(`
database:
  url: postgres://localhost/proofgate
redis:
  url: redis://localhost:6379
routes:
  - name: default
    targets:
      - provider: openai
        model: gpt-4o
`))
	f.Add([]byte(`
routes:
  - name: chat
    targets:
      - provider: anthropic
        model: claude-3-5-sonnet
    cache:
      enabled: true
      similarity_threshold: 0.92
`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`not valid yaml at all`))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Parse must not panic on any input.
		// Errors are expected and acceptable; panics and hangs are not.
		Parse(data) //nolint:errcheck
	})
}

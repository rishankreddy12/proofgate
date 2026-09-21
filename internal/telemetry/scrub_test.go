package telemetry

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScrubber(t *testing.T) {
	var buf bytes.Buffer
	s := NewScrubber(slog.NewJSONHandler(&buf, nil))
	s.Register("sk-registered-value-123")
	s.Register("short") // ignored: too short to register safely
	log := slog.New(s).With("provider", "openai")

	log.Error("call failed with key sk-registered-value-123",
		"err", errors.New("upstream said: invalid key sk-proj-abcdefghijklmnopqrstuvwxyz"),
		slog.Group("req", "auth", "Bearer pg_live_"+"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"),
		"note", "a short word")
	out := buf.String()
	require.NotContains(t, out, "sk-registered-value-123")
	require.NotContains(t, out, "sk-proj-abcdef")
	require.NotContains(t, out, "pg_live_ABCD")
	require.Contains(t, out, "[REDACTED]")
	require.Contains(t, out, "[REDACTED:SECRET]")
	require.Contains(t, out, "a short word")
	require.Contains(t, out, `"provider":"openai"`)
}

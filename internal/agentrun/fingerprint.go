// Package agentrun enforces per-run budgets and stops agents that repeat themselves.
package agentrun

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
)

var (
	digits = regexp.MustCompile(`\d+`)
	spaces = regexp.MustCompile(`\s+`)
)

func Normalize(s string) string {
	s = strings.ToLower(s)
	s = digits.ReplaceAllString(s, "#")
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if len(s) > 2000 {
		s = s[:2000]
	}
	return s
}

// Fingerprint identifies "the step the agent is taking": its last message plus the tool calls it made last.
func Fingerprint(req *api.ChatRequest) string {
	h := sha256.New()
	n := len(req.Messages)
	if n > 0 {
		last := req.Messages[n-1]
		h.Write([]byte(last.Role + "\x00" + Normalize(last.Content.PlainText()) + "\x00"))
	}
	for i := n - 1; i >= 0; i-- {
		if req.Messages[i].Role == "assistant" {
			for _, tc := range req.Messages[i].ToolCalls {
				h.Write([]byte(tc.Function.Name + "\x00" + Normalize(tc.Function.Arguments) + "\x00"))
			}
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

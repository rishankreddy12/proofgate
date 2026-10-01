// Package cache implements the exact and semantic response caches.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
)

// ScopeIncludedFields enumerates the api.ChatRequest fields that are hashed into
// the cache key scope. Any field not listed here must be listed in ScopeExcludedFields.
var ScopeIncludedFields = map[string]struct{}{
	"Model":               {},
	"MaxTokens":           {},
	"MaxCompletionTokens": {},
	"Temperature":         {},
	"TopP":                {},
	"Stop":                {},
	"Tools":               {},
	"ToolChoice":          {},
	"ResponseFormat":      {},
	"Seed":                {},
	"N":                   {},
	"PresencePenalty":     {},
	"FrequencyPenalty":    {},
	"ParallelToolCalls":   {},
	"ReasoningEffort":     {},
	"Logprobs":            {},
	"TopLogprobs":         {},
}

// ScopeExcludedFields enumerates the api.ChatRequest fields that are explicitly excluded
// from the cache key scope, along with justification.
var ScopeExcludedFields = map[string]string{
	"Messages":      "messages are hashed separately in ExactHash and ContextHash",
	"Stream":        "stream flag affects delivery format, not completion content",
	"StreamOptions": "stream options do not alter completion content",
	"User":          "conditionally included in Scope only when cfg.PerUser is true",
}

type Plan struct {
	Exact    bool
	Semantic bool
	Bypass   string
}

func usesTools(r *api.ChatRequest) bool {
	if len(r.Tools) > 0 || len(r.ToolChoice) > 0 {
		return true
	}
	for _, m := range r.Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func Eligibility(req *api.ChatRequest, hdr http.Header, cfg config.CacheConfig, internal ...bool) Plan {
	if cfg.Mode == "" || cfg.Mode == "off" {
		return Plan{}
	}
	isInternal := len(internal) > 0 && internal[0]
	if strings.EqualFold(hdr.Get("X-ProofGate-Cache"), "off") || isInternal {
		return Plan{Bypass: "header"}
	}
	if usesTools(req) {
		return Plan{Bypass: "tools"}
	}
	p := Plan{Exact: cfg.Exact}
	if !cfg.Semantic || len(req.ResponseFormat) > 0 {
		return p
	}
	users := 0
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
		case "user":
			users++
			if m.Content.Parts != nil {
				for _, part := range m.Content.Parts {
					if part.Type != "text" {
						return p
					}
				}
			}
		default:
			return p
		}
	}
	p.Semantic = users == 1
	return p
}

func sum(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p)))) // length prefix: ("ab","c") != ("a","bc")
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fptr(f *float64) string {
	if f == nil {
		return "-"
	}
	return strconv.FormatFloat(*f, 'g', -1, 64)
}

func iptr(i *int) string {
	if i == nil {
		return "-"
	}
	return strconv.Itoa(*i)
}

func bptr(b *bool) string {
	if b == nil {
		return "-"
	}
	return strconv.FormatBool(*b)
}

// ContextHash returns a SHA-256 hex digest of all message content except the last user message.
// This isolates RAG documents and multi-turn history across requests.
func ContextHash(req *api.ChatRequest) string {
	lastUserIdx := -1
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}
	h := sha256.New()
	for i, m := range req.Messages {
		if i == lastUserIdx {
			continue
		}
		h.Write([]byte(strconv.Itoa(len(m.Role))))
		h.Write([]byte{':'})
		h.Write([]byte(m.Role))
		h.Write([]byte{':'})
		text := m.Content.PlainText()
		h.Write([]byte(strconv.Itoa(len(text))))
		h.Write([]byte{':'})
		h.Write([]byte(text))
		if len(m.ToolCalls) > 0 {
			b, _ := json.Marshal(m.ToolCalls)
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func Scope(tenantID, route string, req *api.ChatRequest, cfg config.CacheConfig) string {
	var system []string
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			system = append(system, m.Content.PlainText())
		}
	}
	user := ""
	ctxHash := ""
	if cfg.PerUser {
		user = req.User
		ctxHash = ContextHash(req)
	}
	seed := "-"
	if req.Seed != nil {
		seed = strconv.Itoa(*req.Seed)
	}
	toolsJSON := ""
	if len(req.Tools) > 0 {
		b, _ := json.Marshal(req.Tools)
		toolsJSON = string(b)
	}

	return sum(
		"v2:v"+strconv.Itoa(cfg.Version),
		route,
		tenantID,
		user,
		ctxHash,
		req.Model,
		strings.Join(system, "\x00"),
		fptr(req.Temperature),
		fptr(req.TopP),
		strconv.Itoa(req.EffectiveMaxTokens(0)),
		strings.Join(req.Stop, "\x00"),
		seed,
		iptr(req.N),
		fptr(req.PresencePenalty),
		fptr(req.FrequencyPenalty),
		bptr(req.ParallelToolCalls),
		req.ReasoningEffort,
		bptr(req.Logprobs),
		iptr(req.TopLogprobs),
		toolsJSON,
		string(req.ToolChoice),
		string(req.ResponseFormat),
	)
}

func ExactHash(scope string, req *api.ChatRequest) string {
	b, _ := json.Marshal(req.Messages)
	return sum(scope, string(b))
}

func SemanticText(req *api.ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content.PlainText()
		}
	}
	return ""
}

// SemanticTextFromHeader returns the query text to use for semantic embeddings.
// If allowClientKey is true and the X-ProofGate-Cache-Query header is provided,
// it uses that text instead of extracting from the full request messages (e.g. for RAG
// workloads where the user message contains massive context documents).
// When allowClientKey is false, the header is ignored.
func SemanticTextFromHeader(req *api.ChatRequest, hdr http.Header, allowClientKey ...bool) string {
	allowed := false
	if len(allowClientKey) > 0 {
		allowed = allowClientKey[0]
	}
	if hdr != nil {
		if q := strings.TrimSpace(hdr.Get("X-ProofGate-Cache-Query")); q != "" {
			if allowed {
				return q
			}
			slog.Debug("ignoring X-ProofGate-Cache-Query: client key not allowed for tenant/route")
		}
	}
	return SemanticText(req)
}

var tagRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

func ParseTags(h string) []string {
	var out []string
	for _, t := range strings.Split(h, ",") {
		t = strings.TrimSpace(t)
		if tagRe.MatchString(t) {
			out = append(out, t)
		}
		if len(out) == 8 {
			break
		}
	}
	return out
}

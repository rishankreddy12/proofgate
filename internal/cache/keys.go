// Package cache implements the exact and semantic response caches.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
)

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

func Eligibility(req *api.ChatRequest, hdr http.Header, cfg config.CacheConfig) Plan {
	if cfg.Mode == "" || cfg.Mode == "off" {
		return Plan{}
	}
	if strings.EqualFold(hdr.Get("X-ProofGate-Cache"), "off") {
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

func Scope(tenantID, route string, req *api.ChatRequest, cfg config.CacheConfig) string {
	var system []string
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			system = append(system, m.Content.PlainText())
		}
	}
	user := ""
	if cfg.PerUser {
		user = req.User
	}
	seed := "-"
	if req.Seed != nil {
		seed = strconv.Itoa(*req.Seed)
	}
	return sum("v"+strconv.Itoa(cfg.Version), route, tenantID, user, strings.Join(system, "\x00"),
		fptr(req.Temperature), fptr(req.TopP), strconv.Itoa(req.EffectiveMaxTokens(0)),
		strings.Join(req.Stop, "\x00"), seed, string(req.ResponseFormat))
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

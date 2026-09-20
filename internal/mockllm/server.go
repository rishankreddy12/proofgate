// Package mockllm is a controllable OpenAI-compatible provider for tests, chaos drills and benchmarks.
package mockllm

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/sse"
)

type Mode struct {
	TTFTMs       int     `json:"ttft_ms"`
	TokensPerSec int     `json:"tokens_per_sec"` // 0 = no delay between tokens
	ErrorRate    float64 `json:"error_rate"`
	ErrorStatus  int     `json:"error_status"`
	Reply        string  `json:"reply"`
}

type Server struct {
	name     string
	mu       sync.RWMutex
	mode     Mode
	requests atomic.Int64
}

func New(name string, m Mode) *Server { return &Server{name: name, mode: m} }

func (s *Server) SetMode(m Mode)  { s.mu.Lock(); s.mode = m; s.mu.Unlock() }
func (s *Server) getMode() Mode   { s.mu.RLock(); defer s.mu.RUnlock(); return s.mode }
func (s *Server) Requests() int64 { return s.requests.Load() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	mux.HandleFunc("POST /v1/embeddings", s.embeddings)
	mux.HandleFunc("GET /admin/mode", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(s.getMode())
	})
	mux.HandleFunc("PUT /admin/mode", func(w http.ResponseWriter, r *http.Request) {
		var m Mode
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.SetMode(m)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return mux
}

func writeErr(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":"mock error %d","type":"mock","code":"mock_%d"}}`, status, status)
}

func (s *Server) failStatus(r *http.Request, m Mode) int {
	if v := r.Header.Get("X-Mock-Status"); v != "" {
		n, _ := strconv.Atoi(v)
		return n
	}
	if m.ErrorRate > 0 && rand.Float64() < m.ErrorRate {
		if m.ErrorStatus == 0 {
			return 500
		}
		return m.ErrorStatus
	}
	return 0
}

func (s *Server) reply(req *api.ChatRequest, m Mode) []string {
	if m.Reply != "" {
		return strings.Fields(m.Reply)
	}
	maxTok := min(req.EffectiveMaxTokens(32), 32)
	last := ""
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			last = msg.Content.PlainText()
		}
	}
	words := strings.Fields("echo: " + last)
	for len(words) < maxTok {
		words = append(words, "lorem")
	}
	return words[:maxTok]
}

func promptTokens(req *api.ChatRequest) int {
	n := 0
	for _, m := range req.Messages {
		n += len(strings.Fields(m.Content.PlainText()))
	}
	return n
}

func sleepCtx(r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-time.After(d):
		return true
	case <-r.Context().Done():
		return false
	}
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	m := s.getMode()
	var req api.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400)
		return
	}
	ttft := time.Duration(m.TTFTMs) * time.Millisecond
	if v := r.Header.Get("X-Mock-TTFT-Ms"); v != "" {
		n, _ := strconv.Atoi(v)
		ttft = time.Duration(n) * time.Millisecond
	}
	if st := s.failStatus(r, m); st != 0 {
		writeErr(w, st)
		return
	}
	if !sleepCtx(r, ttft) {
		return
	}
	words := s.reply(&req, m)
	finish := "stop"
	if m.Reply == "" && len(words) >= req.EffectiveMaxTokens(32) {
		finish = "length"
	}
	usage := &api.Usage{PromptTokens: promptTokens(&req), CompletionTokens: len(words)}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	id := fmt.Sprintf("mock-%s-%d", s.name, time.Now().UnixNano())
	now := time.Now().Unix()
	var gap time.Duration
	if m.TokensPerSec > 0 {
		gap = time.Second / time.Duration(m.TokensPerSec)
	}

	if !req.Stream {
		if !sleepCtx(r, gap*time.Duration(len(words))) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ChatResponse{ID: id, Object: "chat.completion", Created: now, Model: req.Model,
			Choices: []api.Choice{{Message: api.Message{Role: "assistant", Content: api.Content{Text: strings.Join(words, " ")}}, FinishReason: finish}},
			Usage:   usage})
		return
	}
	sw, err := sse.NewWriter(w)
	if err != nil {
		writeErr(w, 500)
		return
	}
	chunk := func(d api.ChunkDelta, fr *string) api.ChatChunk {
		return api.ChatChunk{ID: id, Object: "chat.completion.chunk", Created: now, Model: req.Model,
			Choices: []api.ChunkChoice{{Delta: d, FinishReason: fr}}}
	}
	for i, word := range words {
		d := api.ChunkDelta{Content: " " + word}
		if i == 0 {
			d = api.ChunkDelta{Role: "assistant", Content: word}
		}
		if sw.Data(chunk(d, nil)) != nil || !sleepCtx(r, gap) {
			return
		}
	}
	_ = sw.Data(chunk(api.ChunkDelta{}, &finish))
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		_ = sw.Data(api.ChatChunk{ID: id, Object: "chat.completion.chunk", Created: now, Model: req.Model,
			Choices: []api.ChunkChoice{}, Usage: usage})
	}
	_ = sw.Done()
}

func (s *Server) embeddings(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	var req api.EmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400)
		return
	}
	if st := s.failStatus(r, s.getMode()); st != 0 {
		writeErr(w, st)
		return
	}
	dim := 256
	if req.Dimensions != nil && *req.Dimensions > 0 {
		dim = *req.Dimensions
	}
	out := api.EmbeddingResponse{Object: "list", Model: req.Model}
	tokens := 0
	for i, in := range req.Input {
		out.Data = append(out.Data, api.Embedding{Object: "embedding", Index: i, Embedding: HashEmbedding(in, dim)})
		tokens += len(strings.Fields(in))
	}
	out.Usage = api.Usage{PromptTokens: tokens, TotalTokens: tokens}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

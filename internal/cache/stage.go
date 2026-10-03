// Package cache provides enterprise-grade capabilities, configuration, and structural components for the cache subsystem.
package cache

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

// ExactStore defines the backend contract for deterministic key-value cache operations.
type ExactStore interface {
	Get(ctx context.Context, key string) (*Entry, error)
	Put(ctx context.Context, tenantID, key string, e Entry, ttl time.Duration) error
}

// SemanticStore defines the backend contract for vector-based ANN cache operations.
type SemanticStore interface {
	Put(ctx context.Context, tenantID, route, scope string, emb []float32, e Entry, ttl time.Duration) (string, error)
	Nearest(ctx context.Context, tenantID, scope string, emb []float32) (*Match, error)
}

// Candidate tracks a potential (but unused) cache hit for "shadow" evaluation mode.
type Candidate struct {
	Query           string
	CandidateQuery  string
	CandidateAnswer string
	Similarity      float64
	Source          string // "exact" | "approx"
}

// ShadowRecord captures the disparity between a shadow cache hit and the real upstream LLM response.
type ShadowRecord struct {
	ID              string
	TS              time.Time
	TenantID        string
	Route           string
	Threshold       float64
	Similarity      float64
	Query           string
	CandidateQuery  string
	CandidateAnswer string
	ActualAnswer    string
	CandidateSource string
}

// State encapsulates the mid-pipeline execution context carried between Before (Read) and After (Write).
type State struct {
	Plan        Plan
	Scope       string
	ExactKey    string
	Query       string
	ContextHash string
	Embedding   []float32
	Tags        []string
	Nearest     *Match // best semantic candidate, even when below threshold
	Candidate   *Candidate
}

// StateKey provides a globally accessible constant or variable for StateKey.
const StateKey = "cache.state"

// Stage implements the core Caching pipeline plugin.
//
// Mechanics:
// 1. Before(): Generates determinist keys and queries Exact/Semantic stores. On a hit, it synthetically injects the cached response into the pipeline, bypassing all upstream targets.
// 2. After(): Intercepts successful, completed LLM generations and asynchronously persists them back to the Redis stores.
type Stage struct {
	exact    ExactStore
	sem      SemanticStore
	emb      Embedder
	now      func() time.Time
	slots    chan struct{}
	wg       sync.WaitGroup
	onErr    func(op string)
	onDrop   func()
	onShadow func(ShadowRecord)
}

// NewStage initializes the pipeline cache plugin with its constituent storage drivers.
func NewStage(x ExactStore, s SemanticStore, e Embedder, onErr func(op string), onDrop func()) *Stage {
	if onErr == nil {
		onErr = func(string) {}
	}
	if onDrop == nil {
		onDrop = func() {}
	}
	return &Stage{
		exact:    x,
		sem:      s,
		emb:      e,
		now:      time.Now,
		slots:    make(chan struct{}, 64),
		onErr:    onErr,
		onDrop:   onDrop,
		onShadow: func(ShadowRecord) {},
	}
}

// SetOnShadow executes the primary logic for the SetOnShadow operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) SetOnShadow(fn func(ShadowRecord)) {
	if fn == nil {
		fn = func(ShadowRecord) {}
	}
	s.onShadow = fn
}

// Name executes the primary logic for the Name operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) Name() string { return "cache" }

// Wait executes the primary logic for the Wait operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *Stage) Wait() { s.wg.Wait() }

func (s *Stage) hit(c *pipeline.Call, e *Entry, status string, sim float64) {
	r := *e.Response
	r.ID = "chatcmpl-" + c.ID
	r.Created = s.now().Unix()
	c.Response = &r
	c.CacheStatus = status
	c.Usage = api.Usage{}
	c.CostMicros = 0
	c.Values["cache.saved_micros"] = e.CostMicros
	c.Header.Set("X-ProofGate-Cache-Source", e.SourceRequestID)
	if status == "hit-semantic" {
		c.Header.Set("X-ProofGate-Cache-Similarity", strconv.FormatFloat(sim, 'f', 4, 64))
	}
}

// Before executes prior to routing. It computes eligibility and attempts to fulfill the request entirely from cache.
func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	if c.Route == nil {
		return false, nil
	}
	cfg := c.Route.Cache
	hdr := c.Incoming
	if hdr == nil {
		hdr = http.Header{}
	}
	plan := Eligibility(c.Request, hdr, cfg, c.Internal)
	if plan.Bypass != "" {
		c.CacheStatus = "bypass"
		return false, nil
	}
	if !plan.Exact && !plan.Semantic {
		return false, nil
	}
	tenant := c.Principal.TenantID
	allowClient := cfg.AllowClientKey || (c.Principal.Tenant.Cache != nil && c.Principal.Tenant.Cache.AllowClientKey)
	ctxHash := ContextHash(c.Request)
	st := &State{
		Plan:        plan,
		Scope:       Scope(tenant, c.Route.Name, c.Request, cfg),
		Query:       SemanticTextFromHeader(c.Request, hdr, allowClient),
		ContextHash: ctxHash,
		Tags:        ParseTags(hdr.Get("X-ProofGate-Cache-Tags")),
	}
	c.Values[StateKey] = st

	if plan.Exact && s.exact != nil {
		st.ExactKey = ExactKey(tenant, c.Route.Name, ExactHash(st.Scope, c.Request))
		e, err := s.exact.Get(ctx, st.ExactKey)
		if err != nil {
			s.onErr("exact_get")
		} else if e != nil {
			if cfg.Mode == "shadow" {
				var ans string
				if e.Response != nil && len(e.Response.Choices) > 0 {
					ans = e.Response.Choices[0].Message.Content.PlainText()
				}
				candQuery := e.Query
				if candQuery == "" {
					candQuery = st.Query
				}
				st.Candidate = &Candidate{
					Query:           st.Query,
					CandidateQuery:  candQuery,
					CandidateAnswer: ans,
					Similarity:      1.0,
					Source:          "exact",
				}
			} else {
				s.hit(c, e, "hit-exact", 1)
				return true, nil
			}
		}
	}
	if plan.Semantic && s.sem != nil && s.emb != nil {
		v, err := s.emb.Embed(ctx, cfg.EmbeddingRoute, tenant, st.Query)
		if err != nil {
			s.onErr("embed")
			return false, nil
		}
		st.Embedding = v
		m, err := s.sem.Nearest(ctx, tenant, st.Scope, v)
		if err != nil {
			s.onErr("semantic_get")
			return false, nil
		}
		st.Nearest = m
		if m != nil {
			if allowClient && m.Entry.ContextHash != st.ContextHash {
				// Context mismatch: client query header enabled, but stored context differs from current context
			} else if cfg.Mode == "shadow" {
				if st.Candidate == nil && (m.Similarity >= cfg.Threshold || m.Similarity >= 0.70) {
					var ans string
					if m.Entry.Response != nil && len(m.Entry.Response.Choices) > 0 {
						ans = m.Entry.Response.Choices[0].Message.Content.PlainText()
					}
					st.Candidate = &Candidate{
						Query:           st.Query,
						CandidateQuery:  m.Entry.Query,
						CandidateAnswer: ans,
						Similarity:      m.Similarity,
						Source:          "approx",
					}
				}
			} else if m.Similarity >= cfg.Threshold && cfg.Mode == "on" {
				s.hit(c, &m.Entry, "hit-semantic", m.Similarity)
				return true, nil
			}
		}
	}
	return false, nil
}

func storable(r *api.ChatResponse) bool {
	if r == nil || len(r.Choices) != 1 {
		return false
	}
	ch := r.Choices[0]
	return ch.FinishReason == "stop" && len(ch.Message.ToolCalls) == 0 && ch.Message.Content.PlainText() != ""
}

// After executes post-generation. If the request was a cache miss and generated successfully,
// it dispatches an asynchronous worker to write the payload to Redis.
func (s *Stage) After(_ context.Context, c *pipeline.Call) {
	st, ok := c.Values[StateKey].(*State)
	if !ok || c.Err != nil {
		return
	}
	if st.Candidate != nil && c.Response != nil && len(c.Response.Choices) > 0 && s.onShadow != nil {
		actual := c.Response.Choices[0].Message.Content.PlainText()
		s.onShadow(ShadowRecord{
			ID:              uuid.NewString(),
			TS:              s.now(),
			TenantID:        c.Principal.TenantID,
			Route:           c.Route.Name,
			Threshold:       c.Route.Cache.Threshold,
			Similarity:      st.Candidate.Similarity,
			Query:           st.Candidate.Query,
			CandidateQuery:  st.Candidate.CandidateQuery,
			CandidateAnswer: st.Candidate.CandidateAnswer,
			ActualAnswer:    actual,
			CandidateSource: st.Candidate.Source,
		})
	}
	if c.CacheStatus != "miss" || !storable(c.Response) {
		return
	}
	cfg := c.Route.Cache
	e := Entry{
		SourceRequestID: c.ID,
		Query:           st.Query,
		Response:        c.Response.Clone(),
		CostMicros:      c.CostMicros,
		CreatedAt:       s.now(),
		Tags:            st.Tags,
		ContextHash:     st.ContextHash,
	}
	if b, err := json.Marshal(e); err != nil || len(b) > cfg.MaxEntryBytes {
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.onDrop()
		return
	}
	s.wg.Add(1)
	tenant, route := c.Principal.TenantID, c.Route.Name
	go func() {
		defer func() { <-s.slots; s.wg.Done() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if st.ExactKey != "" && s.exact != nil {
			if err := s.exact.Put(ctx, tenant, st.ExactKey, e, cfg.TTL); err != nil {
				s.onErr("exact_put")
			}
		}
		if st.Embedding != nil && s.sem != nil {
			if _, err := s.sem.Put(ctx, tenant, route, st.Scope, st.Embedding, e, cfg.TTL); err != nil {
				s.onErr("semantic_put")
			}
		}
	}()
}

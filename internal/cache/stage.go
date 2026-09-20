package cache

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

type ExactStore interface {
	Get(ctx context.Context, key string) (*Entry, error)
	Put(ctx context.Context, tenantID, key string, e Entry, ttl time.Duration) error
}

type SemanticStore interface {
	Put(ctx context.Context, tenantID, route, scope string, emb []float32, e Entry, ttl time.Duration) (string, error)
	Nearest(ctx context.Context, tenantID, scope string, emb []float32) (*Match, error)
}

// State is what the stage learned in Before. Plan 3's shadow recorder reads it.
type State struct {
	Plan      Plan
	Scope     string
	ExactKey  string
	Query     string
	Embedding []float32
	Tags      []string
	Nearest   *Match // best semantic candidate, even when below threshold
}

const StateKey = "cache.state"

type Stage struct {
	exact  ExactStore
	sem    SemanticStore
	emb    Embedder
	now    func() time.Time
	slots  chan struct{}
	wg     sync.WaitGroup
	onErr  func(op string)
	onDrop func()
}

func NewStage(x ExactStore, s SemanticStore, e Embedder, onErr func(op string), onDrop func()) *Stage {
	if onErr == nil {
		onErr = func(string) {}
	}
	if onDrop == nil {
		onDrop = func() {}
	}
	return &Stage{exact: x, sem: s, emb: e, now: time.Now, slots: make(chan struct{}, 64), onErr: onErr, onDrop: onDrop}
}

func (s *Stage) Name() string { return "cache" }
func (s *Stage) Wait()        { s.wg.Wait() }

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

func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	if c.Route == nil {
		return false, nil
	}
	cfg := c.Route.Cache
	hdr := c.Incoming
	if hdr == nil {
		hdr = http.Header{}
	}
	plan := Eligibility(c.Request, hdr, cfg)
	if plan.Bypass != "" {
		c.CacheStatus = "bypass"
		return false, nil
	}
	if !plan.Exact && !plan.Semantic {
		return false, nil
	}
	tenant := c.Principal.TenantID
	st := &State{Plan: plan, Scope: Scope(tenant, c.Route.Name, c.Request, cfg), Tags: ParseTags(hdr.Get("X-ProofGate-Cache-Tags"))}
	c.Values[StateKey] = st

	if plan.Exact && s.exact != nil {
		st.ExactKey = ExactKey(tenant, c.Route.Name, ExactHash(st.Scope, c.Request))
		e, err := s.exact.Get(ctx, st.ExactKey)
		if err != nil {
			s.onErr("exact_get")
		} else if e != nil {
			s.hit(c, e, "hit-exact", 1)
			return true, nil
		}
	}
	if plan.Semantic && s.sem != nil && s.emb != nil {
		st.Query = SemanticText(c.Request)
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
		if m != nil && m.Similarity >= cfg.Threshold && cfg.Mode == "on" {
			s.hit(c, &m.Entry, "hit-semantic", m.Similarity)
			return true, nil
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

func (s *Stage) After(_ context.Context, c *pipeline.Call) {
	st, ok := c.Values[StateKey].(*State)
	if !ok || c.CacheStatus != "miss" || c.Err != nil || !storable(c.Response) {
		return
	}
	cfg := c.Route.Cache
	e := Entry{SourceRequestID: c.ID, Query: st.Query, Response: c.Response, CostMicros: c.CostMicros, CreatedAt: s.now(), Tags: st.Tags}
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

package agentrun

import (
	"context"
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/redis/go-redis/v9"
)

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, route, tenantID, text string) ([]float32, error)
}

// FuzzyStore persists recent step embeddings for a run.
type FuzzyStore interface {
	RecentEmbeddings(ctx context.Context, tenantID, runID string, window int) ([][]float32, error)
	PushEmbedding(ctx context.Context, tenantID, runID string, emb []float32, window int, ttl time.Duration) error
}

// EncodeEmbedding serializes a float32 slice into little-endian bytes.
func EncodeEmbedding(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// DecodeEmbedding deserializes little-endian bytes into a float32 slice.
func DecodeEmbedding(b []byte) []float32 {
	n := len(b) / 4
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// CosineSimilarity computes the normalized dot product of two vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// StepText extracts the representative action of the current request step.
func StepText(req *api.ChatRequest) string {
	if req == nil {
		return ""
	}
	var sb strings.Builder
	n := len(req.Messages)
	if n > 0 {
		last := req.Messages[n-1]
		sb.WriteString(last.Role)
		sb.WriteString(": ")
		sb.WriteString(last.Content.PlainText())
		sb.WriteString("\n")
	}
	for i := n - 1; i >= 0; i-- {
		if req.Messages[i].Role == "assistant" {
			for _, tc := range req.Messages[i].ToolCalls {
				sb.WriteString("call ")
				sb.WriteString(tc.Function.Name)
				sb.WriteString(": ")
				sb.WriteString(tc.Function.Arguments)
				sb.WriteString("\n")
			}
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

// RedisFuzzyStore implements FuzzyStore on top of Redis lists.
type RedisFuzzyStore struct {
	rdb redis.UniversalClient
}

func NewRedisFuzzyStore(rdb redis.UniversalClient) *RedisFuzzyStore {
	return &RedisFuzzyStore{rdb: rdb}
}

func fuzzyKey(tenantID, runID string) string {
	return "runemb:{t:" + tenantID + "}:" + runID
}

func (s *RedisFuzzyStore) RecentEmbeddings(ctx context.Context, tenantID, runID string, window int) ([][]float32, error) {
	if window <= 0 {
		window = 20
	}
	k := fuzzyKey(tenantID, runID)
	items, err := s.rdb.LRange(ctx, k, 0, int64(window-1)).Result()
	if err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(items))
	for _, it := range items {
		out = append(out, DecodeEmbedding([]byte(it)))
	}
	return out, nil
}

func (s *RedisFuzzyStore) PushEmbedding(ctx context.Context, tenantID, runID string, emb []float32, window int, ttl time.Duration) error {
	k := fuzzyKey(tenantID, runID)
	data := EncodeEmbedding(emb)
	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, k, data)
	pipe.LTrim(ctx, k, 0, int64(window-1))
	if ttl > 0 {
		pipe.PExpire(ctx, k, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// MemFuzzyStore provides an in-memory FuzzyStore for unit testing.
type MemFuzzyStore struct {
	mu sync.Mutex
	m  map[string][][]float32
}

func NewMemFuzzyStore() *MemFuzzyStore {
	return &MemFuzzyStore{m: map[string][][]float32{}}
}

func (m *MemFuzzyStore) RecentEmbeddings(_ context.Context, tenantID, runID string, window int) ([][]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + ":" + runID
	history := m.m[key]
	if len(history) > window {
		history = history[:window]
	}
	out := make([][]float32, len(history))
	copy(out, history)
	return out, nil
}

func (m *MemFuzzyStore) PushEmbedding(_ context.Context, tenantID, runID string, emb []float32, window int, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tenantID + ":" + runID
	cp := make([]float32, len(emb))
	copy(cp, emb)
	list := append([][]float32{cp}, m.m[key]...)
	if len(list) > window {
		list = list[:window]
	}
	m.m[key] = list
	return nil
}

// FuzzyDetector evaluates semantic loop similarity for agent runs.
type FuzzyDetector struct {
	emb   Embedder
	store FuzzyStore
}

func NewFuzzyDetector(emb Embedder, store FuzzyStore) *FuzzyDetector {
	return &FuzzyDetector{emb: emb, store: store}
}

// Check compares the current step embedding against recent history and saves it.
func (fd *FuzzyDetector) Check(ctx context.Context, tenantID, runID string, p store.RunPolicy, req *api.ChatRequest) (maxSim float64, repeats int, isLoop bool, err error) {
	if fd == nil || fd.emb == nil || fd.store == nil || req == nil {
		return 0, 0, false, nil
	}

	text := StepText(req)
	if text == "" {
		return 0, 0, false, nil
	}

	vec, err := fd.emb.Embed(ctx, "", tenantID, text)
	if err != nil {
		return 0, 0, false, err
	}

	history, err := fd.store.RecentEmbeddings(ctx, tenantID, runID, p.LoopWindow)
	if err != nil {
		return 0, 0, false, err
	}

	repeats = 1
	thresh := p.FuzzyThreshold
	if thresh <= 0 {
		thresh = 0.95
	}

	for _, past := range history {
		sim := CosineSimilarity(vec, past)
		if sim > maxSim {
			maxSim = sim
		}
		if sim >= thresh {
			repeats++
		}
	}

	_ = fd.store.PushEmbedding(ctx, tenantID, runID, vec, p.LoopWindow, p.TTL)

	if repeats >= p.LoopRepeats {
		return maxSim, repeats, true, nil
	}

	return maxSim, repeats, false, nil
}

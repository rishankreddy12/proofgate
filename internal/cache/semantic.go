package cache

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Semantic struct {
	rdb     *redis.Client
	mu      sync.Mutex
	indexes map[int]bool
}

// NewSemantic needs a client created with Protocol: 2 (go-redis search replies are parsed as RESP2).
func NewSemantic(rdb *redis.Client) *Semantic { return &Semantic{rdb: rdb, indexes: map[int]bool{}} }

func indexName(dim int) string { return "idx:sc:" + strconv.Itoa(dim) }
func prefix(dim int) string    { return "sc:" + strconv.Itoa(dim) + ":" }

func (s *Semantic) ensureIndex(ctx context.Context, dim int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexes[dim] {
		return nil
	}
	// Equivalent raw command:
	// FT.CREATE idx:sc:<dim> ON HASH PREFIX 1 sc:<dim>: SCHEMA tenant TAG scope TAG route TAG
	//   tags TAG SEPARATOR , created NUMERIC emb VECTOR HNSW 6 TYPE FLOAT32 DIM <dim> DISTANCE_METRIC COSINE
	err := s.rdb.FTCreate(ctx, indexName(dim),
		&redis.FTCreateOptions{OnHash: true, Prefix: []interface{}{prefix(dim)}},
		&redis.FieldSchema{FieldName: "tenant", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "scope", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "route", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "tags", FieldType: redis.SearchFieldTypeTag, Separator: ","},
		&redis.FieldSchema{FieldName: "created", FieldType: redis.SearchFieldTypeNumeric},
		&redis.FieldSchema{FieldName: "emb", FieldType: redis.SearchFieldTypeVector, VectorArgs: &redis.FTVectorArgs{
			HNSWOptions: &redis.FTHNSWOptions{Type: "FLOAT32", Dim: dim, DistanceMetric: "COSINE"}}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "Index already exists") {
		return err
	}
	s.indexes[dim] = true
	return nil
}

func floatBytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

// escapeTag escapes RediSearch TAG punctuation (tenant ids are UUIDs with '-').
func escapeTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(",.<>{}[]\"':;!@#$%^&*()-+=~| /\\", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *Semantic) Put(ctx context.Context, tenantID, route, scope string, emb []float32, e Entry, ttl time.Duration) (string, error) {
	dim := len(emb)
	if err := s.ensureIndex(ctx, dim); err != nil {
		return "", err
	}
	entry, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	key := prefix(dim) + tenantTag(tenantID) + ":" + route + ":" + uuid.NewString()
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, "tenant", tenantID, "scope", scope, "route", route, "tags", strings.Join(e.Tags, ","),
		"created", e.CreatedAt.Unix(), "query", e.Query, "entry", entry, "emb", floatBytes(emb))
	pipe.Expire(ctx, key, ttl)
	for _, t := range e.Tags {
		tk := TagKey(tenantID, t)
		pipe.SAdd(ctx, tk, key)
		pipe.Expire(ctx, tk, ttl)
	}
	_, err = pipe.Exec(ctx)
	return key, err
}

func (s *Semantic) Nearest(ctx context.Context, tenantID, scope string, emb []float32) (*Match, error) {
	dim := len(emb)
	if err := s.ensureIndex(ctx, dim); err != nil {
		return nil, err
	}
	q := fmt.Sprintf("(@tenant:{%s} @scope:{%s})=>[KNN 1 @emb $vec AS dist]", escapeTag(tenantID), escapeTag(scope))
	res, err := s.rdb.FTSearchWithArgs(ctx, indexName(dim), q, &redis.FTSearchOptions{
		Params:         map[string]interface{}{"vec": floatBytes(emb)},
		SortBy:         []redis.FTSearchSortBy{{FieldName: "dist", Asc: true}},
		Return:         []redis.FTSearchReturn{{FieldName: "dist"}, {FieldName: "entry"}},
		LimitOffset:    0,
		Limit:          1,
		DialectVersion: 2,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(res.Docs) == 0 {
		return nil, nil
	}
	d := res.Docs[0]
	dist, err := strconv.ParseFloat(d.Fields["dist"], 64)
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal([]byte(d.Fields["entry"]), &e); err != nil {
		return nil, err
	}
	return &Match{Entry: e, Similarity: 1 - dist, Key: d.ID}, nil
}

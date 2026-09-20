//go:build integration

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/stretchr/testify/require"
)

func resp(text string) *api.ChatResponse {
	return &api.ChatResponse{ID: "x", Object: "chat.completion", Model: "m",
		Choices: []api.Choice{{Message: api.Message{Role: "assistant", Content: api.Content{Text: text}}, FinishReason: "stop"}}}
}

func TestExactRoundTripAndTags(t *testing.T) {
	rdb := redisClient(t)
	x := NewExact(rdb)
	ctx := context.Background()
	key := ExactKey("t1", "default", "abc")
	require.Equal(t, "cache:{t:t1}:x:default:abc", key)

	got, err := x.Get(ctx, key)
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, x.Put(ctx, "t1", key, Entry{SourceRequestID: "r1", Response: resp("hello"), CostMicros: 42,
		CreatedAt: time.Now(), Tags: []string{"faq"}}, time.Minute))
	got, err = x.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "hello", got.Response.Choices[0].Message.Content.Text)
	require.EqualValues(t, 42, got.CostMicros)

	members, err := rdb.SMembers(ctx, TagKey("t1", "faq")).Result()
	require.NoError(t, err)
	require.Equal(t, []string{key}, members)

	ttl, err := rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 50*time.Second)
}

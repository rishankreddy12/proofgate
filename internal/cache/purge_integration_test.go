//go:build integration

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPurgeTagsAndTenant(t *testing.T) {
	rdb := redisClient(t)
	x := NewExact(rdb)
	s := NewSemantic(rdb)
	ctx := context.Background()

	// Put entries for tenant t1
	k1 := ExactKey("t1", "default", "hash1")
	require.NoError(t, x.Put(ctx, "t1", k1, Entry{SourceRequestID: "r1", Response: resp("res1"), Tags: []string{"auth"}}, time.Hour))

	k2 := ExactKey("t1", "default", "hash2")
	require.NoError(t, x.Put(ctx, "t1", k2, Entry{SourceRequestID: "r2", Response: resp("res2"), Tags: []string{"pricing"}}, time.Hour))

	k3 := ExactKey("t1", "default", "hash3")
	require.NoError(t, x.Put(ctx, "t1", k3, Entry{SourceRequestID: "r3", Response: resp("res3")}, time.Hour))

	semKey, err := s.Put(ctx, "t1", "default", "scope1", []float32{1, 0, 0, 0}, Entry{SourceRequestID: "rs1", Response: resp("sem1"), Tags: []string{"auth"}}, time.Hour)
	require.NoError(t, err)

	// Put entry for tenant t2
	k4 := ExactKey("t2", "default", "hash4")
	require.NoError(t, x.Put(ctx, "t2", k4, Entry{SourceRequestID: "r4", Response: resp("res4"), Tags: []string{"auth"}}, time.Hour))

	// Step 1: Purge by tag "auth" for tenant "t1"
	res, err := Purge(ctx, rdb, PurgeOptions{TenantID: "t1", Tags: []string{"auth"}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.DeletedKeys, int64(2)) // k1, semKey, plus tag key

	// Verify k1 and semKey are gone
	e1, err := x.Get(ctx, k1)
	require.NoError(t, err)
	require.Nil(t, e1)

	exists, err := rdb.Exists(ctx, semKey).Result()
	require.NoError(t, err)
	require.EqualValues(t, 0, exists)

	// Verify tag key is gone
	members, err := rdb.SMembers(ctx, TagKey("t1", "auth")).Result()
	require.NoError(t, err)
	require.Empty(t, members)

	// Verify k2, k3 (tenant t1) and k4 (tenant t2) are still present
	e2, err := x.Get(ctx, k2)
	require.NoError(t, err)
	require.NotNil(t, e2)

	e3, err := x.Get(ctx, k3)
	require.NoError(t, err)
	require.NotNil(t, e3)

	e4, err := x.Get(ctx, k4)
	require.NoError(t, err)
	require.NotNil(t, e4)

	// Step 2: Purge tenant "t1"
	res2, err := Purge(ctx, rdb, PurgeOptions{TenantID: "t1"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res2.DeletedKeys, int64(2))

	e2After, err := x.Get(ctx, k2)
	require.NoError(t, err)
	require.Nil(t, e2After)

	e3After, err := x.Get(ctx, k3)
	require.NoError(t, err)
	require.Nil(t, e3After)

	// Tenant t2 remains intact
	e4After, err := x.Get(ctx, k4)
	require.NoError(t, err)
	require.NotNil(t, e4After)

	// Step 3: Purge all
	res3, err := Purge(ctx, rdb, PurgeOptions{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, res3.DeletedKeys, int64(1))

	e4Final, err := x.Get(ctx, k4)
	require.NoError(t, err)
	require.Nil(t, e4Final)
}

// Package cache provides enterprise-grade capabilities, configuration, and structural components for the cache subsystem.
package cache

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"
)

// PurgeOptions specifies what cache entries to invalidate.
// If Tags are specified, entries indexed under those tags for TenantID (or across all tenants if TenantID is empty) are removed.
// If Tags are not specified, keys matching TenantID and Route (or all cache keys for TenantID) are scanned and unlinked.
type PurgeOptions struct {
	TenantID string   `json:"tenant_id,omitempty"`
	Route    string   `json:"route,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// PurgeResult returns the count of unlinked keys.
type PurgeResult struct {
	DeletedKeys int64 `json:"deleted_keys"`
}

// Purge deletes cache keys matching the given options using SMEMBERS/SCAN and UNLINK.
// If tagging is leveraged, it limits the scan space to the specific Sets representing those tags.
// Otherwise, it performs a batched wildcard SCAN over the tenant's hash keyspace.
func Purge(ctx context.Context, rdb *redis.Client, opts PurgeOptions) (*PurgeResult, error) {
	if rdb == nil {
		return &PurgeResult{}, nil
	}

	if len(opts.Tags) > 0 {
		return purgeByTags(ctx, rdb, opts)
	}
	return purgeByScan(ctx, rdb, opts)
}

// purgeByTags executes a targeted cache eviction by resolving the Redis Sets
// mapping a given tag back to the underlying exact and semantic cache keys.
func purgeByTags(ctx context.Context, rdb *redis.Client, opts PurgeOptions) (*PurgeResult, error) {
	toUnlink := make(map[string]struct{})
	for _, tag := range opts.Tags {
		var tagKeys []string
		if opts.TenantID != "" {
			tagKeys = append(tagKeys, TagKey(opts.TenantID, tag))
		} else {
			iter := rdb.Scan(ctx, 0, "cachetag:*:"+tag, 200).Iterator()
			for iter.Next(ctx) {
				tagKeys = append(tagKeys, iter.Val())
			}
			if err := iter.Err(); err != nil {
				return nil, err
			}
		}

		for _, tk := range tagKeys {
			members, err := rdb.SMembers(ctx, tk).Result()
			if err != nil {
				return nil, err
			}
			for _, m := range members {
				if opts.Route != "" && !strings.Contains(m, ":"+opts.Route+":") {
					continue
				}
				toUnlink[m] = struct{}{}
			}
			toUnlink[tk] = struct{}{}
		}
	}

	var deleted int64
	if len(toUnlink) > 0 {
		keys := make([]string, 0, len(toUnlink))
		for k := range toUnlink {
			keys = append(keys, k)
		}
		for i := 0; i < len(keys); i += 500 {
			end := i + 500
			if end > len(keys) {
				end = len(keys)
			}
			n, err := rdb.Unlink(ctx, keys[i:end]...).Result()
			if err != nil {
				return nil, err
			}
			deleted += n
		}
	}
	return &PurgeResult{DeletedKeys: deleted}, nil
}

// purgeByScan executes a brute-force pattern scan (SCAN) over the entire database
// or a specific tenant's keyspace. This is an expensive operation in Redis and should
// be used carefully.
func purgeByScan(ctx context.Context, rdb *redis.Client, opts PurgeOptions) (*PurgeResult, error) {
	var patterns []string
	if opts.TenantID != "" {
		tt := tenantTag(opts.TenantID)
		if opts.Route != "" {
			patterns = []string{
				"cache:" + tt + ":x:" + opts.Route + ":*",
				"sc:*:" + tt + ":" + opts.Route + ":*",
			}
		} else {
			patterns = []string{
				"cache:" + tt + ":*",
				"cachetag:" + tt + ":*",
				"sc:*:" + tt + ":*",
			}
		}
	} else {
		if opts.Route != "" {
			patterns = []string{
				"cache:*:x:" + opts.Route + ":*",
				"sc:*:*:" + opts.Route + ":*",
			}
		} else {
			patterns = []string{
				"cache:*",
				"cachetag:*",
				"sc:*",
			}
		}
	}

	var totalDeleted int64
	for _, pattern := range patterns {
		var cursor uint64
		for {
			keys, nextCursor, err := rdb.Scan(ctx, cursor, pattern, 500).Result()
			if err != nil {
				return nil, err
			}
			if len(keys) > 0 {
				n, err := rdb.Unlink(ctx, keys...).Result()
				if err != nil {
					return nil, err
				}
				totalDeleted += n
			}
			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
	}

	return &PurgeResult{DeletedKeys: totalDeleted}, nil
}

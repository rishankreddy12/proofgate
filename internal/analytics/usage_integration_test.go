//go:build integration

package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInsertUsage(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, chConn(t))
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, conn))
	ins := InsertUsage(conn)
	require.NoError(t, ins(ctx, []UsageEvent{
		{TS: time.Now(), RequestID: "r1", TenantID: "t1", Route: "default", Kind: "chat", Status: "ok", Cache: "miss", CostMicros: 10},
		{TS: time.Now(), RequestID: "r2", TenantID: "t1", Route: "default", Kind: "chat", Status: "ok", Cache: "hit-exact", SavedMicros: 10},
	}))
	var cost, saved int64
	require.NoError(t, conn.QueryRow(ctx, "SELECT sum(cost_micros), sum(saved_micros) FROM usage_events WHERE tenant_id='t1'").Scan(&cost, &saved))
	require.EqualValues(t, 10, cost)
	require.EqualValues(t, 10, saved)
}

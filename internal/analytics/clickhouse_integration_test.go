//go:build integration

package analytics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	tcch "github.com/testcontainers/testcontainers-go/modules/clickhouse"
)

func chConn(t *testing.T) string {
	ctx := context.Background()
	c, err := tcch.Run(ctx, "clickhouse/clickhouse-server:24.8-alpine",
		tcch.WithUsername("pg"), tcch.WithPassword("pg"), tcch.WithDatabase("proofgate"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	return dsn
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, chConn(t))
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, conn))
	require.NoError(t, Migrate(ctx, conn))
	var n uint64
	require.NoError(t, conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE name = 'usage_events'").Scan(&n))
	require.EqualValues(t, 1, n)
}

package analytics

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/proofgate/proofgate/internal/mcpproxy"
)

func InsertMCP(conn driver.Conn) func(context.Context, []mcpproxy.Audit) error {
	return func(ctx context.Context, rows []mcpproxy.Audit) error {
		b, err := conn.PrepareBatch(ctx, "INSERT INTO mcp_calls")
		if err != nil {
			return err
		}
		for _, a := range rows {
			if err := b.Append(a.TS, a.TenantID, a.KeyID, a.Server, a.Method, a.Tool, a.RunID, a.Decision, uint16(a.Status), a.LatencyMs); err != nil {
				return err
			}
		}
		return b.Send()
	}
}

package analytics

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, dsn string) (driver.Conn, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	return conn, conn.Ping(ctx)
}

func Migrate(ctx context.Context, conn driver.Conn) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := migrations.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(string(b), "\n;;\n") {
			stmt = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(stmt), ";"))
			if stmt == "" {
				continue
			}
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("clickhouse migration %s: %w", n, err)
			}
		}
	}
	return nil
}

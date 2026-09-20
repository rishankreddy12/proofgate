# Dependencies

Every third-party module must be listed here with a reason. CI does not check this file, reviewers do.

| Module | Why |
|---|---|
| github.com/go-chi/chi/v5 | Small, net/http-compatible router |
| github.com/jackc/pgx/v5 | Postgres driver with pooling |
| github.com/redis/go-redis/v9 | Redis client with Lua scripting and cluster support |
| gopkg.in/yaml.v3 | Config file parsing (decodes time.Duration) |
| go.opentelemetry.io/otel/* | Tracing with GenAI semantic conventions |
| github.com/prometheus/client_golang | /metrics endpoint |
| github.com/google/uuid | Request and entity ids |
| github.com/stretchr/testify | Test assertions only |
| github.com/testcontainers/testcontainers-go | Integration tests only |
| github.com/ClickHouse/clickhouse-go/v2 | native-protocol batch inserts |

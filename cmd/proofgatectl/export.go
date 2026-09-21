package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/config"
	"gopkg.in/yaml.v3"
)

func redactConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	if cfg.Providers != nil {
		cp.Providers = make([]config.ProviderConfig, len(cfg.Providers))
		for i, p := range cfg.Providers {
			cp.Providers[i] = p
			if p.Headers != nil {
				cp.Providers[i].Headers = make(map[string]string, len(p.Headers))
				for k, v := range p.Headers {
					cp.Providers[i].Headers[k] = v
				}
			}
			if p.APIKeyEnv != "" {
				cp.Providers[i].APIKeyEnv = "<redacted>"
			}
		}
	}
	if cfg.MCPServers != nil {
		cp.MCPServers = make([]config.MCPServerConfig, len(cfg.MCPServers))
		for i, m := range cfg.MCPServers {
			cp.MCPServers[i] = m
			if m.Headers != nil {
				cp.MCPServers[i].Headers = make(map[string]string, len(m.Headers))
				for k := range m.Headers {
					cp.MCPServers[i].Headers[k] = "<redacted>"
				}
			}
		}
	}
	if cfg.Secrets.LocalKEKFile != "" {
		cp.Secrets.LocalKEKFile = "<redacted>"
	}
	return &cp
}

type ExportUsageRow struct {
	TS               time.Time `json:"ts"`
	RequestID        string    `json:"request_id"`
	TenantID         string    `json:"tenant_id"`
	KeyID            string    `json:"key_id"`
	Route            string    `json:"route"`
	Target           string    `json:"target"`
	Kind             string    `json:"kind"`
	Stream           bool      `json:"stream"`
	Status           string    `json:"status"`
	Cache            string    `json:"cache"`
	Attempts         uint8     `json:"attempts"`
	PromptTokens     uint32    `json:"prompt_tokens"`
	CompletionTokens uint32    `json:"completion_tokens"`
	CachedTokens     uint32    `json:"cached_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	SavedUSD         float64   `json:"saved_usd"`
	LatencyMs        uint32    `json:"latency_ms"`
	TTFTMs           uint32    `json:"ttft_ms"`
	OverheadUs       uint32    `json:"overhead_us"`
	UsageEstimated   bool      `json:"usage_estimated"`
	RunID            string    `json:"run_id"`
}

func runExport(ctx context.Context, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl export <usage|config> [flags]")
	}
	switch args[0] {
	case "usage":
		fs := flag.NewFlagSet("export usage", flag.ExitOnError)
		sinceStr := fs.String("since", "2026-01-01", "export events starting from date (YYYY-MM-DD or RFC3339)")
		outFile := fs.String("out", "usage.jsonl", "output file path")
		chURL := fs.String("ch", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN")
		_ = fs.Parse(args[1:])

		if *chURL == "" {
			*chURL = "clickhouse://proofgate:proofgate@127.0.0.1:19000/proofgate"
		}
		since, err := time.Parse("2006-01-02", *sinceStr)
		if err != nil {
			since, err = time.Parse(time.RFC3339, *sinceStr)
			if err != nil {
				die("parse since date %q: %v", *sinceStr, err)
			}
		}

		conn, err := analytics.Open(ctx, *chURL)
		if err != nil {
			die("connect to clickhouse: %v", err)
		}
		defer conn.Close()

		query := `
			SELECT ts, request_id, tenant_id, key_id, route, target, kind, stream, status, cache,
			       attempts, prompt_tokens, completion_tokens, cached_tokens, cost_micros, saved_micros,
			       latency_ms, ttft_ms, overhead_us, usage_estimated, run_id
			FROM usage_events
			WHERE ts >= ?
			ORDER BY ts ASC`

		rows, err := conn.Query(ctx, query, since)
		if err != nil {
			die("query usage_events: %v", err)
		}
		defer rows.Close()

		var out io.Writer
		if *outFile == "-" || *outFile == "" {
			out = os.Stdout
		} else {
			f, err := os.Create(*outFile)
			if err != nil {
				die("create output file %s: %v", *outFile, err)
			}
			defer f.Close()
			out = f
		}

		enc := json.NewEncoder(out)
		count := 0
		for rows.Next() {
			var r ExportUsageRow
			var costMicros, savedMicros int64
			var streamU8, estimatedU8 uint8
			if err := rows.Scan(
				&r.TS, &r.RequestID, &r.TenantID, &r.KeyID, &r.Route, &r.Target, &r.Kind,
				&streamU8, &r.Status, &r.Cache, &r.Attempts, &r.PromptTokens, &r.CompletionTokens,
				&r.CachedTokens, &costMicros, &savedMicros, &r.LatencyMs, &r.TTFTMs, &r.OverheadUs,
				&estimatedU8, &r.RunID,
			); err != nil {
				die("scan usage row: %v", err)
			}
			r.Stream = (streamU8 != 0)
			r.UsageEstimated = (estimatedU8 != 0)
			r.CostUSD = float64(costMicros) / 1e6
			r.SavedUSD = float64(savedMicros) / 1e6
			if err := enc.Encode(r); err != nil {
				die("encode json row: %v", err)
			}
			count++
		}
		fmt.Fprintf(os.Stderr, "Exported %d usage event(s) to %s\n", count, *outFile)

	case "config":
		fs := flag.NewFlagSet("export config", flag.ExitOnError)
		cfgFile := fs.String("config", "deploy/proofgate.yaml", "path to proofgate configuration file")
		outFile := fs.String("out", "config-snapshot.yaml", "output file path")
		_ = fs.Parse(args[1:])

		cfg, err := config.Load(*cfgFile)
		if err != nil {
			die("load config %s: %v", *cfgFile, err)
		}
		redacted := redactConfig(cfg)
		b, err := yaml.Marshal(redacted)
		if err != nil {
			die("marshal yaml: %v", err)
		}
		if *outFile == "-" || *outFile == "" {
			fmt.Print(string(b))
		} else {
			if err := os.WriteFile(*outFile, b, 0o644); err != nil {
				die("write %s: %v", *outFile, err)
			}
			fmt.Fprintf(os.Stderr, "Exported redacted config snapshot to %s\n", *outFile)
		}

	default:
		die("unknown export command: %s (supported: usage, config)", args[0])
	}
}

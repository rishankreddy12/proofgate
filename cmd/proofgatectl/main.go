// Command proofgatectl manages tenants and API keys directly in Postgres.
//
//	proofgatectl tenant create --name acme --rpm 600 --tpm 200000 --budget-usd 50
//	proofgatectl tenant set-policy --name acme --rpm 60
//	proofgatectl key create --tenant acme --name ci --routes default,embed [--allow-direct] [--env live]
//	proofgatectl key list --tenant acme
//	proofgatectl key revoke --id <uuid>
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/proofgate/proofgate/internal/store"
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 3 {
		die("usage: proofgatectl <tenant|key|label|provider-key|kek|export> <command> [flags]")
	}
	if os.Args[1] == "kek" && os.Args[2] == "generate" {
		k, err := secrets.GenerateLocalKEK()
		if err != nil {
			die("generate kek: %v", err)
		}
		fmt.Println(k)
		return
	}
	if os.Args[1] == "export" {
		runExport(context.Background(), os.Args[2:])
		return
	}
	if os.Args[1] == "label" && os.Args[2] == "cache" {
		fs := flag.NewFlagSet("label cache", flag.ExitOnError)
		route := fs.String("route", "", "route name (empty = all)")
		limit := fs.Int("limit", 20, "max records to label")
		chURL := fs.String("ch", "", "clickhouse connection URL")
		_ = fs.Parse(os.Args[3:])
		runLabelCache(context.Background(), *route, *limit, *chURL)
		return
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		die("DATABASE_URL is required")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		die("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		die("migrate: %v", err)
	}
	if os.Args[1] == "provider-key" {
		providerKey(ctx, st, os.Args[2:])
		return
	}
	fs := flag.NewFlagSet(os.Args[1]+" "+os.Args[2], flag.ExitOnError)
	name := fs.String("name", "", "tenant or key name")
	tenant := fs.String("tenant", "", "tenant name")
	rpm := fs.Int("rpm", 0, "requests per minute (0 = unlimited)")
	tpm := fs.Int("tpm", 0, "tokens per minute (0 = unlimited)")
	budget := fs.Float64("budget-usd", 0, "monthly budget in USD (0 = unlimited)")
	strict := fs.Bool("strict", false, "fail closed if Redis is down")
	routes := fs.String("routes", "", "comma-separated allowed routes (empty = all)")
	allowDirect := fs.Bool("allow-direct", false, "allow provider/model targets")
	env := fs.String("env", "live", "key environment label")
	id := fs.String("id", "", "key id")
	runCost := fs.Float64("run-max-cost-usd", 0, "max spend per agent run in USD")
	runSteps := fs.Int("run-max-steps", 0, "max steps per agent run")
	runTokens := fs.Int("run-max-tokens", 0, "max tokens per agent run")
	requireRunID := fs.Bool("require-run-id", false, "require X-ProofGate-Run-Id header")
	mcpPolicyPath := fs.String("mcp-policy", "", "path to JSON file with MCP policy")
	_ = fs.Parse(os.Args[3:])
	policy := store.TenantPolicy{RPM: *rpm, TPM: *tpm, MonthlyBudgetUSD: *budget, Strict: *strict}

	switch os.Args[1] + " " + os.Args[2] {
	case "tenant create":
		t, err := st.CreateTenant(ctx, *name, policy)
		if err != nil {
			die("create tenant: %v", err)
		}
		fmt.Printf("tenant %s id=%s\n", t.Name, t.ID)
	case "tenant set-policy":
		t, err := st.TenantByName(ctx, *name)
		if err != nil {
			die("tenant: %v", err)
		}
		if err := st.UpdateTenantPolicy(ctx, t.ID, policy); err != nil {
			die("update: %v", err)
		}
		fmt.Println("updated")
	case "key create":
		t, err := st.TenantByName(ctx, *tenant)
		if err != nil {
			die("tenant: %v", err)
		}
		plain, prefix, hash, err := auth.GenerateKey(*env)
		if err != nil {
			die("generate: %v", err)
		}
		var rs []string
		if *routes != "" {
			rs = strings.Split(*routes, ",")
		}
		kp := store.KeyPolicy{AllowDirect: *allowDirect}
		if *runCost > 0 || *runSteps > 0 || *runTokens > 0 || *requireRunID {
			kp.Run = &store.RunPolicy{
				MaxCostUSD:   *runCost,
				MaxSteps:     *runSteps,
				MaxTokens:    *runTokens,
				RequireRunID: *requireRunID,
			}
		}
		if *mcpPolicyPath != "" {
			b, err := os.ReadFile(*mcpPolicyPath)
			if err != nil {
				die("read mcp policy: %v", err)
			}
			var mp store.MCPPolicy
			if err := json.Unmarshal(b, &mp); err != nil {
				die("parse mcp policy: %v", err)
			}
			kp.MCP = &mp
		}
		k, err := st.CreateKey(ctx, t.ID, *name, prefix, hash, rs, kp)
		if err != nil {
			die("create key: %v", err)
		}
		fmt.Fprintf(os.Stderr, "key id=%s (shown once, store it now)\n", k.ID)
		fmt.Println(plain)
	case "key list":
		t, err := st.TenantByName(ctx, *tenant)
		if err != nil {
			die("tenant: %v", err)
		}
		keys, err := st.ListKeys(ctx, t.ID)
		if err != nil {
			die("list: %v", err)
		}
		for _, k := range keys {
			status := "active"
			if k.RevokedAt != nil {
				status = "revoked"
			}
			fmt.Printf("%s\t%s\t%s…\t%s\t%v\n", k.ID, k.Name, k.Prefix, status, k.AllowedRoutes)
		}
	case "key revoke":
		if err := st.RevokeKey(ctx, *id); err != nil {
			die("revoke: %v", err)
		}
		fmt.Println("revoked (cached copies expire within 30s)")
	default:
		die("unknown command %q", os.Args[1]+" "+os.Args[2])
	}
}

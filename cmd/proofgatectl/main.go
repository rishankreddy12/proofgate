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

	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/proofgate/proofgate/internal/store"
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func main() {
	gf, cmdArgs := parseGlobalFlags(os.Args[1:])
	if len(cmdArgs) == 0 {
		die("usage: proofgatectl [--profile X] [--server URL] [--output json] [--insecure] <command> [flags]")
	}

	ctx := context.Background()

	// 1. Standalone/remote commands that do not need direct DB connection
	switch cmdArgs[0] {
	case "kek":
		if len(cmdArgs) > 1 && cmdArgs[1] == "generate" {
			k, err := secrets.GenerateLocalKEK()
			if err != nil {
				die("generate kek: %v", err)
			}
			fmt.Println(k)
			return
		}
	case "profile":
		runProfile(gf, cmdArgs[1:])
		return
	case "login":
		runLogin(ctx, gf, cmdArgs[1:])
		return
	case "logout":
		runLogout(ctx, gf)
		return
	case "whoami":
		runWhoami(ctx, gf)
		return
	case "status":
		runStatus(ctx, gf)
		return
	case "health":
		runHealth(ctx, gf)
		return
	case "cache":
		if len(cmdArgs) > 1 && cmdArgs[1] == "purge" {
			runCachePurge(ctx, gf, cmdArgs[2:])
			return
		}
	case "user":
		runUser(ctx, gf, cmdArgs[1:])
		return
	case "provider":
		runProvider(ctx, gf, cmdArgs[1:])
		return
	case "config":
		runConfig(ctx, gf, cmdArgs[1:])
		return
	case "session":
		runSession(ctx, gf, cmdArgs[1:])
		return
	case "chat":
		runChat(ctx, gf, cmdArgs[1:])
		return
	case "export":
		if len(cmdArgs) > 1 && cmdArgs[1] == "config" {
			runExport(ctx, cmdArgs[1:])
			return
		}
	}

	// 2. Direct database commands (bootstrap-admin, tenant, key, provider-key, export usage, label cache)
	if cmdArgs[0] == "export" {
		runExport(ctx, cmdArgs[1:])
		return
	}
	if cmdArgs[0] == "label" && len(cmdArgs) > 1 && cmdArgs[1] == "cache" {
		fs := flag.NewFlagSet("label cache", flag.ExitOnError)
		route := fs.String("route", "", "route name (empty = all)")
		limit := fs.Int("limit", 20, "max records to label")
		chURL := fs.String("ch", "", "clickhouse connection URL")
		_ = fs.Parse(cmdArgs[2:])
		runLabelCache(ctx, *route, *limit, *chURL)
		return
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		cfgPath := os.Getenv("PROOFGATE_CONFIG")
		if cfgPath == "" {
			if _, err := os.Stat("proofgate.yaml"); err == nil {
				cfgPath = "proofgate.yaml"
			} else if _, err := os.Stat("deploy/proofgate.yaml"); err == nil {
				cfgPath = "deploy/proofgate.yaml"
			}
		}
		if cfgPath != "" {
			if cfg, err := config.Load(cfgPath); err == nil && cfg.Database.URL != "" {
				dsn = cfg.Database.URL
			}
		}
	}
	if dsn == "" {
		die("DATABASE_URL is required (or database.url configured in proofgate.yaml)")
	}

	st, err := store.Open(ctx, dsn)
	if err != nil {
		die("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		die("migrate: %v", err)
	}

	if cmdArgs[0] == "bootstrap-admin" {
		fs := flag.NewFlagSet("bootstrap-admin", flag.ExitOnError)
		username := fs.String("username", "", "admin username")
		_ = fs.Parse(cmdArgs[1:])
		if *username == "" {
			die("--username is required")
		}
		if !adminauth.ValidateUsername(*username) {
			die("invalid username: must be 1-64 characters matching [A-Za-z0-9._@-]")
		}

		count, err := st.AdminUserCount(ctx)
		if err != nil {
			die("check admin users: %v", err)
		}
		if count > 0 {
			die("admin users already exist; use the control-plane API to manage users")
		}

		pw1, err := readPassword("Enter password for initial admin user: ")
		if err != nil || pw1 == "" {
			die("password is required")
		}
		if err := adminauth.ValidatePassword(pw1, *username); err != nil {
			die("invalid password: %v", err)
		}
		pw2, err := readPassword("Confirm password: ")
		if err != nil || pw2 != pw1 {
			die("passwords do not match")
		}

		hash, err := adminauth.HashPassword(pw1)
		if err != nil {
			die("hash password: %v", err)
		}

		u, err := st.CreateAdminUser(ctx, *username, hash, "admin")
		if err != nil {
			die("create admin user: %v", err)
		}
		_ = st.RecordAdminAudit(ctx, *username, "user.bootstrap", *username, nil, "127.0.0.1", "ok")
		fmt.Fprintf(os.Stderr, "Admin user created: %s (id=%s)\n", u.Username, u.ID)
		return
	}

	if cmdArgs[0] == "provider-key" {
		providerKey(ctx, st, cmdArgs[1:])
		return
	}

	if len(cmdArgs) < 2 {
		die("usage: proofgatectl <tenant|key|label|provider-key|kek|export|bootstrap-admin> <command> [flags]")
	}

	fs := flag.NewFlagSet(cmdArgs[0]+" "+cmdArgs[1], flag.ExitOnError)
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
	_ = fs.Parse(cmdArgs[2:])
	policy := store.TenantPolicy{RPM: *rpm, TPM: *tpm, MonthlyBudgetUSD: *budget, Strict: *strict}

	switch cmdArgs[0] + " " + cmdArgs[1] {
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
		die("unknown command %q", cmdArgs[0]+" "+cmdArgs[1])
	}
}

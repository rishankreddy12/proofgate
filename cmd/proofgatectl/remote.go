package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"golang.org/x/term"
)

type globalFlags struct {
	profile  string
	server   string
	output   string
	insecure bool
}

func parseGlobalFlags(args []string) (globalFlags, []string) {
	var gf globalFlags
	var remaining []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--profile" && i+1 < len(args):
			gf.profile = args[i+1]
			i++
		case strings.HasPrefix(arg, "--profile="):
			gf.profile = strings.TrimPrefix(arg, "--profile=")
		case arg == "--server" && i+1 < len(args):
			gf.server = args[i+1]
			i++
		case strings.HasPrefix(arg, "--server="):
			gf.server = strings.TrimPrefix(arg, "--server=")
		case arg == "--output" && i+1 < len(args):
			gf.output = args[i+1]
			i++
		case strings.HasPrefix(arg, "--output="):
			gf.output = strings.TrimPrefix(arg, "--output=")
		case arg == "--insecure":
			gf.insecure = true
		default:
			remaining = append(remaining, arg)
		}
	}
	return gf, remaining
}

var stdinReader = bufio.NewReader(os.Stdin)

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func readString(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func printOutput(client *Client, data any) {
	if client.OutputJSON {
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			die("format json: %v", err)
		}
		fmt.Println(string(b))
		return
	}
}

func runLogin(ctx context.Context, gf globalFlags, args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	username := fs.String("username", "", "username")
	_ = fs.Parse(args)

	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	u := *username
	if u == "" {
		u, err = readString("Enter username: ")
		if err != nil || u == "" {
			die("username is required")
		}
	}

	pw, err := readPassword("Enter password: ")
	if err != nil || pw == "" {
		die("password is required")
	}

	var resp struct {
		Token     string    `json:"token"`
		UserID    string    `json:"user_id"`
		Username  string    `json:"username"`
		Role      string    `json:"role"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	_, err = client.Do(ctx, "POST", "/admin/auth/login", map[string]string{
		"username": u,
		"password": pw,
	}, &resp)
	if err != nil {
		die("%v", err)
	}

	pName, _, _ := getActiveProfile(gf.profile)
	if err := saveSession(pName, &SessionFile{
		Token:     resp.Token,
		Username:  resp.Username,
		ExpiresAt: resp.ExpiresAt,
	}); err != nil {
		die("save session: %v", err)
	}

	if client.OutputJSON {
		printOutput(client, map[string]any{
			"username":   resp.Username,
			"role":       resp.Role,
			"expires_at": resp.ExpiresAt,
		})
		return
	}
	fmt.Printf("Logged in successfully as %s (role: %s).\n", resp.Username, resp.Role)
}

func runLogout(ctx context.Context, gf globalFlags) {
	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	_, _ = client.Do(ctx, "POST", "/admin/auth/logout", nil, nil)
	pName, _, _ := getActiveProfile(gf.profile)
	_ = deleteSession(pName)

	if client.OutputJSON {
		fmt.Println(`{"status": "ok"}`)
		return
	}
	fmt.Println("Logged out successfully.")
}

type WhoamiResponse struct {
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

func runWhoami(ctx context.Context, gf globalFlags) {
	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	var resp WhoamiResponse
	_, err = client.Do(ctx, "GET", "/admin/auth/whoami", nil, &resp)
	if err != nil {
		die("%v", err)
	}

	if client.OutputJSON {
		printOutput(client, resp)
		return
	}

	fmt.Printf("User:         %s\n", resp.Username)
	fmt.Printf("Role:         %s\n", resp.Role)
	fmt.Printf("User ID:      %s\n", resp.UserID)
	fmt.Printf("Last Active:  %s\n", resp.LastActiveAt.Format(time.RFC3339))
}

func runStatus(ctx context.Context, gf globalFlags) {
	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	var resp map[string]any
	_, err = client.Do(ctx, "GET", "/admin/cp/status", nil, &resp)
	if err != nil {
		die("%v", err)
	}

	if client.OutputJSON {
		printOutput(client, resp)
		return
	}

	fmt.Printf("ProofGate Version:    %v\n", resp["version"])
	fmt.Printf("Uptime:               %v seconds\n", resp["uptime_seconds"])
	fmt.Printf("Server Address:       %v\n", resp["server_addr"])
	fmt.Printf("Admin Address:        %v\n", resp["admin_addr"])
	fmt.Printf("Configured Providers: %v\n", resp["providers"])
	fmt.Printf("Configured Routes:    %v\n", resp["routes"])
	fmt.Printf("Admin Auth Enabled:   %v\n", resp["admin_auth_enabled"])
}

func runHealth(ctx context.Context, gf globalFlags) {
	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	var resp any
	_, err = client.Do(ctx, "GET", "/admin/cp/health", nil, &resp)
	if err != nil {
		die("%v", err)
	}

	printOutput(client, resp)
	if !client.OutputJSON {
		b, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(b))
	}
}

func runCachePurge(ctx context.Context, gf globalFlags, args []string) {
	fs := flag.NewFlagSet("cache purge", flag.ExitOnError)
	route := fs.String("route", "", "route name")
	tenant := fs.String("tenant", "", "tenant ID")
	_ = fs.Parse(args)

	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	body := map[string]string{}
	if *route != "" {
		body["route"] = *route
	}
	if *tenant != "" {
		body["tenant"] = *tenant
	}

	var resp any
	_, err = client.Do(ctx, "POST", "/admin/cp/cache/purge", body, &resp)
	if err != nil {
		die("%v", err)
	}

	if client.OutputJSON {
		printOutput(client, resp)
		return
	}
	fmt.Println("Cache purge executed.")
}

func runUser(ctx context.Context, gf globalFlags, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl user <create|list|get|disable|enable|delete|change-password> [flags]")
	}

	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("user create", flag.ExitOnError)
		username := fs.String("username", "", "username")
		role := fs.String("role", "admin", "role (admin, operator, viewer)")
		_ = fs.Parse(args[1:])

		if *username == "" {
			die("--username is required")
		}

		pw1, err := readPassword("Enter password for new user: ")
		if err != nil || pw1 == "" {
			die("password is required")
		}
		pw2, err := readPassword("Confirm password: ")
		if err != nil || pw2 != pw1 {
			die("passwords do not match")
		}

		var resp map[string]any
		status, err := client.Do(ctx, "POST", "/admin/cp/users", map[string]string{
			"username": *username,
			"password": pw1,
			"role":     *role,
		}, &resp)
		if err != nil {
			die("%v", err)
		}
		if status == http.StatusCreated {
			if client.OutputJSON {
				printOutput(client, resp)
				return
			}
			fmt.Printf("User %q created with role %q.\n", *username, *role)
		}

	case "list":
		var users []map[string]any
		_, err := client.Do(ctx, "GET", "/admin/cp/users", nil, &users)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, users)
			return
		}

		fmt.Printf("%-20s %-12s %-8s %-24s\n", "USERNAME", "ROLE", "ENABLED", "CREATED")
		for _, u := range users {
			fmt.Printf("%-20v %-12v %-8v %-24v\n", u["username"], u["role"], u["enabled"], u["created_at"])
		}

	case "get":
		fs := flag.NewFlagSet("user get", flag.ExitOnError)
		username := fs.String("username", "", "username")
		_ = fs.Parse(args[1:])
		if *username == "" {
			die("--username is required")
		}

		var user map[string]any
		_, err := client.Do(ctx, "GET", "/admin/cp/users/"+*username, nil, &user)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, user)
			return
		}
		fmt.Printf("Username: %v\nRole:     %v\nEnabled:  %v\nCreated:  %v\n", user["username"], user["role"], user["enabled"], user["created_at"])

	case "disable":
		fs := flag.NewFlagSet("user disable", flag.ExitOnError)
		username := fs.String("username", "", "username")
		_ = fs.Parse(args[1:])
		if *username == "" {
			die("--username is required")
		}

		_, err := client.Do(ctx, "PUT", "/admin/cp/users/"+*username+"/enabled", map[string]bool{"enabled": false}, nil)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("User %q disabled.\n", *username)

	case "enable":
		fs := flag.NewFlagSet("user enable", flag.ExitOnError)
		username := fs.String("username", "", "username")
		_ = fs.Parse(args[1:])
		if *username == "" {
			die("--username is required")
		}

		_, err := client.Do(ctx, "PUT", "/admin/cp/users/"+*username+"/enabled", map[string]bool{"enabled": true}, nil)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("User %q enabled.\n", *username)

	case "delete":
		fs := flag.NewFlagSet("user delete", flag.ExitOnError)
		username := fs.String("username", "", "username")
		_ = fs.Parse(args[1:])
		if *username == "" {
			die("--username is required")
		}

		_, err := client.Do(ctx, "DELETE", "/admin/cp/users/"+*username, nil, nil)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("User %q deleted.\n", *username)

	case "change-password":
		fs := flag.NewFlagSet("user change-password", flag.ExitOnError)
		username := fs.String("username", "", "username (leave blank for self)")
		_ = fs.Parse(args[1:])

		targetUser := *username
		if targetUser == "" {
			// Find self
			var who WhoamiResponse
			_, err := client.Do(ctx, "GET", "/admin/auth/whoami", nil, &who)
			if err != nil {
				die("identify self: %v", err)
			}
			targetUser = who.Username
		}

		oldPw, _ := readPassword("Enter current password: ")
		newPw1, err := readPassword("Enter new password: ")
		if err != nil || newPw1 == "" {
			die("new password is required")
		}
		newPw2, err := readPassword("Confirm new password: ")
		if err != nil || newPw2 != newPw1 {
			die("passwords do not match")
		}

		_, err = client.Do(ctx, "POST", "/admin/cp/users/"+targetUser+"/password", map[string]string{
			"old_password": oldPw,
			"new_password": newPw1,
		}, nil)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("Password updated for user %q.\n", targetUser)

	default:
		die("unknown user subcommand %q", args[0])
	}
}

func runProvider(ctx context.Context, gf globalFlags, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl provider <list|get|test|credential> [flags]")
	}

	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	switch args[0] {
	case "list":
		var provs []map[string]any
		_, err := client.Do(ctx, "GET", "/admin/cp/providers", nil, &provs)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, provs)
			return
		}

		fmt.Printf("%-18s %-12s %-32s %-16s %-8s\n", "NAME", "TYPE", "BASE URL", "KEY SOURCE", "HAS KEY")
		for _, p := range provs {
			fmt.Printf("%-18v %-12v %-32v %-16v %-8v\n", p["name"], p["type"], p["base_url"], p["key_source"], p["has_key"])
		}

	case "get":
		fs := flag.NewFlagSet("provider get", flag.ExitOnError)
		name := fs.String("name", "", "provider name")
		_ = fs.Parse(args[1:])
		if *name == "" {
			die("--name is required")
		}

		var p map[string]any
		_, err := client.Do(ctx, "GET", "/admin/cp/providers/"+*name, nil, &p)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, p)
			return
		}
		fmt.Printf("Name:       %v\nType:       %v\nBase URL:   %v\nKey Source: %v\nHas Key:    %v\n",
			p["name"], p["type"], p["base_url"], p["key_source"], p["has_key"])

	case "test":
		fs := flag.NewFlagSet("provider test", flag.ExitOnError)
		name := fs.String("name", "", "provider name")
		_ = fs.Parse(args[1:])
		if *name == "" {
			die("--name is required")
		}

		var resp map[string]any
		_, err := client.Do(ctx, "POST", "/admin/cp/providers/"+*name+"/test", nil, &resp)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, resp)
			return
		}
		fmt.Printf("Provider: %v | Status: %v | Latency: %vms\n", resp["provider"], resp["status"], resp["latency_ms"])

	case "set-key":
		fs := flag.NewFlagSet("provider set-key", flag.ExitOnError)
		name := fs.String("name", "", "provider name")
		prov := fs.String("provider", "", "provider name (alias for --name)")
		key := fs.String("key", "", "API key")
		_ = fs.Parse(args[1:])
		targetProv := *name
		if targetProv == "" {
			targetProv = *prov
		}
		if targetProv == "" {
			die("--name or --provider is required")
		}

		apiKey := *key
		if apiKey == "" {
			var err error
			apiKey, err = readPassword("Paste the provider API key and press Enter: ")
			if err != nil || apiKey == "" {
				die("api key is required")
			}
		}

		var resp map[string]any
		_, err = client.Do(ctx, "POST", "/admin/cp/providers/"+targetProv+"/credential", map[string]string{
			"api_key": apiKey,
		}, &resp)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, resp)
			return
		}
		fmt.Printf("Provider %q credential stored (version: %v).\n", targetProv, resp["version"])

	case "credential":
		if len(args) < 2 {
			die("usage: proofgatectl provider credential <set|list> [flags]")
		}
		switch args[1] {
		case "set":
			fs := flag.NewFlagSet("provider credential set", flag.ExitOnError)
			provider := fs.String("provider", "", "provider name")
			_ = fs.Parse(args[2:])
			if *provider == "" {
				die("--provider is required")
			}

			key, err := readPassword("Paste the provider API key and press Enter: ")
			if err != nil || key == "" {
				die("api key is required")
			}

			var resp map[string]any
			_, err = client.Do(ctx, "POST", "/admin/cp/providers/"+*provider+"/credential", map[string]string{
				"api_key": key,
			}, &resp)
			if err != nil {
				die("%v", err)
			}

			if client.OutputJSON {
				printOutput(client, resp)
				return
			}
			fmt.Printf("Provider %q credential stored (version: %v).\n", *provider, resp["version"])

		case "list":
			var creds []map[string]any
			_, err := client.Do(ctx, "GET", "/admin/cp/providers/credentials", nil, &creds)
			if err != nil {
				die("%v", err)
			}

			if client.OutputJSON {
				printOutput(client, creds)
				return
			}

			fmt.Printf("%-20s %-8s %-8s %-20s %-24s\n", "PROVIDER", "VERSION", "ACTIVE", "CREATED BY", "CREATED AT")
			for _, c := range creds {
				fmt.Printf("%-20v %-8v %-8v %-20v %-24v\n", c["Provider"], c["Version"], c["Active"], c["CreatedBy"], c["CreatedAt"])
			}

		default:
			die("unknown credential subcommand %q", args[1])
		}

	default:
		die("unknown provider subcommand %q", args[0])
	}
}

func runConfig(ctx context.Context, gf globalFlags, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl config <show|validate|reload> [flags]")
	}

	switch args[0] {
	case "show":
		client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
		if err != nil {
			die("client: %v", err)
		}

		var cfg config.Config
		_, err = client.Do(ctx, "GET", "/admin/cp/config", nil, &cfg)
		if err != nil {
			die("%v", err)
		}

		printOutput(client, cfg)
		if !client.OutputJSON {
			b, _ := json.MarshalIndent(cfg, "", "  ")
			fmt.Println(string(b))
		}

	case "validate":
		fs := flag.NewFlagSet("config validate", flag.ExitOnError)
		file := fs.String("file", "", "path to config file")
		fs.StringVar(file, "f", "", "path to config file (shorthand)")
		_ = fs.Parse(args[1:])
		if *file == "" {
			die("-f / --file is required")
		}

		_, err := config.Load(*file)
		if err != nil {
			die("INVALID: %v", err)
		}
		fmt.Printf("Configuration in %s is valid.\n", *file)

	case "lint":
		fs := flag.NewFlagSet("config lint", flag.ExitOnError)
		file := fs.String("file", "", "path to config file")
		fs.StringVar(file, "f", "", "path to config file (shorthand)")
		strict := fs.Bool("strict", false, "fail on warnings as well as errors")
		_ = fs.Parse(args[1:])
		if *file == "" {
			die("-f / --file is required")
		}

		cfg, err := config.Load(*file)
		if err != nil {
			die("PARSE ERROR: %v", err)
		}
		issues := config.Lint(cfg, config.LintOpts{Strict: *strict})
		hasErrors := false
		for _, issue := range issues {
			if issue.Severity == config.SeverityError {
				hasErrors = true
				fmt.Fprintf(os.Stderr, "ERROR: [%s] %s\n", issue.Rule, issue.Message)
			} else {
				fmt.Fprintf(os.Stdout, "WARN:  [%s] %s\n", issue.Rule, issue.Message)
			}
		}
		if hasErrors || (*strict && len(issues) > 0) {
			os.Exit(1)
		}
		if len(issues) == 0 {
			fmt.Printf("Configuration in %s passed linting with zero issues.\n", *file)
		}

	case "reload":
		client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
		if err != nil {
			die("client: %v", err)
		}

		_, err = client.Do(ctx, "POST", "/admin/cp/config/reload", nil, nil)
		if err != nil {
			die("%v", err)
		}
		fmt.Println("Configuration reloaded successfully.")

	default:
		die("unknown config subcommand %q", args[0])
	}
}

func runSession(ctx context.Context, gf globalFlags, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl session <list|revoke|revoke-all> [flags]")
	}

	client, err := NewClient(gf.profile, gf.server, gf.insecure, gf.output == "json")
	if err != nil {
		die("client: %v", err)
	}

	switch args[0] {
	case "list":
		var sessions []map[string]any
		_, err := client.Do(ctx, "GET", "/admin/cp/sessions", nil, &sessions)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, sessions)
			return
		}

		fmt.Printf("%-16s %-16s %-12s %-16s %-24s\n", "SESSION ID", "USERNAME", "ROLE", "IP", "LAST ACTIVE")
		for _, s := range sessions {
			id := fmt.Sprintf("%v", s["id"])
			if len(id) > 12 {
				id = id[:12] + "..."
			}
			fmt.Printf("%-16s %-16v %-12v %-16v %-24v\n", id, s["username"], s["role"], s["ip"], s["last_active_at"])
		}

	case "revoke":
		fs := flag.NewFlagSet("session revoke", flag.ExitOnError)
		id := fs.String("id", "", "session ID")
		_ = fs.Parse(args[1:])
		if *id == "" {
			die("--id is required")
		}

		_, err := client.Do(ctx, "DELETE", "/admin/cp/sessions/"+*id, nil, nil)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("Session %q revoked.\n", *id)

	case "revoke-all":
		fs := flag.NewFlagSet("session revoke-all", flag.ExitOnError)
		user := fs.String("user", "", "username (empty = all)")
		_ = fs.Parse(args[1:])

		path := "/admin/cp/sessions"
		if *user != "" {
			path += "?user=" + *user
		}

		var resp map[string]any
		_, err := client.Do(ctx, "DELETE", path, nil, &resp)
		if err != nil {
			die("%v", err)
		}

		if client.OutputJSON {
			printOutput(client, resp)
			return
		}
		fmt.Printf("Revoked %v sessions.\n", resp["revoked_count"])

	default:
		die("unknown session subcommand %q", args[0])
	}
}

func runChat(ctx context.Context, gf globalFlags, args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	route := fs.String("route", "default", "route or model to use")
	system := fs.String("system", "", "optional system prompt")
	dataURL := fs.String("data-url", "", "data-plane URL (if connecting directly to port 8080)")
	key := fs.String("key", "", "data-plane API key (if connecting directly)")
	noStream := fs.Bool("no-stream", false, "disable streaming")
	_ = fs.Parse(args)

	promptArgs := fs.Args()

	client, err := NewClient(gf.profile, gf.server, gf.insecure, false)
	if err != nil {
		die("client: %v", err)
	}

	messages := []map[string]string{}
	if *system != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": *system,
		})
	}

	executeChat := func(prompt string) string {
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": prompt,
		})

		reqBody := map[string]any{
			"model":    *route,
			"messages": messages,
			"stream":   !*noStream,
		}

		var answer string
		if *noStream {
			var resp struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if *dataURL != "" || *key != "" {
				baseURL := *dataURL
				if baseURL == "" {
					baseURL = "http://localhost:8080"
				}
				url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
				b, _ := json.Marshal(reqBody)
				req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				if *key != "" {
					req.Header.Set("Authorization", "Bearer "+*key)
				}
				res, err := client.HTTPClient.Do(req)
				if err != nil {
					die("chat request failed: %v", err)
				}
				defer res.Body.Close()
				_ = json.NewDecoder(res.Body).Decode(&resp)
			} else {
				_, err := client.Do(ctx, "POST", "/admin/cp/chat", reqBody, &resp)
				if err != nil {
					die("chat error: %v", err)
				}
			}
			if len(resp.Choices) > 0 {
				answer = resp.Choices[0].Message.Content
				fmt.Println(answer)
			}
		} else {
			var res *http.Response
			if *dataURL != "" || *key != "" {
				baseURL := *dataURL
				if baseURL == "" {
					baseURL = "http://localhost:8080"
				}
				url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
				b, _ := json.Marshal(reqBody)
				req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "text/event-stream")
				if *key != "" {
					req.Header.Set("Authorization", "Bearer "+*key)
				}
				var err error
				res, err = client.HTTPClient.Do(req)
				if err != nil {
					die("stream request failed: %v", err)
				}
			} else {
				var err error
				res, err = client.Stream(ctx, "POST", "/admin/cp/chat", reqBody)
				if err != nil {
					die("stream error: %v", err)
				}
			}
			defer res.Body.Close()

			reader := bufio.NewReader(res.Body)
			var sb strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "[DONE]" {
					break
				}
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
					chunkText := chunk.Choices[0].Delta.Content
					sb.WriteString(chunkText)
					fmt.Print(chunkText)
				}
			}
			fmt.Println()
			answer = sb.String()
		}

		if answer != "" {
			messages = append(messages, map[string]string{
				"role":    "assistant",
				"content": answer,
			})
		}
		return answer
	}

	if len(promptArgs) > 0 {
		prompt := strings.Join(promptArgs, " ")
		executeChat(prompt)
		return
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := io.ReadAll(os.Stdin)
		if err == nil && len(strings.TrimSpace(string(b))) > 0 {
			executeChat(strings.TrimSpace(string(b)))
			return
		}
	}

	fmt.Printf("ProofGate Interactive Chat (route: %s)\n", *route)
	fmt.Println("Type 'exit' or 'quit' to end session.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You > ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Bye!")
			break
		}

		fmt.Print("Assistant > ")
		executeChat(input)
		fmt.Println()
	}
}

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/proofgate/proofgate/internal/store"
)

func kekFromConfig(path string) secrets.KEK {
	cfg, err := config.Load(path)
	if err != nil {
		die("config: %v", err)
	}
	s := cfg.Secrets
	k, err := secrets.FromConfig(s.KEK, s.LocalKEKFile, s.VaultAddr, s.VaultKey, s.VaultAuth, s.VaultRole)
	if err != nil {
		die("kek: %v", err)
	}
	return k
}

func providerKey(ctx context.Context, st *store.Store, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl provider-key set|list|rotate-kek")
	}
	fs := flag.NewFlagSet("provider-key", flag.ExitOnError)
	cfgPath := fs.String("config", "deploy/proofgate.yaml", "gateway config (for the KEK settings)")
	provider := fs.String("provider", "", "provider name from the config")
	actor := fs.String("actor", os.Getenv("USER"), "who is doing this")
	if *actor == "" {
		*actor = os.Getenv("USERNAME") // Windows fallback
	}
	fromLocal := fs.String("from-local-kek-file", "", "old local KEK file (rotate-kek)")
	_ = fs.Parse(args[1:])
	switch args[0] {
	case "set":
		if *provider == "" {
			die("--provider is required")
		}
		fmt.Fprintln(os.Stderr, "Paste the provider API key and press Enter (input is read from stdin, not from arguments):")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			die("read key: %v", err)
		}
		key := strings.TrimSpace(line)
		if key == "" {
			die("empty key")
		}
		k := kekFromConfig(*cfgPath)
		sealed, err := secrets.Seal(ctx, k, []byte(key), secrets.AAD(*provider))
		if err != nil {
			die("seal: %v", err)
		}
		v, err := st.PutCredential(ctx, *provider, sealed, *actor)
		if err != nil {
			die("store: %v", err)
		}
		fmt.Printf("stored %s key version %d (sealed with %s)\n", *provider, v, k.ID())
	case "list":
		infos, err := st.ListCredentials(ctx)
		if err != nil {
			die("list: %v", err)
		}
		for _, c := range infos {
			fmt.Printf("%s\tv%d\t%s\t%s\t%s\tactive=%v\n", c.Provider, c.Version, c.KEKID, c.CreatedBy, c.CreatedAt.Format("2006-01-02"), c.Active)
		}
	case "rotate-kek":
		oldK, err := secrets.LoadLocalKEK(*fromLocal)
		if err != nil {
			die("old kek: %v", err)
		}
		newK := kekFromConfig(*cfgPath)
		all, err := st.AllSealed(ctx)
		if err != nil {
			die("read: %v", err)
		}
		n := 0
		for id, s := range all {
			if s.KEKID != oldK.ID() {
				continue
			}
			r, err := secrets.Rewrap(ctx, oldK, newK, s)
			if err != nil {
				die("rewrap %v: %v", id, err)
			}
			if err := st.ReplaceWrapped(ctx, id[0].(string), id[1].(int), r); err != nil {
				die("save %v: %v", id, err)
			}
			n++
		}
		fmt.Printf("rewrapped %d credentials to %s\n", n, newK.ID())
	default:
		die("usage: proofgatectl provider-key set|list|rotate-kek")
	}
}

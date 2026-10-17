// Command proofgate runs the LLM gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/agentrun"
	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/app"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/cache"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/control"
	"github.com/proofgate/proofgate/internal/health"
	"github.com/proofgate/proofgate/internal/mcpproxy"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/proof"
	"github.com/proofgate/proofgate/internal/provider"
	"github.com/proofgate/proofgate/internal/ratelimit"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/proofgate/proofgate/internal/server"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/proofgate/proofgate/internal/telemetry"
	"github.com/proofgate/proofgate/internal/version"
	"github.com/redis/go-redis/v9"
)

func main() {
	defaultCfg := "/etc/proofgate/proofgate.yaml"
	if env := os.Getenv("PROOFGATE_CONFIG"); env != "" {
		defaultCfg = env
	}
	cfgPath := flag.String("config", defaultCfg, "config file")
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	healthURL := flag.String("healthcheck-url", "", "URL to probe for healthcheck (overrides default)")
	flag.Parse()
	if *healthcheck {
		targetURL := *healthURL
		if targetURL == "" {
			targetURL = os.Getenv("PROOFGATE_HEALTHCHECK_URL")
		}
		if targetURL == "" {
			if cfg, err := config.Load(*cfgPath); err == nil && cfg.Server.Addr != "" {
				addr := cfg.Server.Addr
				if strings.HasPrefix(addr, ":") {
					addr = "127.0.0.1" + addr
				}
				targetURL = "http://" + addr + "/healthz"
			} else {
				targetURL = "http://127.0.0.1:8080/healthz"
			}
		}
		resp, err := http.Get(targetURL) //nolint:noctx
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	}
	scrubber := telemetry.NewScrubber(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(slog.New(scrubber))
	if err := run(*cfgPath, scrubber); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func needsDBKeys(cfg *config.Config) bool {
	for _, p := range cfg.Providers {
		if p.APIKeyDB {
			return true
		}
	}
	return false
}

func run(cfgPath string, scrubber *telemetry.Scrubber) error {
	startTime := time.Now()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	var lintErrors []string
	for _, issue := range config.Lint(cfg) {
		if issue.Severity == config.SeverityError {
			slog.Error("config lint error", "rule", issue.Rule, "msg", issue.Message)
			lintErrors = append(lintErrors, fmt.Sprintf("[%s] %s", issue.Rule, issue.Message))
		} else {
			slog.Warn("config lint warning", "rule", issue.Rule, "msg", issue.Message)
		}
	}
	if len(lintErrors) > 0 {
		return fmt.Errorf("config lint failed with %d error(s):\n%s", len(lintErrors), strings.Join(lintErrors, "\n"))
	}
	for _, p := range cfg.Providers {
		if p.APIKeyEnv != "" {
			if k := os.Getenv(p.APIKeyEnv); k != "" {
				scrubber.Register(k)
			}
		}
	}
	shutdownTracing, err := telemetry.SetupTracingWithEndpoint(ctx, cfg.Telemetry.ServiceName, cfg.Telemetry.OTLPEndpoint, cfg.Telemetry.SampleRate)
	if err != nil {
		return err
	}
	st, err := store.OpenWithConfig(ctx, cfg.Database.URL, cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	ropt, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return err
	}
	ropt.Protocol = 2
	rdb := redis.NewClient(ropt)
	defer rdb.Close()

	chConn, err := analytics.Open(ctx, cfg.Analytics.ClickHouseDSN)
	if err != nil {
		return err
	}
	if err := analytics.Migrate(ctx, chConn); err != nil {
		return err
	}
	if err := analytics.ApplyRetention(ctx, chConn, cfg.Analytics.TextTTLHours, cfg.Analytics.TTLDays); err != nil {
		slog.Warn("apply clickhouse retention failed", "err", err)
	}
	metrics := telemetry.NewMetrics()
	usageDropped := metrics.Counter("proofgate_analytics_dropped_total", "Analytics rows dropped because the queue was full.", "table")
	usage := analytics.NewBatcher("usage_events", cfg.Analytics.UsageBatcher.Capacity, cfg.Analytics.UsageBatcher.BatchSize, cfg.Analytics.UsageBatcher.FlushInterval, analytics.InsertUsage(chConn),
		func() { usageDropped.WithLabelValues("usage_events").Inc() })

	kek, err := secrets.FromConfigWithOptions(cfg.Secrets.KEK, cfg.Secrets.LocalKEKFile, cfg.Secrets.VaultAddr, cfg.Secrets.VaultKey,
		cfg.Secrets.VaultAuth, cfg.Secrets.VaultRole, cfg.Secrets.VaultTokenFile)
	if err != nil && needsDBKeys(cfg) {
		return err
	}
	if kek != nil {
		var prevKEKs []secrets.KEK
		for _, pcfg := range cfg.Secrets.PreviousKEKs {
			pkek, perr := secrets.FromConfigWithOptions(pcfg.KEK, pcfg.LocalKEKFile, pcfg.VaultAddr, pcfg.VaultKey, pcfg.VaultAuth, pcfg.VaultRole, pcfg.VaultTokenFile)
			if perr != nil {
				return fmt.Errorf("initialize previous rotation KEK: %w", perr)
			}
			if pkek != nil {
				prevKEKs = append(prevKEKs, pkek)
			}
		}
		multiKEK := secrets.NewMultiKEK(kek, prevKEKs...)
		if cfg.Secrets.KEK == "vault" {
			multiKEK.SetFallbackResolver(secrets.NewDynamicVaultResolverFromConfig(cfg.Secrets.VaultAddr, cfg.Secrets.VaultAuth, cfg.Secrets.VaultRole, cfg.Secrets.VaultTokenFile))
		}
		kek = multiKEK
	}
	var keyCache *secrets.KeyCache
	if kek != nil {
		keyCache = secrets.NewKeyCache(st, kek, cfg.Secrets.CacheTTL, scrubber.Register)
	}
	keys := func(provider string) provider.KeyFunc {
		return func(ctx context.Context) (string, error) {
			if keyCache == nil {
				return "", errors.New("key cache not configured")
			}
			return keyCache.Get(ctx, provider)
		}
	}

	breakers := router.NewBreakers(cfg.Breakers.Threshold, cfg.Breakers.Cooldown, time.Now)
	rt, err := server.BuildRuntime(cfg, breakers, os.Getenv, keys)
	if err != nil {
		return err
	}
	tracker := health.NewTracker(cfg.Health, cfg.SLOs, time.Now)
	tracker.SetTargets(rt.Router.Targets())
	rt.Router.SetHealth(tracker.Degraded)
	state := &server.State{}

	limiter := ratelimit.NewRedis(rdb)
	ledger := budget.NewRedisLedger(rdb)

	exact := cache.NewExact(rdb)
	semantic := cache.NewSemantic(rdb)

	var h *server.Handlers
	var routingShadowWorker *proof.RoutingShadowWorker
	var cacheLabelWorker *proof.CacheLabelWorker
	judgeTarget := ""
	embedFunc := cache.EmbedFunc(func(ctx context.Context, route, tenant, text string) ([]float32, error) {
		if h == nil {
			return nil, errors.New("handlers not initialized")
		}
		start := time.Now()
		vecs, u, target, err := h.EmbedInternal(ctx, route, []string{text})
		var cost int64
		if err == nil {
			cost, _ = state.Load().Pricing.CostMicros(target.String(), u)
			_ = ledger.Add(context.WithoutCancel(ctx), tenant, budget.Month(time.Now()), cost)
		}
		usage.Emit(analytics.UsageEvent{TS: start, RequestID: uuid.NewString(), TenantID: tenant, Route: route,
			Target: target.String(), Kind: "cache_embed", Status: telemetry.StatusOf(err), Cache: "none",
			PromptTokens: uint32(u.PromptTokens), CostMicros: cost, LatencyMs: uint32(time.Since(start).Milliseconds())})
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			return nil, errors.New("empty embedding vector returned")
		}
		return vecs[0], nil
	})
	embedder := cache.NewLRUEmbedder(embedFunc, cfg.Defaults.EmbedderCacheSize)

	cacheErrors := metrics.Counter("proofgate_cache_errors_total", "Cache errors by operation.", "op")
	cacheDropped := metrics.Counter("proofgate_cache_dropped_total", "Cache write tasks dropped due to worker congestion.")

	cacheStage := cache.NewStage(exact, semantic, embedder,
		func(op string) { cacheErrors.WithLabelValues(op).Inc() },
		func() { cacheDropped.WithLabelValues().Inc() },
	)

	// Attach proof recording to the production ClickHouse connection. Both
	// recorders are bounded and non-blocking on the request path.
	chClient := proof.NewCHWithStoreText(chConn, cfg.Analytics.StoreText)
	proofDropped := metrics.Counter("proofgate_proof_dropped_total", "Proof rows dropped because a queue was full.", "queue")
	shadowRecorder := proof.NewShadowRecorder(chClient, 50_000, 5_000, time.Second,
		func() { proofDropped.WithLabelValues("cache_shadow").Inc() },
		func(err error) { slog.Warn("cache shadow flush failed", "err", err) })
	// The config flag is an explicit deployment opt-in. Until it is enabled,
	// shadow evidence may only be collected for tenants that explicitly opted in.
	cacheStage.SetShadowConsentRequired(!cfg.Proof.ShadowConsent)
	cacheStage.SetOnShadow(shadowRecorder.CacheHook())
	decisionBatch := analytics.NewBatcher("routing_decisions", 50_000, 5_000, time.Second,
		chClient.InsertDecisions,
		func() { proofDropped.WithLabelValues("routing_decisions").Inc() })

	runs := agentrun.NewRedisStore(rdb)
	fuzzyDetector := agentrun.NewFuzzyDetector(embedFunc, agentrun.NewRedisFuzzyStore(rdb))

	buildPipeline := func(runtime *server.Runtime, pipelineCfg *config.Config) *pipeline.Pipeline {
		return app.BuildPipeline(app.Deps{
			Metrics:           metrics,
			UsageEmit:         usage.Emit,
			Runs:              runs,
			FuzzyDetector:     fuzzyDetector,
			DefaultMaxToks:    pipelineCfg.Defaults.DefaultMaxTokens,
			CacheStage:        cacheStage,
			Limiter:           limiter,
			MaxTokReserve:     pipelineCfg.Defaults.MaxTokensReserve,
			FailOpenInc:       metrics.FailOpen.Inc,
			Ledger:            ledger,
			Pricing:           runtime.Pricing,
			BudgetCfg:         pipelineCfg.Budget,
			BudgetFailOpenInc: metrics.BudgetFailOpen.Inc,
			DecisionEmit:      decisionBatch.Emit,
			RoutingShadow: func(ctx context.Context, task proof.RoutingShadowTask, req *api.ChatRequest) {
				if routingShadowWorker == nil || h == nil || judgeTarget == "" {
					return
				}
				go func() {
					counterfactual := task.StrongTarget
					if task.Decision != "cheap" {
						counterfactual = task.CheapTarget
					}
					resp, err := h.ChatInternal(ctx, counterfactual, req.Clone())
					if err != nil || resp == nil || len(resp.Choices) == 0 {
						return
					}
					answer := resp.Choices[0].Message.Content.PlainText()
					if task.Decision == "cheap" {
						task.StrongAnswer = answer
					} else {
						task.CheapAnswer = answer
					}
					task.JudgeModel = judgeTarget
					_, _ = routingShadowWorker.EvaluateAndRecord(context.WithoutCancel(ctx), task)
				}()
			},
		})
	}
	pipe := buildPipeline(rt, cfg)
	rt.Pipeline = pipe
	state.Store(rt)
	h = &server.Handlers{State: state, Breakers: breakers, Pipeline: pipe, Limiter: limiter, Ledger: ledger, Now: time.Now, Health: tracker, Metrics: metrics,
		MaxRequestBodyBytes: cfg.Server.MaxRequestBodyBytes,
		OnEmbed: func(ev server.EmbedEvent) {
			metrics.ObserveEmbed(ev.Route, ev.Target, telemetry.StatusOf(ev.Err), ev.Tokens, ev.CostMicros, ev.Duration)
			usage.Emit(analytics.UsageEvent{TS: time.Now().Add(-ev.Duration), RequestID: uuid.NewString(), TenantID: ev.Principal.TenantID,
				KeyID: ev.Principal.KeyID, Route: ev.Route, Target: ev.Target, Kind: "embeddings", Status: telemetry.StatusOf(ev.Err),
				Cache: "none", PromptTokens: uint32(ev.Tokens), CostMicros: ev.CostMicros, LatencyMs: uint32(ev.Duration.Milliseconds())})
		}}
	mcpAudit := analytics.NewBatcher("mcp_calls", cfg.Analytics.MCPBatcher.Capacity, cfg.Analytics.MCPBatcher.BatchSize, cfg.Analytics.MCPBatcher.FlushInterval, analytics.InsertMCP(chConn),
		func() { usageDropped.WithLabelValues("mcp_calls").Inc() })

	h.MCP = mcpproxy.New(mcpproxy.Deps{
		Upstreams:           func() map[string]mcpproxy.Upstream { return state.Load().MCP },
		Runs:                runs,
		Audit:               mcpAudit.Emit,
		MaxRequestBodyBytes: cfg.MCP.MaxRequestBodyBytes,
		RequestTimeout:      cfg.MCP.RequestTimeout,
		MaxRequestBodyBytesFunc: func() int64 {
			if rt := state.Load(); rt != nil && rt.Config != nil {
				return rt.Config.MCP.MaxRequestBodyBytes
			}
			return 0
		},
		RequestTimeoutFunc: func() time.Duration {
			if rt := state.Load(); rt != nil && rt.Config != nil {
				return rt.Config.MCP.RequestTimeout
			}
			return 0
		},
	})

	authMW := auth.NewMiddlewareWithConfig(st, cfg.Auth.CacheTTL, cfg.Auth.NegativeCacheTTL, cfg.Auth.MaxCachedKeys)
	authMW.ClientIPFunc = func(r *http.Request) string {
		var trusted []*net.IPNet
		if active := state.Load(); active != nil && active.Config != nil {
			trusted, _ = active.Config.Server.ParsedTrustedProxies()
		}
		return server.ClientIP(r, trusted)
	}

	var (
		rtMu    sync.Mutex
		fileCfg = cfg
		lastOvs []store.Override
	)
	var redisHealth *health.RedisHealth
	rebuild := func() {
		rtMu.Lock()
		defer rtMu.Unlock()
		merged, errs := server.ApplyOverrides(fileCfg, lastOvs)
		for _, e := range errs {
			slog.Warn("override ignored", "err", e)
		}
		next, err := server.BuildRuntime(merged, breakers, os.Getenv, keys)
		if err != nil {
			slog.Error("runtime rebuild failed; keeping previous", "err", err)
			return
		}
		next.Pipeline = buildPipeline(next, merged)
		cacheStage.SetShadowConsentRequired(!merged.Proof.ShadowConsent)
		if cacheLabelWorker != nil {
			cacheLabelWorker.SetRoutes(cacheLabelRoutes(merged))
		}
		next.Router.SetHealth(tracker.Degraded)
		tracker.SetTargets(next.Router.Targets())
		tracker.SetSLOs(merged.SLOs)
		if redisHealth != nil {
			redisHealth.SetTargets(next.Router.Targets())
		}
		state.Store(next)
	}

	watcher := config.NewWatcher(cfgPath, cfg.WatcherInterval, func(c *config.Config) {
		rtMu.Lock()
		fileCfg = c
		rtMu.Unlock()
		rebuild()
	})
	go watcher.Run(ctx)

	go server.PollOverrides(ctx, st, cfg.OverrideInterval, func(ovs []store.Override) {
		rtMu.Lock()
		changed := !reflect.DeepEqual(ovs, lastOvs)
		lastOvs = ovs
		rtMu.Unlock()
		if changed {
			rebuild()
		}
	})

	probe := func(ctx context.Context, t router.Target) (time.Duration, error) {
		one := 1
		p, ok := state.Load().Registry.Get(t.Provider)
		if !ok {
			return 0, errors.New("unknown provider")
		}
		ctx, cancel := context.WithTimeout(ctx, cfg.Health.ProbeTimeout)
		defer cancel()
		start := time.Now()
		st, err := p.ChatStream(ctx, t.Model, &api.ChatRequest{MaxTokens: &one,
			Messages: []api.Message{{Role: "user", Content: api.Content{Text: "ping"}}}})
		if err != nil {
			return 0, err
		}
		defer st.Close()
		if _, err := st.Recv(); err != nil {
			return 0, err
		}
		return time.Since(start), nil
	}
	go health.NewProber(tracker, probe, cfg.Health.ProbeInterval).Run(ctx)
	instanceID := uuid.NewString()

	switch strings.ToLower(cfg.Health.ShareMode) {
	case "redis":
		if rdb != nil {
			redisHealth = health.NewRedisHealth(rdb, instanceID, tracker, rt.Router.Targets(), 2*time.Second)
			redisHealth.Start(ctx)
			defer redisHealth.Stop()
		}
	case "gossip":
		if cfg.Health.GossipAddr != "" {
			gossip := health.NewGossipWithSecret(cfg.Health.GossipAddr, cfg.Health.GossipPeers, tracker, cfg.Health.ProbeInterval, cfg.Health.GossipSecret)
			if cfg.Health.AllowUnsignedGossip {
				gossip.SetAllowUnsigned(true)
			}
			if err := gossip.Start(); err == nil {
				defer gossip.Stop()
			}
		}
	}
	leader := proof.NewLeader(rdb, instanceID, "proofgate:leader:monitor", 30*time.Second, 10*time.Second)
	leader.Start(ctx)
	judgeTarget = chooseJudgeTarget(cfg)
	if judgeTarget != "" {
		judge := proof.NewJudge(proof.ChatCallerFunc(h.ChatInternal), judgeTarget)
		judge.SetAllowedProviders(cfg.Proof.AllowedJudgeProviders)
		shadowSpend := proof.NewShadowSpendGuard(rdb, 0)
		routingShadowWorker = proof.NewRoutingShadowWorker(chClient, judge, leader, shadowSpend)

		cacheRoutes := cacheLabelRoutes(cfg)
		if len(cacheRoutes) > 0 {
			cacheLabelWorker = proof.NewCacheLabelWorker(leader, shadowSpend, chClient, judge,
				cacheRoutes, judgeTarget, cfg.Proof.MonitorInterval)
			cacheLabelWorker.Start(ctx)
		}
	} else {
		slog.Warn("proof judge disabled: no eligible judge target configured")
	}

	go proof.RunMonitor(ctx, leader, cfg.Proof.MonitorInterval, func(runCtx context.Context) error {
		rt := state.Load()
		if rt != nil && rt.Config != nil {
			return proof.MonitorAllRoutes(runCtx, rt.Config.Routes, chClient, chClient, st, rt.Config.Proof)
		}
		return nil
	})

	ctrlBus := control.NewBus(rdb, instanceID, control.DefaultChannel)
	// API-key and provider-credential caches are process-local. Subscribe every
	// replica before starting the control-bus consumer so revocations and
	// rotations cannot leave a stale positive/secret cache behind.
	authMW.SubscribeControl(ctrlBus)
	if keyCache != nil {
		keyCache.SubscribeControl(ctrlBus)
	}
	ctrlBus.Subscribe(control.OpPurgeSecrets, func(c context.Context, msg control.Message) error {
		if msg.Sender == instanceID {
			return nil
		}
		if keyCache != nil {
			keyCache.Purge()
		}
		return nil
	})
	ctrlBus.Subscribe(control.OpPurgeCache, func(c context.Context, msg control.Message) error {
		if msg.Sender == instanceID {
			return nil
		}
		req, _ := http.NewRequestWithContext(c, "POST", "/admin/cache/purge", nil)
		rec := httptest.NewRecorder()
		server.AdminCachePurgeHandler(rdb)(rec, req)
		return nil
	})
	ctrlBus.Subscribe(control.OpReload, func(c context.Context, msg control.Message) error {
		if msg.Sender == instanceID {
			return nil
		}
		return watcher.Reload()
	})
	go func() {
		if err := ctrlBus.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("control bus error", "err", err)
		}
	}()

	// Refuse unsafe config at startup: non-loopback admin_addr requires admin_auth.enabled
	if !config.IsLoopbackAddr(cfg.Server.AdminAddr) && !cfg.AdminAuth.Enabled {
		if os.Getenv("PROOFGATE_ALLOW_UNAUTH_ADMIN") != "1" {
			return fmt.Errorf("fatal: server.admin_addr %q is non-loopback while admin_auth.enabled is false; enable admin_auth or set PROOFGATE_ALLOW_UNAUTH_ADMIN=1 to override", cfg.Server.AdminAddr)
		}
		slog.Warn("SECURITY WARNING: admin_addr is non-loopback while admin_auth.enabled is false; running insecurely due to PROOFGATE_ALLOW_UNAUTH_ADMIN=1", "admin_addr", cfg.Server.AdminAddr)
	}

	public := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           telemetry.Tracing(telemetry.AccessLog(h.Routes(authMW.Handler))),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	var adminAuthSvc *adminauth.Service
	if cfg.AdminAuth.Enabled {
		adminAuthSvc = adminauth.NewService(st, rdb, cfg.AdminAuth, scrubber)
	}

	// 1. Metrics listener: isolated to metrics and healthz checks
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
	metricsMux.HandleFunc("GET /healthz", server.LivenessHandler)
	metricsMux.HandleFunc("GET /readyz", h.Readiness(
		server.ReadinessDependency{Name: "postgres", Check: func(ctx context.Context) error {
			return st.Ping(ctx)
		}},
		server.ReadinessDependency{Name: "redis", Check: func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		}},
		server.ReadinessDependency{Name: "clickhouse", Check: func(ctx context.Context) error {
			return chConn.Ping(ctx)
		}},
	))

	metricsSrv := &http.Server{
		Addr:              cfg.Server.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	// 2. Admin listener: operational and control-plane endpoints
	admin := http.NewServeMux()
	admin.Handle("GET /healthz", http.HandlerFunc(server.LivenessHandler))
	admin.Handle("GET /readyz", h.Readiness(
		server.ReadinessDependency{Name: "postgres", Check: func(ctx context.Context) error {
			return st.Ping(ctx)
		}},
		server.ReadinessDependency{Name: "redis", Check: func(ctx context.Context) error {
			return rdb.Ping(ctx).Err()
		}},
		server.ReadinessDependency{Name: "clickhouse", Check: func(ctx context.Context) error {
			return chConn.Ping(ctx)
		}},
	))

	cpDeps := &server.ControlPlaneDeps{
		State:          state,
		Store:          st,
		Redis:          rdb,
		AuthService:    adminAuthSvc,
		AuthMiddleware: authMW,
		TenantEraser:   store.NewTenantEraser(st, rdb, analytics.NewTenantPurger(chConn)),
		ReloadFunc:     watcher.Reload,
		PurgeCache:     server.AdminCachePurgeHandler(rdb),
		PurgeSecrets: func() {
			if keyCache != nil {
				keyCache.Purge()
			}
		},
		PublishControl: func(c context.Context, op string, args map[string]string) error {
			return ctrlBus.Publish(c, op, args)
		},
		KEK:         kek,
		KeyCache:    keyCache,
		HealthAdmin: h.HealthAdmin,
		Handlers:    h,
		StartTime:   startTime,
		AdminAddr:   cfg.Server.AdminAddr,
		ServerAddr:  cfg.Server.Addr,
		// Config is intentionally left nil. Handlers resolve trusted proxies and
		// admin-chat policy from the atomically reloaded State snapshot.
		Metrics:     metrics,
		EnablePprof: cfg.Server.EnablePprof,
	}
	server.RegisterAdminRoutes(admin, cpDeps, cfg.AdminAuth.Enabled)
	adminSrv := &http.Server{
		Addr:              cfg.Server.AdminAddr,
		Handler:           admin,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for _, r := range state.Load().Router.Routes() {
					for _, tg := range r.Targets {
						metrics.SetBreaker(tg.String(), breakers.State(tg))
					}
				}
			}
		}
	}()

	errc := make(chan error, 3)
	go func() { errc <- public.ListenAndServe() }()
	go func() { errc <- adminSrv.ListenAndServe() }()
	go func() { errc <- metricsSrv.ListenAndServe() }()
	slog.Info("proofgate started", "version", version.Version, "addr", cfg.Server.Addr, "admin", cfg.Server.AdminAddr, "metrics", cfg.Server.MetricsAddr)

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	slog.Info("shutting down, draining in-flight requests")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.Server.DrainTimeout)
	defer cancel()
	_ = metricsSrv.Shutdown(sctx)
	_ = adminSrv.Shutdown(sctx)
	if err := public.Shutdown(sctx); err != nil {
		slog.Warn("drain timed out", "err", err)
	}
	cacheStage.Wait()
	if cacheLabelWorker != nil {
		cacheLabelWorker.Stop()
	}

	// Create a fresh context for final flush because the drain context might already be expired
	fctx, fcancel := context.WithTimeout(context.Background(), cfg.Analytics.FlushTimeout)
	defer fcancel()
	if err := usage.Close(fctx); err != nil {
		slog.Warn("usage analytics close failed", "err", err)
	}
	if err := mcpAudit.Close(fctx); err != nil {
		slog.Warn("mcp analytics close failed", "err", err)
	}
	shadowRecorder.Close()
	if err := decisionBatch.Close(fctx); err != nil {
		slog.Warn("routing decision proof close failed", "err", err)
	}
	return shutdownTracing(fctx)
}

func chooseJudgeTarget(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	allowed := func(provider string) bool {
		if len(cfg.Proof.AllowedJudgeProviders) == 0 {
			return true
		}
		for _, candidate := range cfg.Proof.AllowedJudgeProviders {
			if strings.EqualFold(candidate, provider) {
				return true
			}
		}
		return false
	}
	for _, route := range cfg.Routes {
		if route.Embeddings {
			continue
		}
		candidates := []string{route.SmartRoute.StrongTarget}
		for _, target := range route.Targets {
			candidates = append(candidates, target.Provider+"/"+target.Model)
		}
		for _, candidate := range candidates {
			target, ok := router.ParseTarget(candidate)
			if ok && allowed(target.Provider) {
				return candidate
			}
		}
	}
	return ""
}

func cacheLabelRoutes(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var routes []string
	for _, route := range cfg.Routes {
		if route.Cache.Mode == "shadow" || route.Cache.Mode == "on" {
			routes = append(routes, route.Name)
		}
	}
	return routes
}

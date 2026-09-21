// Command proofgate runs the LLM gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/cache"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/guard"
	"github.com/proofgate/proofgate/internal/health"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/ratelimit"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/server"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/proofgate/proofgate/internal/telemetry"
	"github.com/proofgate/proofgate/internal/version"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfgPath := flag.String("config", "/etc/proofgate/proofgate.yaml", "config file")
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	flag.Parse()
	if *healthcheck {
		resp, err := http.Get("http://127.0.0.1:8080/healthz") //nolint:noctx
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(*cfgPath); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	shutdownTracing, err := telemetry.SetupTracing(ctx, "proofgate")
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	ropt, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		return err
	}
	ropt.Protocol = 2
	rdb := redis.NewClient(ropt)
	defer rdb.Close()

	chConn, err := analytics.Open(ctx, os.Getenv("CLICKHOUSE_DSN"))
	if err != nil {
		return err
	}
	if err := analytics.Migrate(ctx, chConn); err != nil {
		return err
	}
	metrics := telemetry.NewMetrics()
	usageDropped := metrics.Counter("proofgate_analytics_dropped_total", "Analytics rows dropped because the queue was full.", "table")
	usage := analytics.NewBatcher("usage_events", 50_000, 5_000, time.Second, analytics.InsertUsage(chConn),
		func() { usageDropped.WithLabelValues("usage_events").Inc() })

	breakers := router.NewBreakers(5, 30*time.Second, time.Now)
	rt, err := server.BuildRuntime(cfg, breakers, os.Getenv)
	if err != nil {
		return err
	}
	tracker := health.NewTracker(cfg.Health, cfg.SLOs, time.Now)
	rt.Router.SetHealth(tracker.Degraded)
	state := &server.State{}
	state.Store(rt)

	limiter := ratelimit.NewRedis(rdb)
	ledger := budget.NewRedisLedger(rdb)

	exact := cache.NewExact(rdb)
	semantic := cache.NewSemantic(rdb)

	var h *server.Handlers
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
	embedder := cache.NewLRUEmbedder(embedFunc, 10_000)

	cacheErrors := metrics.Counter("proofgate_cache_errors_total", "Cache errors by operation.", "op")
	cacheDropped := metrics.Counter("proofgate_cache_dropped_total", "Cache write tasks dropped due to worker congestion.")

	cacheStage := cache.NewStage(exact, semantic, embedder,
		func(op string) { cacheErrors.WithLabelValues(op).Inc() },
		func() { cacheDropped.WithLabelValues().Inc() },
	)

	// Order matters: After runs in reverse, so metrics and trace (first) observe the final state (last).
	pipe := pipeline.New(
		metrics.Stage(),
		telemetry.TraceStage(),
		analytics.UsageStage(usage.Emit),
		guard.NewStage(),
		cacheStage,
		ratelimit.NewStage(limiter, cfg.Defaults.MaxTokensReserve, cfg.Defaults.DefaultMaxTokens, metrics.FailOpen.Inc),
		budget.NewStage(ledger, time.Now),
	)
	h = &server.Handlers{State: state, Breakers: breakers, Pipeline: pipe, Limiter: limiter, Ledger: ledger, Now: time.Now, Health: tracker,
		OnEmbed: func(ev server.EmbedEvent) {
			metrics.ObserveEmbed(ev.Route, ev.Target, telemetry.StatusOf(ev.Err), ev.Tokens, ev.CostMicros, ev.Duration)
			usage.Emit(analytics.UsageEvent{TS: time.Now().Add(-ev.Duration), RequestID: uuid.NewString(), TenantID: ev.Principal.TenantID,
				KeyID: ev.Principal.KeyID, Route: ev.Route, Target: ev.Target, Kind: "embeddings", Status: telemetry.StatusOf(ev.Err),
				Cache: "none", PromptTokens: uint32(ev.Tokens), CostMicros: ev.CostMicros, LatencyMs: uint32(ev.Duration.Milliseconds())})
		}}
	authMW := auth.NewMiddleware(st, 30*time.Second, 5*time.Second)

	var (
		rtMu    sync.Mutex
		fileCfg = cfg
		lastOvs []store.Override
	)
	rebuild := func() {
		rtMu.Lock()
		defer rtMu.Unlock()
		merged, errs := server.ApplyOverrides(fileCfg, lastOvs)
		for _, e := range errs {
			slog.Warn("override ignored", "err", e)
		}
		next, err := server.BuildRuntime(merged, breakers, os.Getenv)
		if err != nil {
			slog.Error("runtime rebuild failed; keeping previous", "err", err)
			return
		}
		next.Router.SetHealth(tracker.Degraded)
		tracker.SetSLOs(merged.SLOs)
		state.Store(next)
	}

	watcher := config.NewWatcher(cfgPath, 5*time.Second, func(c *config.Config) {
		rtMu.Lock()
		fileCfg = c
		rtMu.Unlock()
		rebuild()
	})
	go watcher.Run(ctx)

	go server.PollOverrides(ctx, st, 10*time.Second, func(ovs []store.Override) {
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
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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

	public := &http.Server{Addr: cfg.Server.Addr, Handler: telemetry.Tracing(telemetry.AccessLog(h.Routes(authMW.Handler))),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}

	admin := http.NewServeMux()
	admin.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
	admin.HandleFunc("GET /admin/health", h.HealthAdmin)
	admin.HandleFunc("POST /admin/cache/purge", server.AdminCachePurgeHandler(rdb))
	admin.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ping(r.Context()); err != nil {
			http.Error(w, "postgres: "+err.Error(), 503)
			return
		}
		if err := rdb.Ping(r.Context()).Err(); err != nil {
			http.Error(w, "redis: "+err.Error(), 503)
			return
		}
		w.WriteHeader(200)
	})
	admin.HandleFunc("POST /admin/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := watcher.Reload(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(204)
	})
	admin.HandleFunc("GET /debug/pprof/", pprof.Index)
	admin.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	admin.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	adminSrv := &http.Server{Addr: cfg.Server.AdminAddr, Handler: admin, ReadHeaderTimeout: 10 * time.Second}

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

	errc := make(chan error, 2)
	go func() { errc <- public.ListenAndServe() }()
	go func() { errc <- adminSrv.ListenAndServe() }()
	slog.Info("proofgate started", "version", version.Version, "addr", cfg.Server.Addr, "admin", cfg.Server.AdminAddr)

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	slog.Info("shutting down, draining in-flight requests")
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = adminSrv.Shutdown(sctx)
	if err := public.Shutdown(sctx); err != nil {
		slog.Warn("drain timed out", "err", err)
	}
	cacheStage.Wait()
	_ = usage.Close(sctx)
	return shutdownTracing(sctx)
}

package analytics

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/telemetry"
)

type UsageEvent struct {
	TS                                        time.Time
	RequestID, TenantID, KeyID, Route, Target string
	Kind, Status, Cache, RunID                string
	Stream                                    bool
	Attempts                                  uint8
	PromptTokens, CompletionTokens            uint32
	CachedTokens                              uint32
	CostMicros, SavedMicros                   int64
	LatencyMs, TTFTMs, OverheadUs             uint32
	UsageEstimated                            bool
}

func clampU32(d int64) uint32 {
	if d < 0 {
		return 0
	}
	if d > int64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(d)
}

func EventFromCall(c *pipeline.Call) UsageEvent {
	kind := "chat"
	if k, ok := c.Values["usage.kind"].(string); ok && k != "" {
		kind = k
	}
	e := UsageEvent{TS: c.Start, RequestID: c.ID, TenantID: c.Principal.TenantID, KeyID: c.Principal.KeyID,
		Kind: kind, Status: telemetry.StatusOf(c.Err), Cache: c.CacheStatus, Stream: c.Stream,
		Attempts: uint8(min(c.Attempts, 255)), PromptTokens: clampU32(int64(c.Usage.PromptTokens)),
		CompletionTokens: clampU32(int64(c.Usage.CompletionTokens)), CachedTokens: clampU32(int64(c.Usage.CachedTokens())),
		CostMicros: c.CostMicros, LatencyMs: clampU32(c.Latency.Milliseconds()), TTFTMs: clampU32(c.TTFT.Milliseconds())}
	if c.Route != nil {
		e.Route = c.Route.Name
	}
	if c.Target.Provider != "" {
		e.Target = c.Target.String()
		e.OverheadUs = clampU32((c.Latency - c.UpstreamTime).Microseconds())
	}
	if v, ok := c.Values["cache.saved_micros"].(int64); ok {
		e.SavedMicros = v
	}
	if v, ok := c.Values["run.id"].(string); ok {
		e.RunID = v
	}
	_, e.UsageEstimated = c.Values["usage.estimated"]
	return e
}

func b2u(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func InsertUsage(conn driver.Conn) func(context.Context, []UsageEvent) error {
	return func(ctx context.Context, rows []UsageEvent) error {
		batch, err := conn.PrepareBatch(ctx, "INSERT INTO usage_events")
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := batch.Append(r.TS, r.RequestID, r.TenantID, r.KeyID, r.Route, r.Target, r.Kind, b2u(r.Stream),
				r.Status, r.Cache, r.Attempts, r.PromptTokens, r.CompletionTokens, r.CachedTokens, r.CostMicros,
				r.SavedMicros, r.LatencyMs, r.TTFTMs, r.OverheadUs, b2u(r.UsageEstimated), r.RunID); err != nil {
				return err
			}
		}
		return batch.Send()
	}
}

type usageStage struct{ emit func(UsageEvent) bool }

func UsageStage(emit func(UsageEvent) bool) pipeline.Stage { return usageStage{emit} }

func (usageStage) Name() string                                        { return "usage" }
func (usageStage) Before(context.Context, *pipeline.Call) (bool, error) { return false, nil }
func (s usageStage) After(_ context.Context, c *pipeline.Call)          { s.emit(EventFromCall(c)) }

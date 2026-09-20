package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/proofgate/proofgate/internal/pipeline"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/proofgate/proofgate")

// SetupTracing installs an OTLP/HTTP exporter when OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise tracing is a no-op.
func SetupTracing(ctx context.Context, service string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(service)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1.0))))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

func Tracing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path, trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(semconv.HTTPRequestMethodKey.String(r.Method), semconv.URLPath(r.URL.Path)))
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type traceStage struct{}

func TraceStage() pipeline.Stage { return traceStage{} }

func (traceStage) Name() string                                        { return "trace" }
func (traceStage) Before(context.Context, *pipeline.Call) (bool, error) { return false, nil }

func (traceStage) After(ctx context.Context, c *pipeline.Call) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", c.Target.Provider),
		attribute.String("gen_ai.response.model", c.Target.Model),
		attribute.Int("gen_ai.request.max_tokens", c.Request.EffectiveMaxTokens(0)),
		attribute.Int("gen_ai.usage.input_tokens", c.Usage.PromptTokens),
		attribute.Int("gen_ai.usage.output_tokens", c.Usage.CompletionTokens),
		attribute.String("proofgate.cache", c.CacheStatus),
		attribute.Int("proofgate.attempts", c.Attempts),
		attribute.String("proofgate.cost_usd", fmt.Sprintf("%.6f", float64(c.CostMicros)/1e6)),
		attribute.String("proofgate.tenant_id", c.Principal.TenantID),
	}
	if c.Route != nil {
		attrs = append(attrs, attribute.String("gen_ai.request.model", c.Route.Name), attribute.String("proofgate.route", c.Route.Name))
	}
	span.SetAttributes(attrs...)
	if c.Err != nil {
		span.SetStatus(codes.Error, StatusOf(c.Err))
	}
}

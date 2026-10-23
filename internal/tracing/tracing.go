// Package tracing configures OpenTelemetry. When disabled, the global no-op
// tracer is used and span calls cost a few nanoseconds.
package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/shadatr/switchyard/internal/config"
)

// Tracer is the gateway's tracer (no-op until Setup enables tracing).
func Tracer() trace.Tracer { return otel.Tracer("github.com/shadatr/switchyard") }

// Setup installs an OTLP/HTTP exporter if tracing is enabled and returns a
// shutdown function that flushes pending spans.
func Setup(ctx context.Context, cfg config.Tracing, version string) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.Endpoint))
	if err != nil {
		return nil, err
	}
	// Schemaless avoids "conflicting Schema URL" errors when the semconv
	// package and the SDK's default resource use different schema versions.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName("switchyard"), semconv.ServiceVersion(version)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp.Shutdown, nil
}

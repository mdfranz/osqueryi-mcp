package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	logsdk "go.opentelemetry.io/otel/sdk/log"
	metricSdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	traceSdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/mdfranz/osqueryi-mcp"

// Telemetry owns the optional OpenTelemetry providers used for Logfire export.
// It is intentionally a no-op unless the standard OTLP endpoint and headers are
// both configured.
type Telemetry struct {
	enabled        bool
	tracerProvider *traceSdk.TracerProvider
	meterProvider  *metricSdk.MeterProvider
	loggerProvider *logsdk.LoggerProvider
}

func initTelemetry(ctx context.Context) (*Telemetry, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" || os.Getenv("OTEL_EXPORTER_OTLP_HEADERS") == "" {
		return &Telemetry{}, nil
	}

	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "osqueryi-mcp"
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		"",
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(CommitHash),
		attribute.String("vcs.revision", CommitHash),
	))
	if err != nil {
		return &Telemetry{}, err
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return &Telemetry{}, err
	}
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return &Telemetry{}, errors.Join(err, traceExporter.Shutdown(ctx))
	}
	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		return &Telemetry{}, errors.Join(err, traceExporter.Shutdown(ctx), metricExporter.Shutdown(ctx))
	}

	t := &Telemetry{
		enabled:        true,
		tracerProvider: traceSdk.NewTracerProvider(traceSdk.WithBatcher(traceExporter), traceSdk.WithResource(res)),
		meterProvider: metricSdk.NewMeterProvider(
			metricSdk.WithResource(res),
			metricSdk.WithReader(metricSdk.NewPeriodicReader(metricExporter)),
		),
		loggerProvider: logsdk.NewLoggerProvider(
			logsdk.WithResource(res),
			logsdk.WithProcessor(logsdk.NewBatchProcessor(logExporter)),
		),
	}
	otel.SetTracerProvider(t.tracerProvider)
	otel.SetMeterProvider(t.meterProvider)
	return t, nil
}

func (t *Telemetry) Enabled() bool {
	return t != nil && t.enabled
}

func (t *Telemetry) Shutdown(ctx context.Context) error {
	if !t.Enabled() {
		return nil
	}
	return errors.Join(
		t.loggerProvider.Shutdown(ctx),
		t.meterProvider.Shutdown(ctx),
		t.tracerProvider.Shutdown(ctx),
	)
}

func (t *Telemetry) slogHandler() slog.Handler {
	if !t.Enabled() {
		return nil
	}
	return sanitizedHandler{next: otelslog.NewHandler(instrumentationName, otelslog.WithLoggerProvider(t.loggerProvider))}
}

func (t *Telemetry) setErrorLogger(logger *slog.Logger) {
	if !t.Enabled() {
		return
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		logger.Warn("telemetry export failed", "error_type", "export_failed")
	}))
}

type multiHandler []slog.Handler

func (h multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var err error
	for _, handler := range h {
		if handler.Enabled(ctx, record.Level) {
			err = errors.Join(err, handler.Handle(ctx, record))
		}
	}
	return err
}

func (h multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	result := make(multiHandler, len(h))
	for i, handler := range h {
		result[i] = handler.WithAttrs(attrs)
	}
	return result
}

func (h multiHandler) WithGroup(name string) slog.Handler {
	result := make(multiHandler, len(h))
	for i, handler := range h {
		result[i] = handler.WithGroup(name)
	}
	return result
}

// sanitizedHandler makes Logfire log export safe even though local logs retain
// detailed diagnostics, including SQL and osquery error text.
type sanitizedHandler struct {
	next slog.Handler
}

var allowedLogAttributes = map[string]struct{}{
	"tool":           {},
	"duration_ms":    {},
	"bytes":          {},
	"bytes_returned": {},
	"tables":         {},
	"cache_hit":      {},
	"outcome":        {},
	"error_type":     {},
}

func (h sanitizedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h sanitizedHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		if _, ok := allowedLogAttributes[attr.Key]; ok {
			clean.AddAttrs(attr)
		}
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h sanitizedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if _, ok := allowedLogAttributes[attr.Key]; ok {
			clean = append(clean, attr)
		}
	}
	return sanitizedHandler{next: h.next.WithAttrs(clean)}
}

func (h sanitizedHandler) WithGroup(name string) slog.Handler {
	return sanitizedHandler{next: h.next.WithGroup(name)}
}

var instruments struct {
	once                sync.Once
	toolCalls           metric.Int64Counter
	toolDuration        metric.Float64Histogram
	osqueryDuration     metric.Float64Histogram
	responseSize        metric.Int64Histogram
	cacheOperations     metric.Int64Counter
	instrumentCreateErr error
}

func metricInstruments() {
	instruments.once.Do(func() {
		meter := otel.Meter(instrumentationName)
		instruments.toolCalls, instruments.instrumentCreateErr = meter.Int64Counter("osqueryi_mcp_tool_calls")
		if instruments.instrumentCreateErr != nil {
			return
		}
		instruments.toolDuration, instruments.instrumentCreateErr = meter.Float64Histogram("osqueryi_mcp_tool_duration", metric.WithUnit("s"))
		if instruments.instrumentCreateErr != nil {
			return
		}
		instruments.osqueryDuration, instruments.instrumentCreateErr = meter.Float64Histogram("osqueryi_mcp_osquery_duration", metric.WithUnit("s"))
		if instruments.instrumentCreateErr != nil {
			return
		}
		instruments.responseSize, instruments.instrumentCreateErr = meter.Int64Histogram("osqueryi_mcp_response_size", metric.WithUnit("By"))
		if instruments.instrumentCreateErr != nil {
			return
		}
		instruments.cacheOperations, instruments.instrumentCreateErr = meter.Int64Counter("osqueryi_mcp_cache_operations")
	})
}

func beginToolSpan(ctx context.Context, tool string) (context.Context, trace.Span, func(string, int, error)) {
	metricInstruments()
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, "mcp.tool."+tool)
	// The finish closure owns both the span and its safe metric dimensions.
	started := time.Now()
	return ctx, span, func(outcome string, bytesReturned int, err error) {
		attrs := []attribute.KeyValue{attribute.String("mcp.tool.name", tool), attribute.String("outcome", outcome)}
		span.SetAttributes(attrs...)
		if err != nil {
			span.SetStatus(codes.Error, errorType(ctx, err))
		}
		if instruments.instrumentCreateErr == nil {
			instruments.toolCalls.Add(ctx, 1, metric.WithAttributes(attrs...))
			instruments.toolDuration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attrs...))
			if bytesReturned >= 0 {
				instruments.responseSize.Record(ctx, int64(bytesReturned), metric.WithAttributes(attribute.String("mcp.tool.name", tool)))
			}
		}
		span.End()
	}
}

func recordOSQueryMetrics(ctx context.Context, duration time.Duration, outcome string) {
	metricInstruments()
	if instruments.instrumentCreateErr != nil {
		return
	}
	instruments.osqueryDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("outcome", outcome)))
}

func recordCacheOperation(ctx context.Context, operation, outcome string) {
	metricInstruments()
	if instruments.instrumentCreateErr != nil {
		return
	}
	instruments.cacheOperations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("outcome", outcome),
	))
}

func errorType(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return "operation_failed"
}

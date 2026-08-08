package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type capturedHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturedHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturedHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *capturedHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturedHandler) WithGroup(string) slog.Handler      { return h }

func TestSanitizedHandlerExcludesDetailedAttributes(t *testing.T) {
	captured := &capturedHandler{}
	logger := slog.New(sanitizedHandler{next: captured})
	logger.Info("tool_called",
		"tool", "run_query",
		"sql", "SELECT secret FROM users",
		"where", "password = 'secret'",
		"duration_ms", 12,
		"error", "host data",
	)

	if len(captured.records) != 1 {
		t.Fatalf("records = %d, want 1", len(captured.records))
	}
	var attrs []slog.Attr
	captured.records[0].Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	if len(attrs) != 2 || attrs[0].Key != "tool" || attrs[1].Key != "duration_ms" {
		t.Fatalf("sanitized attrs = %#v", attrs)
	}
}

func TestToolSpanDoesNotRecordSensitiveErrors(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	ctx, _, finish := beginToolSpan(context.Background(), "run_query")
	finish("error", -1, errors.New("SELECT secret FROM users"))

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "mcp.tool.run_query" || span.Status.Code.String() != "Error" {
		t.Fatalf("span = %#v", span)
	}
	if len(span.Events) != 0 {
		t.Fatalf("span events contain error details: %#v", span.Events)
	}
	for _, attr := range span.Attributes {
		if strings.Contains(attr.Value.AsString(), "secret") {
			t.Fatalf("sensitive span attribute: %#v", attr)
		}
	}
	_ = ctx
}

func TestTelemetryIsDisabledWithoutOTLPConfiguration(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")

	telemetry, err := initTelemetry(context.Background())
	if err != nil {
		t.Fatalf("init telemetry: %v", err)
	}
	if telemetry.Enabled() {
		t.Fatal("telemetry unexpectedly enabled")
	}
}

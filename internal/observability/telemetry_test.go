package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSQLSpanContainsOperationWithoutSensitiveValues(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	previous := otel.GetTracerProvider()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})
	_, span := StartSQL(context.Background(), "SELECT")
	span.End()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	attributes := map[string]string{}
	for _, attribute := range spans[0].Attributes {
		attributes[string(attribute.Key)] = attribute.Value.AsString()
	}
	if attributes["db.system"] != "postgresql" || attributes["db.operation.name"] != "SELECT" {
		t.Fatalf("unexpected SQL attributes: %#v", attributes)
	}
	for key := range attributes {
		if key == "db.statement" || key == "db.connection_string" || key == "db.query.parameter" {
			t.Fatalf("sensitive SQL attribute was emitted: %s", key)
		}
	}
}

func TestInitSetsTraceContextAndRecordsOperationalSignals(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "audit-test")
	shutdown, err := Init(context.Background(), "ignored", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	ctx, span := StartHTTP(context.Background(), "GET", "/audit-records/export")
	if TraceID(ctx) == "" {
		t.Fatal("HTTP span did not create a trace id")
	}
	SetAuditIdentifiers(span, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222")
	RecordWorker(ctx, "inserted")
	EndHTTP(ctx, span, "/audit-records/export", 200, 0.01)
}

func TestOTLPConfigurationIsOptIn(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	if otlpConfigured() {
		t.Fatal("OTLP unexpectedly enabled")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4317")
	if !otlpConfigured() {
		t.Fatal("OTLP endpoint was not recognized")
	}
}

// Package observability centralizes safe OpenTelemetry signals for the two
// audit executables. It deliberately records technical operation metadata
// only; payloads, credentials, DSNs and HTTP query values are never added.
package observability

import (
	"context"
	"errors"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	httpRequests otelmetric.Int64Counter
	httpDuration otelmetric.Float64Histogram
	workerEvents otelmetric.Int64Counter
)

// Init configures W3C propagation and optional OTLP/gRPC exporters. When no
// OTLP endpoint is configured, SDK providers remain active locally and avoid
// any outbound connection attempts.
func Init(ctx context.Context, serviceName, environment string) (func(context.Context) error, error) {
	if configuredService := os.Getenv("OTEL_SERVICE_NAME"); configuredService != "" {
		serviceName = configuredService
	}
	res := resource.NewWithAttributes("", attribute.String("service.name", serviceName), attribute.String("deployment.environment", environment))
	tracerOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	meterOptions := []sdkmetric.Option{sdkmetric.WithResource(res)}
	shutdowns := make([]func(context.Context) error, 0, 2)
	if otlpConfigured() {
		traceExporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, err
		}
		tracerOptions = append(tracerOptions, sdktrace.WithBatcher(traceExporter))

		metricExporter, err := otlpmetricgrpc.New(ctx)
		if err != nil {
			return nil, err
		}
		meterOptions = append(meterOptions, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)))
	}
	tracerProvider := sdktrace.NewTracerProvider(tracerOptions...)
	meterProvider := sdkmetric.NewMeterProvider(meterOptions...)
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	initInstruments()
	shutdowns = append(shutdowns, tracerProvider.Shutdown, meterProvider.Shutdown)
	return func(shutdownCtx context.Context) error {
		var err error
		for index := len(shutdowns) - 1; index >= 0; index-- {
			err = errors.Join(err, shutdowns[index](shutdownCtx))
		}
		return err
	}, nil
}

func otlpConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
}

func StartHTTP(ctx context.Context, method, route string) (context.Context, oteltrace.Span) {
	initInstruments()
	return otel.Tracer("audit-query-api/http").Start(ctx, "HTTP "+method+" "+route, oteltrace.WithSpanKind(oteltrace.SpanKindServer), oteltrace.WithAttributes(
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
	))
}

func EndHTTP(ctx context.Context, span oteltrace.Span, route string, status int, durationSeconds float64) {
	span.SetAttributes(attribute.Int("http.response.status_code", status))
	if status >= 500 {
		span.SetStatus(codes.Error, "server error")
	}
	httpRequests.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("operation", route), attribute.Int("http.response.status_code", status)))
	httpDuration.Record(ctx, durationSeconds, otelmetric.WithAttributes(attribute.String("operation", route), attribute.Int("http.response.status_code", status)))
	span.End()
}

func StartSQL(ctx context.Context, operation string) (context.Context, oteltrace.Span) {
	return otel.Tracer("audit-service/postgresql").Start(ctx, "postgresql "+operation, oteltrace.WithSpanKind(oteltrace.SpanKindClient), oteltrace.WithAttributes(
		attribute.String("db.system", "postgresql"), attribute.String("db.operation.name", operation),
	))
}

func StartAMQPConsume(ctx context.Context, exchange, routingKey string) (context.Context, oteltrace.Span) {
	initInstruments()
	return otel.Tracer("audit-worker/amqp").Start(ctx, "rabbitmq consume", oteltrace.WithSpanKind(oteltrace.SpanKindConsumer), oteltrace.WithAttributes(
		attribute.String("messaging.system", "rabbitmq"), attribute.String("messaging.operation", "process"),
		attribute.String("messaging.destination.name", exchange), attribute.String("messaging.rabbitmq.destination.routing_key", routingKey),
	))
}

func SetAuditIdentifiers(span oteltrace.Span, eventID, correlationID string) {
	attributes := make([]attribute.KeyValue, 0, 2)
	if eventID != "" {
		attributes = append(attributes, attribute.String("audit.event_id", eventID))
	}
	if correlationID != "" {
		attributes = append(attributes, attribute.String("audit.correlation_id", correlationID))
	}
	span.SetAttributes(attributes...)
}

func RecordWorker(ctx context.Context, result string) {
	initInstruments()
	workerEvents.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("messaging.system", "rabbitmq"), attribute.String("result", result)))
}

func RecordError(span oteltrace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "operation failed")
}

func TraceID(ctx context.Context) string {
	spanContext := oteltrace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func initInstruments() {
	meter := otel.Meter("audit-service")
	httpRequests, _ = meter.Int64Counter("audit.http.server.requests", otelmetric.WithDescription("HTTP requests by logical audit operation and status"))
	httpDuration, _ = meter.Float64Histogram("audit.http.server.duration", otelmetric.WithUnit("s"), otelmetric.WithDescription("HTTP request duration by logical audit operation and status"))
	workerEvents, _ = meter.Int64Counter("audit.worker.events", otelmetric.WithDescription("Audit worker events by processing result"))
}

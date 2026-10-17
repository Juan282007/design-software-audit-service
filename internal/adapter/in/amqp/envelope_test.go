package amqp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	amqp091 "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const validEnvelope = `{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"iam.user.created","version":"1.0","timestamp":"2026-10-11T12:00:00Z","source_service":"iam-service","correlation_id":"44444444-4444-4444-8444-444444444444","actor_id":null,"entity_type":"user","entity_id":"22222222-2222-4222-8222-222222222222","training_center_id":"33333333-3333-4333-8333-333333333333","payload":{"email":"opaque@example.test","nested":{"any":"value"}}}`

func TestParseEnvelopeNormalizesCanonicalFields(t *testing.T) {
	e, err := ParseEnvelope([]byte(validEnvelope))
	if err != nil {
		t.Fatalf("ParseEnvelope() error = %v", err)
	}
	if e.EventType != "iam.user.created" || e.SourceService != "iam-service" || e.Timestamp.Location() != time.UTC {
		t.Fatalf("unexpected normalized envelope: %#v", e)
	}
	if e.ActorID != nil || e.EntityID == nil || e.TrainingCenterID == nil || e.CorrelationID == nil {
		t.Fatalf("optional fields were not normalized: %#v", e)
	}
	if string(e.Payload) != `{"email":"opaque@example.test","nested":{"any":"value"}}` {
		t.Fatalf("payload was changed: %s", e.Payload)
	}
}

func TestExtractTraceContextContinuesAMQPTraceparent(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	ctx := extractTraceContext(context.Background(), amqp091.Table{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})
	if traceID := trace.SpanContextFromContext(ctx).TraceID().String(); traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("traceparent was not extracted: %q", traceID)
	}
}

func TestConsumerReadinessAndRetryCounting(t *testing.T) {
	ready := false
	consumer := NewConsumer(Config{DatabaseReady: func() bool { return ready }}, usecase.AppendAudit{})
	if consumer.Ready() {
		t.Fatal("worker must not be ready before broker connection")
	}
	consumer.brokerReady.Store(true)
	if consumer.Ready() {
		t.Fatal("worker must not be ready while database is unavailable")
	}
	ready = true
	if !consumer.Ready() {
		t.Fatal("worker should be ready when broker and database are ready")
	}
	if retries := consumer.retryCount(amqp091.Table{"x-death": []interface{}{amqp091.Table{"count": int64(2)}, amqp091.Table{"count": int64(1)}}}); retries != 3 {
		t.Fatalf("retries=%d", retries)
	}
}

func TestConsumerStopsCleanlyWhenBrokerIsUnavailableAndContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	consumer := NewConsumer(Config{URL: "not-a-valid-amqp-url", RetryDelayMilliseconds: 1}, usecase.AppendAudit{})
	if err := consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNewAuditRecordKeepsOnlyAuditFields(t *testing.T) {
	envelope, err := ParseEnvelope([]byte(validEnvelope))
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	record, err := NewAuditRecord(envelope, receivedAt)
	if err != nil || record.ID == "" || record.EventID != envelope.EventID || record.ReceivedAt != receivedAt || string(record.Payload) != string(envelope.Payload) {
		t.Fatalf("record=%#v err=%v", record, err)
	}
}

func TestParseEnvelopeRejectsInvalidFieldsBeforePersistence(t *testing.T) {
	cases := []string{
		`{"event_type":"x","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"x","payload":{}}`,
		`{"event_id":"not-a-uuid","event_type":"x","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"x","payload":{}}`,
		`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"x","version":"1","timestamp":"2026-10-11T12:00:00-05:00","source_service":"x","payload":{}}`,
		`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"x","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"x","payload":[]}`,
		`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"x","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"x","training_center_id":"bad","payload":{}}`,
		`{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"x","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"x","correlation_id":"bad","payload":{}}`,
	}
	for _, body := range cases {
		_, err := ParseEnvelope([]byte(body))
		if !errors.Is(err, ErrInvalidEnvelope) {
			t.Errorf("ParseEnvelope(%s) error = %v, want invalid envelope", body, err)
		}
	}
}

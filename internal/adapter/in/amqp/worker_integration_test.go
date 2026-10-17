//go:build integration

package amqp

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

func TestWorkerConsumesCanonicalEventE2E(t *testing.T) {
	brokerURL, databaseURL := os.Getenv("E2E_AMQP_URL"), os.Getenv("E2E_DATABASE_URL")
	if brokerURL == "" || databaseURL == "" {
		t.Skip("E2E_AMQP_URL and E2E_DATABASE_URL are required")
	}
	conn, err := amqp091.Dial(brokerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	eventID := e2eUUID(t)
	body := []byte(fmt.Sprintf(`{"event_id":"%s","event_type":"e2e.audit.created","version":"1.0","timestamp":"2026-10-11T12:00:00Z","source_service":"e2e-producer","actor_id":null,"entity_type":"audit_test","entity_id":"%s","training_center_id":"%s","payload":{"opaque":"value"}}`, eventID, e2eUUID(t), e2eUUID(t)))
	if err := ch.PublishWithContext(context.Background(), "audit-events", "e2e.audit.created", false, false, amqp091.Publishing{ContentType: "application/json", DeliveryMode: amqp091.Persistent, Body: body}); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var records int
		// The observer deliberately has only audit_reader. A single append-only
		// audit_record row is the public E2E proof of global deduplication;
		// audit_event_seen remains an internal writer implementation detail
		// covered by the PostgreSQL persistence integration test.
		err = pool.QueryRow(context.Background(), `SELECT count(*) FROM audit.audit_record WHERE event_id=$1`, eventID).Scan(&records)
		if err == nil && records == 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("worker did not persist the canonical event")
}

func TestWorkerDeduplicatesAndRoutesInvalidEnvelopeE2E(t *testing.T) {
	brokerURL, databaseURL := os.Getenv("E2E_AMQP_URL"), os.Getenv("E2E_DATABASE_URL")
	if brokerURL == "" || databaseURL == "" {
		t.Skip("E2E_AMQP_URL and E2E_DATABASE_URL are required")
	}
	conn, err := amqp091.Dial(brokerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	eventID := e2eUUID(t)
	followUpEventID := e2eUUID(t)
	valid := []byte(fmt.Sprintf(`{"event_id":"%s","event_type":"e2e.resilience.created","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"e2e","correlation_id":"%s","payload":{}}`, eventID, e2eUUID(t)))
	invalid := []byte(`{"event_id":"bad","event_type":"e2e.invalid","version":"1","timestamp":"2026-10-11T12:00:00Z","source_service":"e2e","payload":[]}`)
	followUp := []byte(fmt.Sprintf(`{"event_id":"%s","event_type":"e2e.resilience.recovered","version":"1","timestamp":"2026-10-11T12:00:01Z","source_service":"e2e","correlation_id":"%s","payload":{}}`, followUpEventID, e2eUUID(t)))
	// E8 resilience sequence: persisted event, duplicate, invalid DLQ, then a
	// different valid event. The last delivery proves the worker recovers after
	// an invalid message rather than silently halting the queue.
	for _, body := range [][]byte{valid, valid, invalid, followUp} {
		if err := ch.PublishWithContext(context.Background(), "audit-events", "e2e.test", false, false, amqp091.Publishing{Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var records, followUpRecords int
		var dlq amqp091.Queue
		err = pool.QueryRow(context.Background(), "SELECT count(*) FROM audit.audit_record WHERE event_id=$1", eventID).Scan(&records)
		if err == nil {
			err = pool.QueryRow(context.Background(), "SELECT count(*) FROM audit.audit_record WHERE event_id=$1", followUpEventID).Scan(&followUpRecords)
		}
		dlq, _ = ch.QueueInspect("audit.dlq")
		if err == nil && records == 1 && followUpRecords == 1 && dlq.Messages >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("duplicate, DLQ, or subsequent valid-event processing did not complete")
}

func e2eUUID(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

package amqp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/observability"
	amqp091 "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Config makes the queue, retry and DLQ topology reproducible. The durable
// queue name is the documented BROKER_CONSUMER_GROUP setting.
type Config struct {
	URL, Exchange, Queue, RoutingKey   string
	MaxRetries, RetryDelayMilliseconds int
	DatabaseReady                      func() bool
}

type Consumer struct {
	config      Config
	appendAudit usecase.AppendAudit
	brokerReady atomic.Bool
}

func NewConsumer(config Config, appendAudit usecase.AppendAudit) *Consumer {
	return &Consumer{config: config, appendAudit: appendAudit}
}

// Ready reports both dependencies. The worker must not be considered ready to
// process messages when either the broker session or PostgreSQL is unavailable.
func (c *Consumer) Ready() bool {
	return c.brokerReady.Load() && (c.config.DatabaseReady == nil || c.config.DatabaseReady())
}

func (c *Consumer) Run(ctx context.Context) error {
	for {
		if err := c.consume(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("audit_worker_broker_unavailable", "error_type", "broker_connection")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(c.config.RetryDelayMilliseconds) * time.Millisecond):
			}
			continue
		}
		return nil
	}
}

func (c *Consumer) consume(ctx context.Context) error {
	c.brokerReady.Store(false)
	conn, err := amqp091.Dial(c.config.URL)
	if err != nil {
		return fmt.Errorf("connect to broker: %w", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open broker channel: %w", err)
	}
	defer ch.Close()
	if err := c.declareTopology(ch); err != nil {
		return err
	}
	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("configure worker qos: %w", err)
	}
	deliveries, err := ch.Consume(c.config.Queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume audit queue: %w", err)
	}
	c.brokerReady.Store(true)
	defer c.brokerReady.Store(false)
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("audit delivery channel closed")
			}
			c.process(ctx, ch, delivery)
		}
	}
}

func (c *Consumer) process(ctx context.Context, ch *amqp091.Channel, d amqp091.Delivery) {
	ctx = extractTraceContext(ctx, d.Headers)
	ctx, span := observability.StartAMQPConsume(ctx, c.config.Exchange, d.RoutingKey)
	defer span.End()
	started := time.Now()
	if !c.Ready() {
		if nackErr := d.Nack(false, false); nackErr != nil {
			slog.Warn("audit_worker_retry_nack_failed", "trace_id", observability.TraceID(ctx), "error_type", "broker_ack")
			return
		}
		observability.RecordWorker(ctx, "retry")
		slog.Warn("audit_worker_retry", "trace_id", observability.TraceID(ctx), "reason", "database_not_ready")
		return
	}
	envelope, err := ParseEnvelope(d.Body)
	if err != nil {
		observability.RecordError(span, err)
		c.deadLetter(ctx, ch, d, "invalid_envelope")
		return
	}
	correlationID := ""
	if envelope.CorrelationID != nil {
		correlationID = *envelope.CorrelationID
	}
	observability.SetAuditIdentifiers(span, string(envelope.EventID), correlationID)
	record, err := NewAuditRecord(envelope, time.Now().UTC())
	persisted := false
	if err == nil {
		persisted, err = c.appendAudit.ExecuteEnvelope(ctx, envelope, record)
	}
	if err == nil {
		if ackErr := d.Ack(false); ackErr != nil {
			observability.RecordError(span, ackErr)
			slog.Warn("audit_worker_ack_failed", "trace_id", observability.TraceID(ctx), "event_id", envelope.EventID, "error_type", "broker_ack")
			return
		}
		result := "duplicate"
		if persisted {
			result = "inserted"
		}
		observability.RecordWorker(ctx, result)
		slog.Info("audit_worker_result", "trace_id", observability.TraceID(ctx), "event_id", envelope.EventID, "correlation_id", correlationID, "event_type", envelope.EventType, "source_service", envelope.SourceService, "result", result, "duration_ms", time.Since(started).Milliseconds())
		return
	}
	observability.RecordError(span, err)
	if errors.Is(err, ErrInvalidEnvelope) || c.retryCount(d.Headers) >= int64(c.config.MaxRetries) {
		c.deadLetter(ctx, ch, d, "persistence_failed")
		return
	}
	// Rejecting without requeue uses the primary queue DLX. Its retry queue TTL
	// supplies bounded backoff, then routes the original body and headers back.
	if nackErr := d.Nack(false, false); nackErr != nil {
		observability.RecordError(span, nackErr)
		slog.Warn("audit_worker_retry_nack_failed", "trace_id", observability.TraceID(ctx), "event_id", envelope.EventID, "error_type", "broker_ack")
		return
	}
	observability.RecordWorker(ctx, "retry")
	slog.Warn("audit_worker_retry", "trace_id", observability.TraceID(ctx), "event_id", envelope.EventID, "correlation_id", correlationID, "event_type", envelope.EventType, "source_service", envelope.SourceService, "duration_ms", time.Since(started).Milliseconds())
}

func (c *Consumer) declareTopology(ch *amqp091.Channel) error {
	if err := ch.ExchangeDeclare(c.config.Exchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.ExchangeDeclare("audit.retry", "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.ExchangeDeclare("audit.dlx", "direct", true, false, false, false, nil); err != nil {
		return err
	}
	args := amqp091.Table{"x-dead-letter-exchange": "audit.retry", "x-dead-letter-routing-key": c.config.Queue}
	if _, err := ch.QueueDeclare(c.config.Queue, true, false, false, false, args); err != nil {
		return err
	}
	if err := ch.QueueBind(c.config.Queue, c.config.RoutingKey, c.config.Exchange, false, nil); err != nil {
		return err
	}
	retryArgs := amqp091.Table{"x-message-ttl": int32(c.config.RetryDelayMilliseconds), "x-dead-letter-exchange": c.config.Exchange, "x-dead-letter-routing-key": c.config.Queue}
	if _, err := ch.QueueDeclare(c.config.Queue+".retry", true, false, false, false, retryArgs); err != nil {
		return err
	}
	if err := ch.QueueBind(c.config.Queue+".retry", c.config.Queue, "audit.retry", false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare("audit.dlq", true, false, false, false, nil); err != nil {
		return err
	}
	return ch.QueueBind("audit.dlq", "audit.dlq", "audit.dlx", false, nil)
}

func (c *Consumer) deadLetter(ctx context.Context, ch *amqp091.Channel, d amqp091.Delivery, reason string) {
	headers := d.Headers
	if headers == nil {
		headers = amqp091.Table{}
	}
	headers["audit_failure_reason"] = reason
	if err := ch.PublishWithContext(ctx, "audit.dlx", "audit.dlq", false, false, amqp091.Publishing{Headers: headers, ContentType: d.ContentType, DeliveryMode: amqp091.Persistent, Body: d.Body, Timestamp: time.Now().UTC()}); err != nil {
		slog.Warn("audit_worker_dlq_publish_failed", "trace_id", observability.TraceID(ctx), "reason", reason, "error_type", "broker_publish")
		_ = d.Nack(false, true)
		return
	}
	if err := d.Ack(false); err != nil {
		slog.Warn("audit_worker_dlq_ack_failed", "trace_id", observability.TraceID(ctx), "reason", reason, "error_type", "broker_ack")
		return
	}
	observability.RecordWorker(ctx, "dlq")
	slog.Info("audit_worker_result", "trace_id", observability.TraceID(ctx), "result", "dlq", "reason", reason)
}

func extractTraceContext(ctx context.Context, headers amqp091.Table) context.Context {
	traceparent, ok := headers["traceparent"].(string)
	if !ok || traceparent == "" {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}

func (c *Consumer) retryCount(headers amqp091.Table) int64 {
	deaths, ok := headers["x-death"].([]interface{})
	if !ok {
		return 0
	}
	var count int64
	for _, death := range deaths {
		if values, ok := death.(amqp091.Table); ok {
			if n, ok := values["count"].(int64); ok {
				count += n
			}
		}
	}
	return count
}

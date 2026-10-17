// Package amqp is the RabbitMQ inbound adapter for audit-worker.
package amqp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

var ErrInvalidEnvelope = errors.New("invalid event envelope")

// ParseEnvelope accepts only the canonical envelope. Payload stays opaque after
// verifying that it is a JSON object; business rules never enter audit-worker.
func ParseEnvelope(body []byte) (out.EventEnvelope, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return out.EventEnvelope{}, invalid("body is not a JSON object")
	}
	stringField := func(name string, required bool) (string, error) {
		raw, ok := fields[name]
		if !ok || string(raw) == "null" {
			if required {
				return "", invalid("missing " + name)
			}
			return "", nil
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
			return "", invalid("invalid " + name)
		}
		return value, nil
	}
	eventIDText, err := stringField("event_id", true)
	if err != nil {
		return out.EventEnvelope{}, err
	}
	eventID, err := model.ParseUUID(eventIDText)
	if err != nil {
		return out.EventEnvelope{}, invalid("invalid event_id")
	}
	eventType, err := stringField("event_type", true)
	if err != nil {
		return out.EventEnvelope{}, err
	}
	version, err := stringField("version", true)
	if err != nil {
		return out.EventEnvelope{}, err
	}
	timestampText, err := stringField("timestamp", true)
	if err != nil {
		return out.EventEnvelope{}, err
	}
	if !strings.HasSuffix(timestampText, "Z") {
		return out.EventEnvelope{}, invalid("timestamp must be UTC")
	}
	timestamp, err := time.Parse(time.RFC3339, timestampText)
	if err != nil {
		return out.EventEnvelope{}, invalid("invalid timestamp")
	}
	source, err := stringField("source_service", true)
	if err != nil {
		return out.EventEnvelope{}, err
	}
	correlationID, err := optionalString(fields, "correlation_id")
	if err != nil {
		return out.EventEnvelope{}, err
	}
	if correlationID != nil {
		if _, err := model.ParseUUID(*correlationID); err != nil {
			return out.EventEnvelope{}, invalid("invalid correlation_id")
		}
	}
	payload, ok := fields["payload"]
	if !ok || len(payload) == 0 || string(payload) == "null" {
		return out.EventEnvelope{}, invalid("missing payload")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return out.EventEnvelope{}, invalid("payload is not an object")
	}
	actor, err := optionalUUID(fields, "actor_id")
	if err != nil {
		return out.EventEnvelope{}, err
	}
	entityID, err := optionalUUID(fields, "entity_id")
	if err != nil {
		return out.EventEnvelope{}, err
	}
	entityType, err := optionalString(fields, "entity_type")
	if err != nil {
		return out.EventEnvelope{}, err
	}
	trainingCenter, err := optionalString(fields, "training_center_id")
	if err != nil {
		return out.EventEnvelope{}, err
	}
	if trainingCenter != nil {
		if _, err := model.ParseUUID(*trainingCenter); err != nil {
			return out.EventEnvelope{}, invalid("invalid training_center_id")
		}
	}
	return out.EventEnvelope{EventID: eventID, EventType: eventType, Version: version, Timestamp: timestamp.UTC(), SourceService: source, CorrelationID: correlationID, ActorID: actor, EntityType: entityType, EntityID: entityID, Payload: payload, TrainingCenterID: trainingCenter}, nil
}

func NewAuditRecord(e out.EventEnvelope, receivedAt time.Time) (model.AuditRecord, error) {
	id, err := newUUID()
	if err != nil {
		return model.AuditRecord{}, err
	}
	occurred := e.Timestamp.UTC()
	return model.AuditRecord{ID: id, EventID: e.EventID, EventType: e.EventType, SourceService: e.SourceService, ActorID: e.ActorID, EntityType: e.EntityType, EntityID: e.EntityID, Payload: e.Payload, EventOccurredAt: &occurred, ReceivedAt: receivedAt.UTC()}, nil
}

func optionalUUID(fields map[string]json.RawMessage, name string) (*model.UUID, error) {
	value, err := optionalString(fields, name)
	if err != nil || value == nil {
		return nil, err
	}
	id, err := model.ParseUUID(*value)
	if err != nil {
		return nil, invalid("invalid " + name)
	}
	return &id, nil
}
func optionalString(fields map[string]json.RawMessage, name string) (*string, error) {
	raw, ok := fields[name]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return nil, invalid("invalid " + name)
	}
	return &value, nil
}
func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidEnvelope, reason) }
func newUUID() (model.UUID, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b)
	return model.UUID(encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]), nil
}

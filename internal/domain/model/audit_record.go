package model

import (
	"errors"
	"time"
)

type UUID string

func ParseUUID(v string) (UUID, error) {
	if len(v) != 36 {
		return "", errors.New("invalid UUID")
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", errors.New("invalid UUID")
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return "", errors.New("invalid UUID")
		}
	}
	return UUID(v), nil
}

// AuditRecord mirrors audit.audit_record; it intentionally excludes the internal scope projection.
type AuditRecord struct {
	ID              UUID
	EventID         UUID
	EventType       string
	SourceService   string
	ActorID         *UUID
	EntityType      *string
	EntityID        *UUID
	Payload         []byte
	EventOccurredAt *time.Time
	ReceivedAt      time.Time
}

func (r AuditRecord) Validate() error {
	if r.ID == "" || r.EventID == "" || r.EventType == "" || r.SourceService == "" || len(r.Payload) == 0 || r.ReceivedAt.IsZero() {
		return errors.New("audit record has required fields missing")
	}
	if r.ReceivedAt.Location() != time.UTC {
		return errors.New("received_at must be UTC")
	}
	if r.EventOccurredAt != nil && r.EventOccurredAt.Location() != time.UTC {
		return errors.New("event_occurred_at must be UTC")
	}
	return nil
}

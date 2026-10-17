package out

import (
	"context"
	"errors"
	"fmt"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
	"time"
)

var ErrAuditRecordNotFound = errors.New("audit record not found")

type EventConsumer interface {
	Consume(context.Context, func(context.Context, EventEnvelope) error) error
}
type EventEnvelope struct {
	EventID          model.UUID
	EventType        string
	Version          string
	Timestamp        time.Time
	SourceService    string
	CorrelationID    *string
	ActorID          *model.UUID
	EntityType       *string
	EntityID         *model.UUID
	Payload          []byte
	TrainingCenterID *string
}

// ValidTrainingCenterID rejects malformed scope before any idempotency reservation.
func (e EventEnvelope) ValidTrainingCenterID() (*model.UUID, error) {
	if e.TrainingCenterID == nil {
		return nil, nil
	}
	id, err := model.ParseUUID(*e.TrainingCenterID)
	if err != nil {
		return nil, fmt.Errorf("invalid training_center_id")
	}
	return &id, nil
}

type AuditAppender interface {
	AppendIfUnseen(context.Context, model.AuditRecord, *model.UUID) (bool, error)
}
type AuditReader interface {
	FindByID(context.Context, model.UUID) (model.AuditRecord, error)
}

// AuditQueryReader is deliberately read-only; the query API is never given an append port.
type AuditQueryReader interface {
	List(context.Context, model.AuditFilter, int) (model.AuditPage, error)
	Get(context.Context, model.UUID, *model.UUID) (model.AuditRecord, error)
	OpenExport(context.Context, model.AuditFilter) (AuditRecordIterator, error)
}

// AuditRecordIterator keeps export consumption incremental.
type AuditRecordIterator interface {
	Next() (model.AuditRecord, bool, error)
	Close()
}

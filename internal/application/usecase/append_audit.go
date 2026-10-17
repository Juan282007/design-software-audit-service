package usecase

import (
	"context"
	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type AppendAudit struct{ Store out.AuditAppender }

func (u AppendAudit) Execute(ctx context.Context, r model.AuditRecord, scope *model.UUID) (bool, error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	return u.Store.AppendIfUnseen(ctx, r, scope)
}

// ExecuteEnvelope is the worker-facing boundary. Scope validation happens
// before the append port is reached, so malformed messages cannot reserve an
// event_id or create an audit record. AMQP/DLQ handling is delivered in HU-AUD-002.
func (u AppendAudit) ExecuteEnvelope(ctx context.Context, envelope out.EventEnvelope, r model.AuditRecord) (bool, error) {
	scope, err := envelope.ValidTrainingCenterID()
	if err != nil {
		return false, err
	}
	return u.Execute(ctx, r, scope)
}

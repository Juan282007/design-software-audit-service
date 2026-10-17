package usecase

import (
	"context"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

// ListAudit and GetAudit keep HTTP and PostgreSQL details at their respective edges.
type ListAudit struct{ Reader out.AuditQueryReader }

func (u ListAudit) Execute(ctx context.Context, filter model.AuditFilter, limit int) (model.AuditPage, error) {
	return u.Reader.List(ctx, filter, limit)
}

type GetAudit struct{ Reader out.AuditQueryReader }

func (u GetAudit) Execute(ctx context.Context, id model.UUID, trainingCenterID *model.UUID) (model.AuditRecord, error) {
	return u.Reader.Get(ctx, id, trainingCenterID)
}

// ExportAudit shares the list filter and read port; representation belongs to HTTP.
type ExportAudit struct{ Reader out.AuditQueryReader }

func (u ExportAudit) Execute(ctx context.Context, filter model.AuditFilter) (out.AuditRecordIterator, error) {
	return u.Reader.OpenExport(ctx, filter)
}

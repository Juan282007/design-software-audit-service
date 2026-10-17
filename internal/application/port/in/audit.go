// Package in defines the use cases exposed by the application core.
package in

import (
	"context"

	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type AppendAuditRecord interface {
	Execute(context.Context, model.AuditRecord, *model.UUID) (bool, error)
}

type FindAuditRecord interface {
	FindByID(context.Context, model.UUID) (model.AuditRecord, error)
}

type ListAuditRecords interface {
	Execute(context.Context, model.AuditFilter, int) (model.AuditPage, error)
}

type GetAuditRecord interface {
	Execute(context.Context, model.UUID, *model.UUID) (model.AuditRecord, error)
}

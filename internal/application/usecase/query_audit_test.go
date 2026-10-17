package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type querySpy struct {
	listFilter   model.AuditFilter
	listLimit    int
	getID        model.UUID
	getScope     *model.UUID
	exportFilter model.AuditFilter
	iterator     out.AuditRecordIterator
}

func (s *querySpy) List(_ context.Context, filter model.AuditFilter, limit int) (model.AuditPage, error) {
	s.listFilter, s.listLimit = filter, limit
	return model.AuditPage{}, nil
}
func (s *querySpy) Get(_ context.Context, id model.UUID, scope *model.UUID) (model.AuditRecord, error) {
	s.getID, s.getScope = id, scope
	return model.AuditRecord{ID: id}, nil
}
func (s *querySpy) OpenExport(_ context.Context, filter model.AuditFilter) (out.AuditRecordIterator, error) {
	s.exportFilter = filter
	return s.iterator, nil
}

func TestQueryUseCasesDelegateToReadPort(t *testing.T) {
	id, _ := model.ParseUUID("11111111-1111-4111-8111-111111111111")
	center, _ := model.ParseUUID("22222222-2222-4222-8222-222222222222")
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	filter := model.AuditFilter{From: &from}
	spy := &querySpy{}
	if _, err := (ListAudit{Reader: spy}).Execute(context.Background(), filter, 25); err != nil || spy.listLimit != 25 || spy.listFilter.From != &from {
		t.Fatalf("list delegation err=%v filter=%#v limit=%d", err, spy.listFilter, spy.listLimit)
	}
	record, err := (GetAudit{Reader: spy}).Execute(context.Background(), id, &center)
	if err != nil || record.ID != id || spy.getID != id || spy.getScope != &center {
		t.Fatalf("get delegation record=%#v err=%v", record, err)
	}
	iterator, err := (ExportAudit{Reader: spy}).Execute(context.Background(), filter)
	if err != nil || iterator != nil || spy.exportFilter.From != &from {
		t.Fatalf("export delegation iterator=%v err=%v filter=%#v", iterator, err, spy.exportFilter)
	}
}

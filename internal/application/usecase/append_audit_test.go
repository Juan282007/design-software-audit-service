package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type appenderSpy struct {
	called   bool
	inserted bool
	err      error
}

func (s *appenderSpy) AppendIfUnseen(_ context.Context, _ model.AuditRecord, _ *model.UUID) (bool, error) {
	s.called = true
	return s.inserted, s.err
}

func TestAppendAuditValidatesBeforePersisting(t *testing.T) {
	spy := &appenderSpy{inserted: true}
	ok, err := (AppendAudit{Store: spy}).Execute(context.Background(), model.AuditRecord{}, nil)
	if err == nil || ok || spy.called {
		t.Fatal("invalid record must not reach persistence")
	}
}

func TestAppendAuditDelegatesValidRecord(t *testing.T) {
	id, _ := model.ParseUUID("2f1eac90-f5c5-4c2d-8be4-2114cd6de3cb")
	spy := &appenderSpy{err: errors.New("storage failure")}
	_, err := (AppendAudit{Store: spy}).Execute(context.Background(), model.AuditRecord{ID: id, EventID: id, EventType: "x", SourceService: "s", Payload: []byte(`{}`), ReceivedAt: time.Now().UTC()}, nil)
	if !spy.called || !errors.Is(err, spy.err) {
		t.Fatalf("called=%v err=%v", spy.called, err)
	}
}

func TestAppendEnvelopeRejectsInvalidScopeBeforePersistence(t *testing.T) {
	bad := "not-a-uuid"
	spy := &appenderSpy{inserted: true}
	id, _ := model.ParseUUID("2f1eac90-f5c5-4c2d-8be4-2114cd6de3cb")
	_, err := (AppendAudit{Store: spy}).ExecuteEnvelope(context.Background(), out.EventEnvelope{TrainingCenterID: &bad}, validRecord(id))
	if err == nil || spy.called {
		t.Fatal("invalid scope must not reach persistence")
	}
}

func TestAppendEnvelopePassesValidOrAbsentScope(t *testing.T) {
	id, _ := model.ParseUUID("2f1eac90-f5c5-4c2d-8be4-2114cd6de3cb")
	valid := string(id)
	for _, scope := range []*string{nil, &valid} {
		spy := &appenderSpy{inserted: true}
		ok, err := (AppendAudit{Store: spy}).ExecuteEnvelope(context.Background(), out.EventEnvelope{TrainingCenterID: scope}, validRecord(id))
		if err != nil || !ok || !spy.called {
			t.Fatalf("scope=%v ok=%v err=%v called=%v", scope, ok, err, spy.called)
		}
	}
}

func validRecord(id model.UUID) model.AuditRecord {
	return model.AuditRecord{ID: id, EventID: id, EventType: "x", SourceService: "s", Payload: []byte(`{}`), ReceivedAt: time.Now().UTC()}
}

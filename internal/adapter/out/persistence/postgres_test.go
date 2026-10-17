package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/domain/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAuditWhereBuildsOnlyBoundFiltersAndSharedScope(t *testing.T) {
	actor, _ := model.ParseUUID("11111111-1111-4111-8111-111111111111")
	entity, _ := model.ParseUUID("22222222-2222-4222-8222-222222222222")
	center, _ := model.ParseUUID("33333333-3333-4333-8333-333333333333")
	cursorID, _ := model.ParseUUID("44444444-4444-4444-8444-444444444444")
	text := "value"
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	filter := model.AuditFilter{
		ActorID: &actor, SourceService: &text, EventType: &text, EntityType: &text, EntityID: &entity,
		From: &from, To: &to, TrainingCenterID: &center,
		After: &model.AuditCursor{ReceivedAt: from, ID: cursorID},
	}
	args, where := auditWhere(filter)
	if len(args) != 10 || len(where) != 9 {
		t.Fatalf("args=%d where=%d: %#v", len(args), len(where), where)
	}
	for _, condition := range where {
		if condition == "" || condition[0] == ';' {
			t.Fatalf("unsafe condition %q", condition)
		}
	}
	if where[7] != "training_center_id=$8" || where[8] != "(received_at, id) < ($9, $10)" {
		t.Fatalf("scope/cursor predicates=%#v", where)
	}
}

func TestAuditWhereOmitsEveryOptionalPredicate(t *testing.T) {
	args, where := auditWhere(model.AuditFilter{})
	if len(args) != 0 || len(where) != 0 {
		t.Fatalf("args=%#v where=%#v", args, where)
	}
}

func TestNewPoolRejectsMalformedConnectionURL(t *testing.T) {
	if _, err := NewPool(context.Background(), "%%%not-a-url"); err == nil {
		t.Fatal("malformed database URL was accepted")
	}
}

func TestAuditRecordRowsIteratesAndPropagatesRowErrors(t *testing.T) {
	id, _ := model.ParseUUID("11111111-1111-4111-8111-111111111111")
	eventID, _ := model.ParseUUID("22222222-2222-4222-8222-222222222222")
	rows := &rowsSpy{id: id, eventID: eventID, next: true}
	iterator := auditRecordRows{rows: rows}
	record, ok, err := iterator.Next()
	if err != nil || !ok || record.ID != id || record.EventID != eventID || len(record.Payload) == 0 {
		t.Fatalf("record=%#v ok=%v err=%v", record, ok, err)
	}
	_, ok, err = iterator.Next()
	if err != nil || ok {
		t.Fatalf("end ok=%v err=%v", ok, err)
	}
	iterator.Close()
	if !rows.closed {
		t.Fatal("rows were not closed")
	}
	rows = &rowsSpy{err: errors.New("row failure")}
	_, ok, err = (&auditRecordRows{rows: rows}).Next()
	if ok || !errors.Is(err, rows.err) {
		t.Fatalf("error row ok=%v err=%v", ok, err)
	}
}

type rowsSpy struct {
	id, eventID model.UUID
	next        bool
	closed      bool
	err         error
}

func (r *rowsSpy) Close()                                       { r.closed = true }
func (r *rowsSpy) Err() error                                   { return r.err }
func (r *rowsSpy) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *rowsSpy) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *rowsSpy) Next() bool {
	if !r.next {
		return false
	}
	r.next = false
	return true
}
func (r *rowsSpy) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	occurred := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	*dest[0].(*model.UUID) = r.id
	*dest[1].(*model.UUID) = r.eventID
	*dest[2].(*string) = "event.type"
	*dest[3].(*string) = "source"
	*dest[4].(**model.UUID) = nil
	*dest[5].(**string) = nil
	*dest[6].(**model.UUID) = nil
	*dest[7].(*[]byte) = []byte(`{"opaque":true}`)
	*dest[8].(**time.Time) = &occurred
	*dest[9].(*time.Time) = occurred
	return nil
}
func (r *rowsSpy) Values() ([]any, error) { return nil, r.err }
func (r *rowsSpy) RawValues() [][]byte    { return nil }
func (r *rowsSpy) Conn() *pgx.Conn        { return nil }

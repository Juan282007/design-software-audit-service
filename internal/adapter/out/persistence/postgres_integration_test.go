//go:build integration

package persistence

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

// Run with real PostgreSQL 16 migrated by design-software-audit-db:
// TEST_AUDIT_READER_DSN=... TEST_AUDIT_WRITER_DSN=... go test -tags=integration ./internal/adapter/out/persistence
func TestAuditRolePrivilegesAgainstPostgreSQL(t *testing.T) {
	readerDSN, writerDSN := os.Getenv("TEST_AUDIT_READER_DSN"), os.Getenv("TEST_AUDIT_WRITER_DSN")
	if readerDSN == "" || writerDSN == "" {
		t.Skip("requires TEST_AUDIT_READER_DSN and TEST_AUDIT_WRITER_DSN")
	}
	ctx := context.Background()
	writer, err := NewPool(ctx, writerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := NewPool(ctx, readerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var currentUser string
	var canInsertSeen bool
	if err := writer.QueryRow(ctx, "SELECT current_user, has_table_privilege(current_user, 'audit.audit_event_seen', 'INSERT')").Scan(&currentUser, &canInsertSeen); err != nil {
		t.Fatalf("writer session check: %v", err)
	}
	if !canInsertSeen {
		t.Fatalf("writer session %q lacks INSERT on audit_event_seen", currentUser)
	}
	t.Logf("writer session %q has INSERT on audit_event_seen", currentUser)
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatalf("writer transaction: %v", err)
	}
	var txUser string
	var txCanInsert bool
	if err := tx.QueryRow(ctx, "SELECT current_user, has_table_privilege(current_user, 'audit.audit_event_seen', 'INSERT')").Scan(&txUser, &txCanInsert); err != nil {
		t.Fatalf("writer transaction check: %v", err)
	}
	t.Logf("writer transaction session %q has INSERT=%v", txUser, txCanInsert)
	if !txCanInsert {
		t.Fatalf("writer transaction session %q lacks INSERT", txUser)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO audit.audit_event_seen(event_id) VALUES($1)", "77777777-7777-4777-8777-777777777777"); err != nil {
		t.Fatalf("writer transaction insert: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("writer transaction rollback: %v", err)
	}
	id, eventID := integrationUUID(t), integrationUUID(t)
	store := AuditStore{Pool: writer}
	inserted, err := store.AppendIfUnseen(ctx, model.AuditRecord{ID: id, EventID: eventID, EventType: "integration.test", SourceService: "audit-service", Payload: []byte(`{}`), ReceivedAt: time.Now().UTC()}, nil)
	if err != nil || !inserted {
		t.Fatalf("writer append: inserted=%v err=%v", inserted, err)
	}
	duplicate, err := store.AppendIfUnseen(ctx, model.AuditRecord{ID: id, EventID: eventID, EventType: "integration.test", SourceService: "audit-service", Payload: []byte(`{}`), ReceivedAt: time.Now().UTC()}, nil)
	if err != nil || duplicate {
		t.Fatalf("dedup result: inserted=%v err=%v", duplicate, err)
	}
	if stored, err := (AuditStore{Pool: reader}).FindByID(ctx, id); err != nil || stored.EventID != eventID {
		t.Fatalf("reader read: record=%#v err=%v", stored, err)
	}
	var count int
	if err := reader.QueryRow(ctx, "SELECT count(*) FROM audit.audit_record").Scan(&count); err != nil {
		t.Fatalf("reader SELECT: %v", err)
	}
	if _, err := reader.Exec(ctx, "INSERT INTO audit.audit_event_seen(event_id) VALUES(gen_random_uuid())"); err == nil {
		t.Fatal("reader unexpectedly inserted")
	}
	if _, err := writer.Exec(ctx, "UPDATE audit.audit_record SET event_type='forbidden' WHERE id=$1", id); err == nil {
		t.Fatal("writer unexpectedly updated")
	}
	if _, err := writer.Exec(ctx, "DELETE FROM audit.audit_record WHERE id=$1", id); err == nil {
		t.Fatal("writer unexpectedly deleted")
	}
	if _, err := writer.Exec(ctx, "TRUNCATE audit.audit_record"); err == nil {
		t.Fatal("writer unexpectedly truncated")
	}
}

//go:build integration

package persistence

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAuditQueriesAgainstPartitionedPostgreSQL is the reproducible E9 persistence
// evidence. It needs a migrated PostgreSQL 16 and separate writer/reader logins.
func TestAuditQueriesAgainstPartitionedPostgreSQL(t *testing.T) {
	readerDSN, writerDSN := os.Getenv("TEST_AUDIT_READER_DSN"), os.Getenv("TEST_AUDIT_WRITER_DSN")
	if readerDSN == "" || writerDSN == "" {
		t.Skip("requires TEST_AUDIT_READER_DSN and TEST_AUDIT_WRITER_DSN")
	}
	ctx := context.Background()
	writerPool, err := NewPool(ctx, writerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer writerPool.Close()
	readerPool, err := NewPool(ctx, readerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer readerPool.Close()
	writer, reader := AuditStore{Pool: writerPool}, AuditStore{Pool: readerPool}

	centerA, centerB := integrationUUID(t), integrationUUID(t)
	runType := "E9_" + string(integrationUUID(t))
	seededA := make(map[model.UUID]struct{})
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 55; i++ {
		id := seedRecord(t, ctx, writer, &centerA, base.Add(-time.Duration(i)*time.Second), runType)
		seededA[id] = struct{}{}
	}
	monthlyID := seedRecord(t, ctx, writer, &centerA, time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC), runType)
	seededA[monthlyID] = struct{}{}
	centerBID := seedRecord(t, ctx, writer, &centerB, base.Add(-time.Hour), runType)
	nullScopeID := seedRecord(t, ctx, writer, nil, base.Add(-2*time.Hour), runType)

	first, err := reader.List(ctx, model.AuditFilter{TrainingCenterID: &centerA, EventType: &runType}, 50)
	if err != nil || !first.HasMore || len(first.Records) != 50 {
		t.Fatalf("first page len=%d hasMore=%v err=%v", len(first.Records), first.HasMore, err)
	}
	seen := make(map[model.UUID]struct{}, len(first.Records))
	for _, record := range first.Records {
		if _, ok := seededA[record.ID]; !ok {
			t.Fatalf("center-scoped list leaked record %s", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	last := first.Records[len(first.Records)-1]
	second, err := reader.List(ctx, model.AuditFilter{TrainingCenterID: &centerA, EventType: &runType, After: &model.AuditCursor{ReceivedAt: last.ReceivedAt, ID: last.ID}}, 50)
	if err != nil || len(second.Records) == 0 {
		t.Fatalf("second page len=%d err=%v", len(second.Records), err)
	}
	for _, record := range second.Records {
		if _, duplicate := seen[record.ID]; duplicate {
			t.Fatalf("duplicate record %s across cursor pages", record.ID)
		}
		if _, ok := seededA[record.ID]; !ok {
			t.Fatalf("center-scoped second page leaked record %s", record.ID)
		}
	}

	monthlyFrom := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	monthlyTo := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	monthly, err := reader.List(ctx, model.AuditFilter{TrainingCenterID: &centerA, EventType: &runType, From: &monthlyFrom, To: &monthlyTo}, 50)
	if err != nil || len(monthly.Records) != 1 || monthly.Records[0].ID != monthlyID {
		t.Fatalf("monthly partition result=%#v err=%v", monthly.Records, err)
	}
	if _, err = reader.Get(ctx, centerBID, &centerA); !errors.Is(err, out.ErrAuditRecordNotFound) {
		t.Fatalf("out-of-scope detail err=%v, want not found", err)
	}
	global, err := reader.List(ctx, model.AuditFilter{EventType: &runType}, 100)
	if err != nil {
		t.Fatal(err)
	}
	globalIDs := make(map[model.UUID]struct{}, len(global.Records))
	for _, record := range global.Records {
		globalIDs[record.ID] = struct{}{}
	}
	for _, expected := range []model.UUID{centerBID, nullScopeID, monthlyID} {
		if _, ok := globalIDs[expected]; !ok {
			t.Fatalf("global list did not return %s", expected)
		}
	}

	// HU-AUD-004: the full export uses the same scope/filter rule, delivers
	// payloads, and advances records in the documented deterministic order.
	iterator, err := reader.OpenExport(ctx, model.AuditFilter{TrainingCenterID: &centerA, EventType: &runType})
	if err != nil {
		t.Fatal(err)
	}
	defer iterator.Close()
	exported := make(map[model.UUID]struct{}, len(seededA))
	var previous *model.AuditRecord
	for {
		record, ok, nextErr := iterator.Next()
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if !ok {
			break
		}
		if _, allowed := seededA[record.ID]; !allowed {
			t.Fatalf("center-scoped export leaked record %s", record.ID)
		}
		if len(record.Payload) == 0 {
			t.Fatalf("export omitted payload for %s", record.ID)
		}
		if previous != nil && (previous.ReceivedAt.Before(record.ReceivedAt) || (previous.ReceivedAt.Equal(record.ReceivedAt) && previous.ID < record.ID)) {
			t.Fatalf("export order is not received_at/id descending: previous=%s current=%s", previous.ID, record.ID)
		}
		copy := record
		previous = &copy
		exported[record.ID] = struct{}{}
	}
	if len(exported) != len(seededA) {
		t.Fatalf("exported=%d expected=%d", len(exported), len(seededA))
	}

	assertReaderReadOnly(t, ctx, readerPool)
	logApplicableIndexPlan(t, ctx, readerPool, centerA)
}

func seedRecord(t *testing.T, ctx context.Context, writer AuditStore, center *model.UUID, receivedAt time.Time, eventType string) model.UUID {
	t.Helper()
	id, eventID := integrationUUID(t), integrationUUID(t)
	inserted, err := writer.AppendIfUnseen(ctx, model.AuditRecord{ID: id, EventID: eventID, EventType: eventType, SourceService: "audit-integration", Payload: []byte(`{"test":"hu-aud-003"}`), ReceivedAt: receivedAt}, center)
	if err != nil || !inserted {
		t.Fatalf("seed event=%s inserted=%v err=%v", eventID, inserted, err)
	}
	return id
}

func assertReaderReadOnly(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit.audit_record").Scan(&count); err != nil {
		t.Fatalf("reader SELECT: %v", err)
	}
	_, err := pool.Exec(ctx, "INSERT INTO audit.audit_record DEFAULT VALUES")
	expectPermissionDenied(t, err, "INSERT")
	_, err = pool.Exec(ctx, "UPDATE audit.audit_record SET event_type=event_type WHERE false")
	expectPermissionDenied(t, err, "UPDATE")
	_, err = pool.Exec(ctx, "DELETE FROM audit.audit_record WHERE false")
	expectPermissionDenied(t, err, "DELETE")
	_, err = pool.Exec(ctx, "TRUNCATE audit.audit_record")
	expectPermissionDenied(t, err, "TRUNCATE")
}

func logApplicableIndexPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, center model.UUID) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN (COSTS false) SELECT id,event_id,event_type,source_service,actor_id,entity_type,entity_id,event_occurred_at,received_at FROM audit.audit_record WHERE training_center_id=$1 ORDER BY received_at DESC, id DESC LIMIT 50`, center)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := make([]string, 0)
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	t.Logf("representative list EXPLAIN:\n%s", joined)
	if !strings.Contains(joined, "Index") {
		t.Fatalf("expected an applicable index in plan:\n%s", joined)
	}
}

func integrationUUID(t *testing.T) model.UUID {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	id, err := model.ParseUUID(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func expectPermissionDenied(t *testing.T, err error, operation string) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("%s err=%v, want permission denied", operation, err)
	}
}

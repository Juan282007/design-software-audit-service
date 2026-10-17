// Package persistence contains PostgreSQL adapters. SQL behavior is exercised
// against PostgreSQL 16 once the audit-db migrations are available.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
	"github.com/code-sena/design-software-audit-service/internal/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, databaseURL)
}

// Ping is used by startup and the continuous readiness check. It has a safe
// SQL span without recording DSNs, parameters or other connection details.
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, span := observability.StartSQL(ctx, "PING")
	defer span.End()
	err := pool.Ping(ctx)
	observability.RecordError(span, err)
	return err
}

// AuditStore implements the transactional, global event_id deduplication flow.
// training_center_id is deliberately a storage-only projection, not AuditRecord data.
type AuditStore struct{ Pool *pgxpool.Pool }

func (s AuditStore) AppendIfUnseen(ctx context.Context, record model.AuditRecord, trainingCenterID *model.UUID) (inserted bool, err error) {
	ctx, span := observability.StartSQL(ctx, "INSERT")
	defer func() {
		observability.RecordError(span, err)
		span.End()
	}()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO audit.audit_event_seen (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING`, record.EventID)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() == 0 {
		return false, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit.audit_record
 (id,event_id,event_type,source_service,actor_id,entity_type,entity_id,payload,event_occurred_at,received_at,training_center_id)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		record.ID, record.EventID, record.EventType, record.SourceService, record.ActorID, record.EntityType, record.EntityID, record.Payload, record.EventOccurredAt, record.ReceivedAt, trainingCenterID)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

var ErrNotFound = errors.New("audit record not found")

func (s AuditStore) FindByID(ctx context.Context, id model.UUID) (record model.AuditRecord, err error) {
	ctx, span := observability.StartSQL(ctx, "SELECT")
	defer func() {
		observability.RecordError(span, err)
		span.End()
	}()
	var r model.AuditRecord
	err = s.Pool.QueryRow(ctx, `SELECT id,event_id,event_type,source_service,actor_id,entity_type,entity_id,payload,event_occurred_at,received_at FROM audit.audit_record WHERE id=$1`, id).
		Scan(&r.ID, &r.EventID, &r.EventType, &r.SourceService, &r.ActorID, &r.EntityType, &r.EntityID, &r.Payload, &r.EventOccurredAt, &r.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.AuditRecord{}, ErrNotFound
	}
	return r, err
}

// List projects no payload, applies all filters with parameters, and fetches one extra row.
func (s AuditStore) List(ctx context.Context, filter model.AuditFilter, limit int) (page model.AuditPage, err error) {
	ctx, span := observability.StartSQL(ctx, "SELECT")
	defer func() {
		observability.RecordError(span, err)
		span.End()
	}()
	args, where := auditWhere(filter)
	args = append(args, limit+1)
	query := `SELECT id,event_id,event_type,source_service,actor_id,entity_type,entity_id,event_occurred_at,received_at FROM audit.audit_record`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY received_at DESC, id DESC LIMIT $%d", len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return model.AuditPage{}, err
	}
	defer rows.Close()
	page = model.AuditPage{Records: make([]model.AuditRecordSummary, 0, limit)}
	for rows.Next() {
		var r model.AuditRecordSummary
		if err = rows.Scan(&r.ID, &r.EventID, &r.EventType, &r.SourceService, &r.ActorID, &r.EntityType, &r.EntityID, &r.EventOccurredAt, &r.ReceivedAt); err != nil {
			return model.AuditPage{}, err
		}
		page.Records = append(page.Records, r)
	}
	if err = rows.Err(); err != nil {
		return model.AuditPage{}, err
	}
	if len(page.Records) > limit {
		page.HasMore = true
		page.Records = page.Records[:limit]
	}
	return page, nil
}

// OpenExport opens the database stream before HTTP sends response bytes. pgx
// advances rows incrementally and uses the request context for cancellation.
func (s AuditStore) OpenExport(ctx context.Context, filter model.AuditFilter) (iterator out.AuditRecordIterator, err error) {
	ctx, span := observability.StartSQL(ctx, "SELECT")
	defer func() {
		observability.RecordError(span, err)
		span.End()
	}()
	args, where := auditWhere(filter)
	query := `SELECT id,event_id,event_type,source_service,actor_id,entity_type,entity_id,payload,event_occurred_at,received_at FROM audit.audit_record`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY received_at DESC, id DESC"
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &auditRecordRows{rows: rows}, nil
}

type auditRecordRows struct{ rows pgx.Rows }

func (r *auditRecordRows) Next() (model.AuditRecord, bool, error) {
	if !r.rows.Next() {
		return model.AuditRecord{}, false, r.rows.Err()
	}
	var record model.AuditRecord
	if err := r.rows.Scan(&record.ID, &record.EventID, &record.EventType, &record.SourceService, &record.ActorID, &record.EntityType, &record.EntityID, &record.Payload, &record.EventOccurredAt, &record.ReceivedAt); err != nil {
		return model.AuditRecord{}, false, err
	}
	return record, true, nil
}

func (r *auditRecordRows) Close() { r.rows.Close() }

// auditWhere is shared by list and export to ensure a single filter and scope
// interpretation. It builds only fixed SQL fragments and bound parameters.
func auditWhere(filter model.AuditFilter) ([]any, []string) {
	args := make([]any, 0, 10)
	where := make([]string, 0, 10)
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if filter.ActorID != nil {
		add("actor_id=$%d", *filter.ActorID)
	}
	if filter.SourceService != nil {
		add("source_service=$%d", *filter.SourceService)
	}
	if filter.EventType != nil {
		add("event_type=$%d", *filter.EventType)
	}
	if filter.EntityType != nil {
		add("entity_type=$%d", *filter.EntityType)
	}
	if filter.EntityID != nil {
		add("entity_id=$%d", *filter.EntityID)
	}
	if filter.From != nil {
		add("received_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("received_at < $%d", *filter.To)
	}
	if filter.TrainingCenterID != nil {
		add("training_center_id=$%d", *filter.TrainingCenterID)
	}
	if filter.After != nil {
		args = append(args, filter.After.ReceivedAt, filter.After.ID)
		where = append(where, fmt.Sprintf("(received_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	return args, where
}

// Get uses id and optional scope in one query, preventing cross-center existence disclosure.
func (s AuditStore) Get(ctx context.Context, id model.UUID, trainingCenterID *model.UUID) (record model.AuditRecord, err error) {
	ctx, span := observability.StartSQL(ctx, "SELECT")
	defer func() {
		observability.RecordError(span, err)
		span.End()
	}()
	query := `SELECT id,event_id,event_type,source_service,actor_id,entity_type,entity_id,payload,event_occurred_at,received_at FROM audit.audit_record WHERE id=$1`
	args := []any{id}
	if trainingCenterID != nil {
		query += " AND training_center_id=$2"
		args = append(args, *trainingCenterID)
	}
	var r model.AuditRecord
	err = s.Pool.QueryRow(ctx, query, args...).Scan(&r.ID, &r.EventID, &r.EventType, &r.SourceService, &r.ActorID, &r.EntityType, &r.EntityID, &r.Payload, &r.EventOccurredAt, &r.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.AuditRecord{}, out.ErrAuditRecordNotFound
	}
	return r, err
}

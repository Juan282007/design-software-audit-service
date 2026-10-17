package model

import "time"

// AuditRecordSummary is the safe list projection: payload is intentionally absent.
type AuditRecordSummary struct {
	ID              UUID
	EventID         UUID
	EventType       string
	SourceService   string
	ActorID         *UUID
	EntityType      *string
	EntityID        *UUID
	EventOccurredAt *time.Time
	ReceivedAt      time.Time
}

// AuditFilter contains only contract-declared filters. Nil values are omitted.
type AuditFilter struct {
	ActorID, EntityID                    *UUID
	SourceService, EventType, EntityType *string
	From, To                             *time.Time
	After                                *AuditCursor
	TrainingCenterID                     *UUID
}

// AuditCursor is the stable keyset position, never supplied directly to SQL by a client.
type AuditCursor struct {
	ReceivedAt time.Time
	ID         UUID
}

type AuditPage struct {
	Records []AuditRecordSummary
	HasMore bool
}

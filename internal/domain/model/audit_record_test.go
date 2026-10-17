package model

import (
	"testing"
	"time"
)

func TestRecordAndUUID(t *testing.T) {
	id, e := ParseUUID("2f1eac90-f5c5-4c2d-8be4-2114cd6de3cb")
	if e != nil {
		t.Fatal(e)
	}
	r := AuditRecord{ID: id, EventID: id, EventType: "x", SourceService: "s", Payload: []byte(`{}`), ReceivedAt: time.Now().UTC()}
	if e := r.Validate(); e != nil {
		t.Fatal(e)
	}
}

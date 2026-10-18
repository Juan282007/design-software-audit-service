package amqp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xeipuuv/gojsonschema"
)

func TestCanonicalEventSchemaMatchesWorkerEnvelopeRules(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate schema test")
	}
	schemaPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "api", "events", "audit-consumed-event.schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	validate := func(payload string) *gojsonschema.Result {
		t.Helper()
		result, err := gojsonschema.Validate(gojsonschema.NewStringLoader(string(schema)), gojsonschema.NewStringLoader(payload))
		if err != nil {
			t.Fatalf("validate schema: %v", err)
		}
		return result
	}

	valid := `{
		"event_id":"10000000-0000-4000-8000-000000000001",
		"event_type":"USER_LOGIN","version":"1.0","timestamp":"2026-10-18T12:00:00Z",
		"source_service":"iam-service","actor_id":null,"entity_type":null,"entity_id":null,
		"training_center_id":"20000000-0000-4000-8000-000000000002","payload":{"result":"ok"}
	}`
	if result := validate(valid); !result.Valid() {
		t.Fatalf("valid envelope rejected: %v", result.Errors())
	}
	if _, err := ParseEnvelope([]byte(valid)); err != nil {
		t.Fatalf("worker rejected schema-valid envelope: %v", err)
	}

	invalid := `{
		"event_id":"not-a-uuid","event_type":"","version":"1.0","timestamp":"2026-10-18T12:00:00Z",
		"source_service":"iam-service","training_center_id":"not-a-uuid","payload":[]
	}`
	if result := validate(invalid); result.Valid() {
		t.Fatal("invalid envelope was accepted by JSON Schema")
	}
	if _, err := ParseEnvelope([]byte(invalid)); err == nil {
		t.Fatal("worker accepted schema-invalid envelope")
	}
}

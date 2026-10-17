package config

import "testing"

func TestQueryAPIReportsOnlyMissingVariableName(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secret")
	t.Setenv("IAM_JWKS_URL", "")
	t.Setenv("IAM_JWT_ISSUER", "issuer")
	_, e := LoadQueryAPI()
	if e == nil || e.Error() != "missing required configuration: IAM_JWKS_URL" {
		t.Fatalf("unexpected %v", e)
	}
}

func TestQueryAPIRequiresCursorSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://reader")
	t.Setenv("IAM_JWKS_URL", "https://iam.example/jwks")
	t.Setenv("IAM_JWT_ISSUER", "https://iam.example")
	t.Setenv("AUDIT_CURSOR_SECRET", "")
	_, e := LoadQueryAPI()
	if e == nil || e.Error() != "missing required configuration: AUDIT_CURSOR_SECRET" {
		t.Fatalf("unexpected %v", e)
	}
}
func TestWorkerValidatesRoutingKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "db")
	t.Setenv("MESSAGE_BROKER_URL", "amqp://secret")
	t.Setenv("BROKER_TOPIC_PATTERN", "#")
	t.Setenv("BROKER_CONSUMER_GROUP", "audit-queue")
	if _, e := LoadWorker(); e != nil {
		t.Fatal(e)
	}
}

func TestReadinessIntervalMustBePositive(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://reader")
	t.Setenv("IAM_JWKS_URL", "https://iam.example/jwks")
	t.Setenv("IAM_JWT_ISSUER", "https://iam.example")
	t.Setenv("AUDIT_CURSOR_SECRET", "cursor-secret")
	t.Setenv("READINESS_CHECK_INTERVAL_MS", "0")
	if _, err := LoadQueryAPI(); err == nil || err.Error() != "invalid readiness configuration" {
		t.Fatalf("unexpected %v", err)
	}
}

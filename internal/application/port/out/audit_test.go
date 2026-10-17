package out

import "testing"

func TestValidTrainingCenterIDRejectsMalformedScope(t *testing.T) {
	bad := "not-a-uuid"
	if _, err := (EventEnvelope{TrainingCenterID: &bad}).ValidTrainingCenterID(); err == nil {
		t.Fatal("expected invalid scope")
	}
}

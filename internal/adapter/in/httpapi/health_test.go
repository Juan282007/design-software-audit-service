package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthRouterReportsProcessLiveness(t *testing.T) {
	response := httptest.NewRecorder()
	NewHealthRouter().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type=%q", response.Header().Get("Content-Type"))
	}
	if response.Body.String() != `{"status":"ok"}` {
		t.Fatalf("body=%q", response.Body.String())
	}
}

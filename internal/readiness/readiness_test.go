package readiness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckerContinuouslyReflectsDependencyAvailability(t *testing.T) {
	var available atomic.Bool
	available.Store(true)
	checker := New(context.Background(), 10*time.Millisecond, func(context.Context) error {
		if available.Load() {
			return nil
		}
		return errUnavailable{}
	})
	defer checker.Close()
	eventually(t, func() bool { return checker.Ready() })
	available.Store(false)
	eventually(t, func() bool { return !checker.Ready() })
}

func TestInternalReadinessRouteIsNarrowAndDependencyBased(t *testing.T) {
	var available atomic.Bool
	available.Store(true)
	handler := Handler(available.Load)
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status=%d", response.Code)
	}
	available.Store(false)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/other", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected internal route status=%d", response.Code)
	}
}

func TestStartInternalHTTPSupportsDeploymentProbe(t *testing.T) {
	server, err := StartInternalHTTP("127.0.0.1:0", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not reached")
}

type errUnavailable struct{}

func (errUnavailable) Error() string { return "dependency unavailable" }

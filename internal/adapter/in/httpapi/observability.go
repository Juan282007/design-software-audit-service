package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type traceIDContextKey struct{}

// withRequestObservability emits only operational metadata. It deliberately
// excludes authorization headers, query values, payloads and response bodies.
func withRequestObservability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackTraceID, incomingTraceparent := traceFromRequest(r)
		route := logicalRoute(r.URL.Path)
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := observability.StartHTTP(ctx, r.Method, route)
		traceID := observability.TraceID(ctx)
		if traceID == "" {
			traceID = fallbackTraceID
		}
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if incomingTraceparent != "" {
			recorder.Header().Set("Traceparent", incomingTraceparent)
		}
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(ctx, traceIDContextKey{}, traceID)))
		duration := time.Since(started)
		observability.EndHTTP(ctx, span, route, recorder.status, duration.Seconds())
		slog.Info("audit_query_request", "trace_id", traceID, "method", r.Method, "route", route, "status", recorder.status, "duration_ms", duration.Milliseconds())
	})
}

func logicalRoute(path string) string {
	switch {
	case strings.HasSuffix(path, "/health"):
		return "/health"
	case strings.HasSuffix(path, "/audit-records/export"):
		return "/audit-records/export"
	case strings.Contains(path, "/audit-records/"):
		return "/audit-records/{id}"
	default:
		return "/audit-records"
	}
}

// traceFromRequest preserves a valid W3C trace id provided by Traefik. The
// service does not log the complete header and creates a fresh id only when it
// is absent or malformed.
func traceFromRequest(r *http.Request) (string, string) {
	traceparent := r.Header.Get("Traceparent")
	parts := strings.Split(traceparent, "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return newTraceID(), ""
	}
	for _, part := range parts {
		if _, err := hex.DecodeString(part); err != nil {
			return newTraceID(), ""
		}
	}
	if parts[1] == "00000000000000000000000000000000" || parts[2] == "0000000000000000" {
		return newTraceID(), ""
	}
	return parts[1], traceparent
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func traceIDFromContext(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDContextKey{}).(string); ok && traceID != "" {
		return traceID
	}
	return newTraceID()
}

func newTraceID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "trace-id-unavailable"
	}
	return hex.EncodeToString(bytes[:])
}

package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorDetail struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}
type errorBody struct {
	Error struct {
		Code    string        `json:"code"`
		Message string        `json:"message"`
		Details []errorDetail `json:"details,omitempty"`
		TraceID string        `json:"trace_id"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details []errorDetail) {
	traceID := traceIDFromContext(r.Context())
	response := errorBody{}
	response.Error.Code, response.Error.Message, response.Error.Details, response.Error.TraceID = code, message, details, traceID
	if status >= http.StatusInternalServerError {
		slog.Error("audit_query_error", "trace_id", traceID, "status", status, "error_code", code)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

const (
	exportCSV  = "csv"
	exportJSON = "json"
)

// writeAuditExport validates and opens the row stream before committing a file
// response. This guarantees that authentication, authorization, validation and
// query-opening failures still use the standard JSON error envelope.
func writeAuditExport(w http.ResponseWriter, r *http.Request, export usecase.ExportAudit, codec cursorCodec) {
	format, filter, details := parseExport(r, codec)
	if len(details) > 0 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Los parametros de exportacion no son validos.", details)
		return
	}
	filter.TrainingCenterID = scopeFromContext(r.Context()).TrainingCenterID
	iterator, err := export.Execute(r.Context(), filter)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "No fue posible exportar los registros de auditoria.", nil)
		return
	}
	defer iterator.Close()

	if format == exportJSON {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-records.json"`)
		w.WriteHeader(http.StatusOK)
		streamJSON(w, iterator, r)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-records.csv"`)
	w.WriteHeader(http.StatusOK)
	streamCSV(w, iterator, r)
}

func parseExport(r *http.Request, codec cursorCodec) (string, model.AuditFilter, []errorDetail) {
	filter, _, details := parseList(r, codec)
	q := r.URL.Query()
	format := exportCSV
	if raw, ok := q["format"]; ok {
		if len(raw) != 1 || raw[0] == "" || (raw[0] != exportCSV && raw[0] != exportJSON) {
			details = append(details, errorDetail{Field: "format", Issue: "UNSUPPORTED_FORMAT"})
		} else {
			format = raw[0]
		}
	}
	// Export is deliberately unpaged: accepting either parameter could produce
	// a partial download with semantics different from the contract.
	if _, ok := q["cursor"]; ok {
		details = append(details, errorDetail{Field: "cursor", Issue: "NOT_ALLOWED"})
	}
	if _, ok := q["limit"]; ok {
		details = append(details, errorDetail{Field: "limit", Issue: "NOT_ALLOWED"})
	}
	return format, filter, details
}

func streamCSV(w http.ResponseWriter, iterator out.AuditRecordIterator, r *http.Request) {
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "event_id", "event_type", "source_service", "actor_id", "entity_type", "entity_id", "event_occurred_at", "received_at", "payload"})
	writer.Flush()
	flush(w)
	for {
		record, ok, err := iterator.Next()
		if err != nil {
			logExportStreamFailure(r, err)
			return
		}
		if !ok {
			return
		}
		_ = writer.Write([]string{string(record.ID), string(record.EventID), record.EventType, record.SourceService, optionalUUID(record.ActorID), optionalText(record.EntityType), optionalUUID(record.EntityID), optionalTime(record.EventOccurredAt), record.ReceivedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), string(record.Payload)})
		writer.Flush()
		if err = writer.Error(); err != nil {
			logExportStreamFailure(r, err)
			return
		}
		flush(w)
	}
}

func streamJSON(w http.ResponseWriter, iterator out.AuditRecordIterator, r *http.Request) {
	_, _ = w.Write([]byte("["))
	first := true
	for {
		record, ok, err := iterator.Next()
		if err != nil {
			logExportStreamFailure(r, err)
			return
		}
		if !ok {
			_, _ = w.Write([]byte("]"))
			return
		}
		body, err := json.Marshal(toRecordJSON(record))
		if err != nil {
			logExportStreamFailure(r, err)
			return
		}
		if !first {
			_, _ = w.Write([]byte(","))
		}
		first = false
		_, _ = w.Write(body)
		flush(w)
	}
}

func optionalUUID(v *model.UUID) string {
	if v == nil {
		return ""
	}
	return string(*v)
}
func optionalText(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func optionalTime(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
}

func flush(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func logExportStreamFailure(r *http.Request, err error) {
	// Bytes may already be committed, so an error envelope would corrupt CSV or JSON.
	slog.Error("audit_export_stream_failed", "trace_id", traceIDFromContext(r.Context()), "error_type", "stream_failure")
}

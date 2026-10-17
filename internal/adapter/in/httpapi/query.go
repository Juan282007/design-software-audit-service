package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

// NewQueryRouter exposes only the contract's health and read routes.
func NewQueryRouter(verifier *JWTVerifier, list usecase.ListAudit, get usecase.GetAudit, export usecase.ExportAudit, secret string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	codec := cursorCodec{secret: []byte(secret)}
	mux.Handle("GET /audit-records", RequireAuditView(verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter, limit, details := parseList(r, codec)
		if len(details) > 0 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Los parámetros de consulta no son válidos.", details)
			return
		}
		scope := scopeFromContext(r.Context())
		filter.TrainingCenterID = scope.TrainingCenterID
		page, err := list.Execute(r.Context(), filter, limit)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "No fue posible consultar los registros de auditoría.", nil)
			return
		}
		response := struct {
			Data       []summaryJSON `json:"data"`
			Pagination struct {
				NextCursor *string `json:"next_cursor"`
				Limit      int     `json:"limit"`
				HasMore    bool    `json:"has_more"`
			} `json:"pagination"`
		}{Data: make([]summaryJSON, 0, len(page.Records))}
		for _, record := range page.Records {
			response.Data = append(response.Data, toSummaryJSON(record))
		}
		response.Pagination.Limit, response.Pagination.HasMore = limit, page.HasMore
		if page.HasMore {
			next, err := codec.Encode(model.AuditCursor{ReceivedAt: page.Records[len(page.Records)-1].ReceivedAt, ID: page.Records[len(page.Records)-1].ID})
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "No fue posible consultar los registros de auditoría.", nil)
				return
			}
			response.Pagination.NextCursor = &next
		}
		writeJSON(w, http.StatusOK, response)
	})))
	mux.Handle("GET /audit-records/{id}", RequireAuditView(verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := model.ParseUUID(r.PathValue("id"))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "El identificador no es válido.", []errorDetail{{Field: "id", Issue: "INVALID_UUID"}})
			return
		}
		record, err := get.Execute(r.Context(), id, scopeFromContext(r.Context()).TrainingCenterID)
		if errors.Is(err, out.ErrAuditRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "El registro de auditoría no existe.", nil)
			return
		}
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "No fue posible consultar el registro de auditoría.", nil)
			return
		}
		writeJSON(w, http.StatusOK, toRecordJSON(record))
	})))
	mux.Handle("GET /audit-records/export", RequireAuditExport(verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAuditExport(w, r, export, codec)
	})))
	// The executable may be reached directly or through the documented /api/v1 base path.
	root := http.NewServeMux()
	root.Handle("/api/v1/", http.StripPrefix("/api/v1", mux))
	root.Handle("/", mux)
	return withRequestObservability(root)
}

func parseList(r *http.Request, codec cursorCodec) (model.AuditFilter, int, []errorDetail) {
	q, filter, details := r.URL.Query(), model.AuditFilter{}, []errorDetail{}
	parseUUID := func(name string, target **model.UUID) {
		if raw, ok := q[name]; ok {
			if len(raw) != 1 || raw[0] == "" {
				details = append(details, errorDetail{name, "INVALID_UUID"})
				return
			}
			id, err := model.ParseUUID(raw[0])
			if err != nil {
				details = append(details, errorDetail{name, "INVALID_UUID"})
			} else {
				*target = &id
			}
		}
	}
	parseText := func(name string, target **string) {
		if raw, ok := q[name]; ok {
			if len(raw) != 1 || strings.TrimSpace(raw[0]) == "" {
				details = append(details, errorDetail{name, "MUST_NOT_BE_BLANK"})
			} else {
				value := raw[0]
				*target = &value
			}
		}
	}
	parseUUID("actor_id", &filter.ActorID)
	parseUUID("entity_id", &filter.EntityID)
	parseText("source_service", &filter.SourceService)
	parseText("event_type", &filter.EventType)
	parseText("entity_type", &filter.EntityType)
	parseTime := func(name string, target **time.Time) {
		if raw, ok := q[name]; ok {
			if len(raw) != 1 {
				details = append(details, errorDetail{name, "INVALID_DATE_TIME"})
				return
			}
			at, err := time.Parse(time.RFC3339, raw[0])
			if err != nil {
				details = append(details, errorDetail{name, "INVALID_DATE_TIME"})
			} else {
				at = at.UTC()
				*target = &at
			}
		}
	}
	parseTime("from", &filter.From)
	parseTime("to", &filter.To)
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		details = append(details, errorDetail{"from", "MUST_BE_BEFORE_TO"})
	}
	limit := 50
	if raw, ok := q["limit"]; ok {
		if len(raw) != 1 {
			details = append(details, errorDetail{"limit", "INVALID_LIMIT"})
		} else if parsed, err := strconv.Atoi(raw[0]); err != nil || parsed < 1 || parsed > 100 {
			details = append(details, errorDetail{"limit", "OUT_OF_RANGE"})
		} else {
			limit = parsed
		}
	}
	if raw, ok := q["cursor"]; ok {
		if len(raw) != 1 {
			details = append(details, errorDetail{"cursor", "INVALID_CURSOR"})
		} else if cursor, err := codec.Decode(raw[0]); err != nil {
			details = append(details, errorDetail{"cursor", "INVALID_CURSOR"})
		} else {
			filter.After = &cursor
		}
	}
	return filter, limit, details
}

type summaryJSON struct {
	ID              string     `json:"id"`
	EventID         string     `json:"event_id"`
	EventType       string     `json:"event_type"`
	SourceService   string     `json:"source_service"`
	ActorID         *string    `json:"actor_id"`
	EntityType      *string    `json:"entity_type"`
	EntityID        *string    `json:"entity_id"`
	EventOccurredAt *time.Time `json:"event_occurred_at"`
	ReceivedAt      time.Time  `json:"received_at"`
}

func toSummaryJSON(r model.AuditRecordSummary) summaryJSON {
	return summaryJSON{string(r.ID), string(r.EventID), r.EventType, r.SourceService, uuidString(r.ActorID), r.EntityType, uuidString(r.EntityID), r.EventOccurredAt, r.ReceivedAt}
}
func uuidString(id *model.UUID) *string {
	if id == nil {
		return nil
	}
	value := string(*id)
	return &value
}

type recordJSON struct {
	summaryJSON
	Payload json.RawMessage `json:"payload"`
}

func toRecordJSON(r model.AuditRecord) recordJSON {
	return recordJSON{summaryJSON: summaryJSON{string(r.ID), string(r.EventID), r.EventType, r.SourceService, uuidString(r.ActorID), r.EntityType, uuidString(r.EntityID), r.EventOccurredAt, r.ReceivedAt}, Payload: json.RawMessage(r.Payload)}
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

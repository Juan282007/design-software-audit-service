package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type queryReaderSpy struct {
	page                  model.AuditPage
	record                model.AuditRecord
	listFilter            model.AuditFilter
	getScope              *model.UUID
	listCalled, getCalled bool
	listErr, getErr       error
	exportFilter          model.AuditFilter
	exportCalled          bool
	exportIterator        out.AuditRecordIterator
	exportErr             error
}

func (s *queryReaderSpy) List(_ context.Context, filter model.AuditFilter, _ int) (model.AuditPage, error) {
	s.listCalled = true
	s.listFilter = filter
	return s.page, s.listErr
}
func (s *queryReaderSpy) Get(_ context.Context, _ model.UUID, scope *model.UUID) (model.AuditRecord, error) {
	s.getCalled = true
	s.getScope = scope
	return s.record, s.getErr
}
func (s *queryReaderSpy) OpenExport(_ context.Context, filter model.AuditFilter) (out.AuditRecordIterator, error) {
	s.exportCalled = true
	s.exportFilter = filter
	return s.exportIterator, s.exportErr
}

type auditIteratorSpy struct {
	records []model.AuditRecord
	index   int
	err     error
	closed  bool
}

func (s *auditIteratorSpy) Next() (model.AuditRecord, bool, error) {
	if s.err != nil {
		return model.AuditRecord{}, false, s.err
	}
	if s.index >= len(s.records) {
		return model.AuditRecord{}, false, nil
	}
	r := s.records[s.index]
	s.index++
	return r, true, nil
}
func (s *auditIteratorSpy) Close() { s.closed = true }

func TestListUsesGlobalScopeAndExcludesPayload(t *testing.T) {
	reader := &queryReaderSpy{page: model.AuditPage{Records: []model.AuditRecordSummary{summary("10000000-0000-4000-8000-000000000001", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))}}}
	h, token := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records?event_type=LOGIN", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !reader.listCalled || reader.listFilter.TrainingCenterID != nil {
		t.Fatalf("status=%d called=%v scope=%v", w.Code, reader.listCalled, reader.listFilter.TrainingCenterID)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	item := body["data"].([]any)[0].(map[string]any)
	if _, found := item["payload"]; found {
		t.Fatal("list response must exclude payload")
	}
}

func TestListForcesTrainingCenterScopeAndRejectsInvalidInputs(t *testing.T) {
	center := "20000000-0000-4000-8000-000000000002"
	reader := &queryReaderSpy{}
	h, token := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:TRAINING_CENTER"}, &center)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/audit-records?actor_id=bad&limit=101&from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || reader.listCalled {
		t.Fatalf("status=%d list=%v", w.Code, reader.listCalled)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/audit-records", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || reader.listFilter.TrainingCenterID == nil || string(*reader.listFilter.TrainingCenterID) != center {
		t.Fatalf("scope was not forced: %#v", reader.listFilter.TrainingCenterID)
	}
}

func TestDetailUsesSingleScopedReadAndAuthenticationErrorsUseEnvelope(t *testing.T) {
	center := "20000000-0000-4000-8000-000000000002"
	reader := &queryReaderSpy{record: record("10000000-0000-4000-8000-000000000001")}
	h, token := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:TRAINING_CENTER"}, &center)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/audit-records/10000000-0000-4000-8000-000000000001", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !reader.getCalled || reader.getScope == nil || string(*reader.getScope) != center {
		t.Fatalf("status=%d called=%v scope=%v", w.Code, reader.getCalled, reader.getScope)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["payload"].(map[string]any); !ok {
		t.Fatalf("payload missing: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/audit-records", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"].(map[string]any)["code"] != "MISSING_TOKEN" {
		t.Fatalf("unexpected error: %s", w.Body.String())
	}
	reader.listCalled, reader.getCalled = false, false
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/audit-records", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || reader.listCalled || reader.getCalled {
		t.Fatalf("write route status=%d list=%v get=%v", w.Code, reader.listCalled, reader.getCalled)
	}
}

func TestCursorIsAuthenticatedAndStable(t *testing.T) {
	codec := cursorCodec{secret: []byte("test-secret")}
	id, _ := model.ParseUUID("10000000-0000-4000-8000-000000000001")
	raw, err := codec.Encode(model.AuditCursor{ReceivedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), ID: id})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(raw)
	if err != nil || decoded.ID != id {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	if _, err = codec.Decode(raw + "x"); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}

func TestHealthIsPublicAndDoesNotClaimDependencies(t *testing.T) {
	h, _ := queryHandler(t, &queryReaderSpy{}, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || w.Body.String() != "{\"status\":\"ok\"}" {
		t.Fatalf("health status=%d content-type=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func TestContractErrorResponsesDoNotReachBusinessAndIncludeTraceID(t *testing.T) {
	reader := &queryReaderSpy{listErr: errors.New("database unavailable"), getErr: errors.New("database unavailable")}
	h, token := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil)

	assertError := func(request *http.Request, wantStatus int, wantCode string) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != wantStatus || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
		}
		var body map[string]map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["error"]["code"] != wantCode || body["error"]["trace_id"] == "" {
			t.Fatalf("unexpected error body: %s", w.Body.String())
		}
	}

	invalidID := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records/not-a-uuid", nil)
	invalidID.Header.Set("Authorization", "Bearer "+token)
	assertError(invalidID, http.StatusBadRequest, "VALIDATION_ERROR")
	if reader.getCalled {
		t.Fatal("invalid id must not reach business")
	}

	malformedToken := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records", nil)
	malformedToken.Header.Set("Authorization", "Bearer malformed")
	assertError(malformedToken, http.StatusUnauthorized, "TOKEN_INVALID")
	if reader.listCalled {
		t.Fatal("invalid token must not reach business")
	}

	listFailure := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records", nil)
	listFailure.Header.Set("Authorization", "Bearer "+token)
	assertError(listFailure, http.StatusInternalServerError, "INTERNAL_ERROR")

	reader.getErr = out.ErrAuditRecordNotFound
	notFound := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records/10000000-0000-4000-8000-000000000001", nil)
	notFound.Header.Set("Authorization", "Bearer "+token)
	assertError(notFound, http.StatusNotFound, "NOT_FOUND")
}

func TestAuthenticationAndScopeFailuresDoNotReachEitherUseCase(t *testing.T) {
	request := func(h http.Handler, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		return w
	}
	assertCode := func(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
		t.Helper()
		var body map[string]map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != wantStatus || body["error"]["code"] != wantCode {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	expiredReader := &queryReaderSpy{}
	expiredHandler, expiredToken := queryHandlerWithExpiry(t, expiredReader, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil, time.Now().Add(-time.Minute))
	assertCode(t, request(expiredHandler, expiredToken), http.StatusUnauthorized, "TOKEN_EXPIRED")
	if expiredReader.listCalled || expiredReader.getCalled {
		t.Fatal("expired token must not reach business")
	}

	featureReader := &queryReaderSpy{}
	featureHandler, featureToken := queryHandler(t, featureReader, []string{"ADMIN"}, nil)
	assertCode(t, request(featureHandler, featureToken), http.StatusForbidden, "INSUFFICIENT_PERMISSIONS")
	if featureReader.listCalled || featureReader.getCalled {
		t.Fatal("missing feature must not reach business")
	}

	scopeReader := &queryReaderSpy{}
	scopeHandler, scopeToken := queryHandler(t, scopeReader, []string{"AUDIT_LOG_VIEW:TRAINING_CENTER"}, nil)
	assertCode(t, request(scopeHandler, scopeToken), http.StatusForbidden, "SCOPE_VIOLATION")
	if scopeReader.listCalled || scopeReader.getCalled {
		t.Fatal("invalid scope must not reach business")
	}
}

func TestStructuredErrorLogCorrelatesTraceWithoutSensitiveRequestData(t *testing.T) {
	previous := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	reader := &queryReaderSpy{listErr: errors.New("persistence failure")}
	h, token := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records?event_type=sensitive-event-value", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	var body map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	traceID := body["error"]["trace_id"].(string)
	output := logs.String()
	if !strings.Contains(output, traceID) || !strings.Contains(output, "INTERNAL_ERROR") {
		t.Fatalf("trace correlation missing from logs: %s", output)
	}
	if strings.Contains(output, token) || strings.Contains(output, "sensitive-event-value") || strings.Contains(output, "payload") {
		t.Fatalf("sensitive request data leaked to logs: %s", output)
	}
}

func TestRequestObservabilityPropagatesValidTraceparent(t *testing.T) {
	reader := &queryReaderSpy{listErr: errors.New("persistence failure")}
	h, token := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil)
	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Traceparent", traceparent)
	h.ServeHTTP(w, r)
	if w.Header().Get("Traceparent") != traceparent || !strings.Contains(w.Body.String(), "4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatalf("trace was not propagated: header=%q body=%s", w.Header().Get("Traceparent"), w.Body.String())
	}
}

func TestExportUsesIndependentPermissionAndStreamsCSVAndJSON(t *testing.T) {
	first := record("10000000-0000-4000-8000-000000000001")
	first.Payload = []byte(`{"private":"kept-as-json"}`)
	iterator := &auditIteratorSpy{records: []model.AuditRecord{first}}
	reader := &queryReaderSpy{exportIterator: iterator}
	h, token := queryHandler(t, reader, []string{"AUDIT_EXPORT:GLOBAL"}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records/export?event_type=LOGIN", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !reader.exportCalled || reader.exportFilter.TrainingCenterID != nil || !iterator.closed {
		t.Fatalf("csv status=%d export=%v scope=%v closed=%v", w.Code, reader.exportCalled, reader.exportFilter.TrainingCenterID, iterator.closed)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("unexpected csv content type %q", w.Header().Get("Content-Type"))
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 2 || rows[0][0] != "id" || rows[1][9] != `{"private":"kept-as-json"}` {
		t.Fatalf("csv rows=%#v err=%v", rows, err)
	}

	center := "20000000-0000-4000-8000-000000000002"
	reader = &queryReaderSpy{exportIterator: &auditIteratorSpy{records: []model.AuditRecord{first}}}
	h, token = queryHandler(t, reader, []string{"AUDIT_EXPORT:TRAINING_CENTER"}, &center)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/v1/audit-records/export?format=json", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || reader.exportFilter.TrainingCenterID == nil || string(*reader.exportFilter.TrainingCenterID) != center {
		t.Fatalf("json status=%d scope=%v", w.Code, reader.exportFilter.TrainingCenterID)
	}
	var body []map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body) != 1 {
		t.Fatalf("json body=%s err=%v", w.Body.String(), err)
	}
	if _, ok := body[0]["payload"].(map[string]any); !ok {
		t.Fatalf("payload was double encoded: %s", w.Body.String())
	}
}

func TestExportRejectsViewOnlyAndBadParametersBeforeOpeningStream(t *testing.T) {
	reader := &queryReaderSpy{exportIterator: &auditIteratorSpy{}}
	h, viewToken := queryHandler(t, reader, []string{"AUDIT_LOG_VIEW:GLOBAL"}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records/export", nil)
	r.Header.Set("Authorization", "Bearer "+viewToken)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || reader.exportCalled {
		t.Fatalf("view-only status=%d export=%v", w.Code, reader.exportCalled)
	}
	var denied map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &denied)
	if denied["error"]["code"] != "INSUFFICIENT_PERMISSIONS" {
		t.Fatalf("body=%s", w.Body.String())
	}

	reader = &queryReaderSpy{exportIterator: &auditIteratorSpy{}}
	h, exportToken := queryHandler(t, reader, []string{"AUDIT_EXPORT:GLOBAL"}, nil)
	for _, rawURL := range []string{"/api/v1/audit-records/export?format=xml", "/api/v1/audit-records/export?limit=2", "/api/v1/audit-records/export?cursor=anything"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodGet, rawURL, nil)
		r.Header.Set("Authorization", "Bearer "+exportToken)
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || reader.exportCalled {
			t.Fatalf("url=%s status=%d export=%v", rawURL, w.Code, reader.exportCalled)
		}
	}
}

func TestExportOpeningFailureUsesErrorEnvelopeBeforeFile(t *testing.T) {
	reader := &queryReaderSpy{exportErr: errors.New("database unavailable")}
	h, token := queryHandler(t, reader, []string{"AUDIT_EXPORT:GLOBAL"}, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-records/export", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "INTERNAL_ERROR") {
		t.Fatalf("status=%d content-type=%q body=%s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func queryHandler(t *testing.T, reader *queryReaderSpy, features []string, center *string) (http.Handler, string) {
	return queryHandlerWithExpiry(t, reader, features, center, time.Now().Add(time.Minute))
}

func queryHandlerWithExpiry(t *testing.T, reader *queryReaderSpy, features []string, center *string, expiry time.Time) (http.Handler, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "query-key", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	}))
	t.Cleanup(server.Close)
	payload := map[string]any{"sub": "user", "iss": "issuer", "exp": expiry.Unix(), "features": features}
	if center != nil {
		payload["training_center_id"] = *center
	}
	return NewQueryRouter(NewJWTVerifier("issuer", server.URL), usecase.ListAudit{Reader: reader}, usecase.GetAudit{Reader: reader}, usecase.ExportAudit{Reader: reader}, "test-secret"), signedToken(t, key, "RS256", "query-key", payload)
}
func summary(raw string, received time.Time) model.AuditRecordSummary {
	id, _ := model.ParseUUID(raw)
	return model.AuditRecordSummary{ID: id, EventID: id, EventType: "LOGIN", SourceService: "iam", ReceivedAt: received}
}
func record(raw string) model.AuditRecord {
	id, _ := model.ParseUUID(raw)
	return model.AuditRecord{ID: id, EventID: id, EventType: "LOGIN", SourceService: "iam", Payload: []byte(`{"email":"not-returned-in-list"}`), ReceivedAt: time.Now().UTC()}
}

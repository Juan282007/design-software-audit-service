// Command audit-contract-provider is a CI-only Pact verification harness.
// It invokes the production query router with deterministic provider data and
// replaces the Pact fixture bearer token with a locally signed RS256 token.
package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/adapter/in/httpapi"
	"github.com/code-sena/design-software-audit-service/internal/application/port/out"
	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

const pactToken = "pact-contract-token"

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "pact-provider", "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}})
	}))
	defer jwks.Close()

	token, err := signedToken(key)
	if err != nil {
		log.Fatal(err)
	}
	reader := contractReader{}
	router := httpapi.NewQueryRouter(
		httpapi.NewJWTVerifier("pact-issuer", jwks.URL),
		usecase.ListAudit{Reader: reader}, usecase.GetAudit{Reader: reader}, usecase.ExportAudit{Reader: reader}, "pact-cursor-secret",
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+pactToken {
			r = r.Clone(r.Context())
			r.Header = r.Header.Clone()
			r.Header.Set("Authorization", "Bearer "+token)
		}
		router.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServe(":18080", handler))
}

type contractReader struct{}

func (contractReader) List(context.Context, model.AuditFilter, int) (model.AuditPage, error) {
	id, _ := model.ParseUUID("10000000-0000-4000-8000-000000000001")
	eventID, _ := model.ParseUUID("10000000-0000-4000-8000-000000000002")
	receivedAt, _ := time.Parse(time.RFC3339, "2026-10-18T12:00:00Z")
	return model.AuditPage{Records: []model.AuditRecordSummary{{
		ID: id, EventID: eventID, EventType: "USER_LOGIN", SourceService: "iam-service", ReceivedAt: receivedAt.UTC(),
	}}}, nil
}

func (contractReader) Get(context.Context, model.UUID, *model.UUID) (model.AuditRecord, error) {
	return model.AuditRecord{}, out.ErrAuditRecordNotFound
}

func (contractReader) OpenExport(context.Context, model.AuditFilter) (out.AuditRecordIterator, error) {
	return nil, out.ErrAuditRecordNotFound
}

func signedToken(key *rsa.PrivateKey) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "pact-provider"})
	payload, _ := json.Marshal(map[string]any{
		"sub": "pact-consumer", "iss": "pact-issuer", "exp": time.Now().Add(time.Hour).Unix(),
		"features": []string{"AUDIT_LOG_VIEW:GLOBAL"},
	})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

package httpapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWTVerifierAcceptsValidRS256TokenAndCachesJWKS(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "key-1", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	}))
	defer server.Close()
	v := NewJWTVerifier("https://issuer.example", server.URL)
	token := signedToken(t, key, "RS256", "key-1", map[string]any{"sub": "actor-1", "iss": "https://issuer.example", "exp": time.Now().Add(time.Minute).Unix(), "training_center_id": "2f1eac90-f5c5-4c2d-8be4-2114cd6de3cb", "features": []string{"AUDIT_VIEW"}})
	claims, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "actor-1" || claims.TrainingCenterID == "" || len(claims.Features) != 1 {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err = v.Verify(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("JWKS requests = %d, want 1", requests)
	}
}

func TestJWTVerifierRejectsNonRS256AndExpiredTokens(t *testing.T) {
	v := NewJWTVerifier("issuer", "http://unused")
	for _, token := range []string{
		signedToken(t, nil, "none", "key", map[string]any{"sub": "x", "iss": "issuer", "exp": time.Now().Add(time.Minute).Unix()}),
		signedToken(t, nil, "RS256", "key", map[string]any{"sub": "x", "iss": "issuer", "exp": time.Now().Add(-time.Minute).Unix()}),
	} {
		if _, err := v.Verify(context.Background(), token); err == nil {
			t.Fatal("expected token rejection")
		}
	}
}

func TestClaimsRequireExplicitFeatureScope(t *testing.T) {
	if _, err := (Claims{Features: []string{"AUDIT_LOG_VIEW:GLOBAL"}}).AuditViewScope(); err != nil {
		t.Fatal("expected global audit feature")
	}
	if _, err := (Claims{Features: []string{"ADMIN"}}).AuditViewScope(); err == nil {
		t.Fatal("roles must not authorize feature")
	}
	if _, err := (Claims{Features: []string{"AUDIT_LOG_VIEW:TRAINING_CENTER"}}).AuditViewScope(); err == nil {
		t.Fatal("center scope without a center must be rejected")
	}
}

func TestRequireAuditViewProtectsAnHTTPRoute(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "route-key", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	}))
	defer server.Close()
	h := RequireAuditView(NewJWTVerifier("issuer", server.URL), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := func(token string) int {
		r := httptest.NewRequest(http.MethodGet, "/audit-records", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	valid := signedToken(t, key, "RS256", "route-key", map[string]any{"sub": "u", "iss": "issuer", "exp": time.Now().Add(time.Minute).Unix(), "features": []string{"AUDIT_LOG_VIEW:GLOBAL"}})
	withoutFeature := signedToken(t, key, "RS256", "route-key", map[string]any{"sub": "u", "iss": "issuer", "exp": time.Now().Add(time.Minute).Unix(), "features": []string{"ADMIN"}})
	if got := request(valid); got != http.StatusNoContent {
		t.Fatalf("valid feature status=%d", got)
	}
	if got := request(withoutFeature); got != http.StatusForbidden {
		t.Fatalf("missing feature status=%d", got)
	}
	if got := request(""); got != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", got)
	}
}

func signedToken(t *testing.T, key *rsa.PrivateKey, alg, kid string, payload map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid})
	body, _ := json.Marshal(payload)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	if key == nil {
		return input + "."
	}
	sum := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

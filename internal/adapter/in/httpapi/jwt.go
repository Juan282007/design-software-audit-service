package httpapi

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

var ErrTokenExpired = errors.New("token expired")
var errMissingFeature = errors.New("required feature missing")
var errInvalidScope = errors.New("invalid feature scope")

// Claims contains only authorization data declared by the audit API contract.
type Claims struct {
	Subject, TrainingCenterID string
	Features                  []string
}

// AuditViewScope accepts only IAM's CODE:SCOPE_TYPE grant representation.
func (c Claims) AuditViewScope() (auditScope, error) { return c.auditScope("AUDIT_LOG_VIEW") }

// AuditExportScope is intentionally independent from AUDIT_LOG_VIEW.
func (c Claims) AuditExportScope() (auditScope, error) { return c.auditScope("AUDIT_EXPORT") }

func (c Claims) auditScope(feature string) (auditScope, error) {
	global, center := false, false
	for _, granted := range c.Features {
		switch granted {
		case feature + ":GLOBAL":
			global = true
		case feature + ":TRAINING_CENTER":
			center = true
		}
	}
	if global {
		return auditScope{}, nil
	}
	if !center {
		return auditScope{}, errMissingFeature
	}
	id, err := model.ParseUUID(c.TrainingCenterID)
	if err != nil {
		return auditScope{}, errInvalidScope
	}
	return auditScope{TrainingCenterID: &id}, nil
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}
type jwk struct{ KID, KTY, N, E, Use, Alg string }

// JWTVerifier caches JWKS keys locally; it never calls IAM for each request.
type JWTVerifier struct {
	issuer, jwksURL string
	client          *http.Client
	mu              sync.RWMutex
	keys            map[string]*rsa.PublicKey
}

func NewJWTVerifier(issuer, jwksURL string) *JWTVerifier {
	return &JWTVerifier{issuer: issuer, jwksURL: jwksURL, client: &http.Client{Timeout: 5 * time.Second}, keys: map[string]*rsa.PublicKey{}}
}

func (v *JWTVerifier) Verify(ctx context.Context, raw string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("invalid bearer token")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KID       string `json:"kid"`
	}
	if decodeJSON(parts[0], &header) != nil || header.Algorithm != "RS256" || header.KID == "" {
		return Claims{}, errors.New("unsupported token header")
	}
	key := v.key(header.KID)
	if key == nil {
		if err := v.refresh(ctx); err != nil {
			return Claims{}, errors.New("unable to load signing keys")
		}
		key = v.key(header.KID)
		if key == nil {
			return Claims{}, errors.New("unknown signing key")
		}
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, errors.New("invalid token signature")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature) != nil {
		return Claims{}, errors.New("invalid token signature")
	}
	var payload struct {
		Subject          string   `json:"sub"`
		Issuer           string   `json:"iss"`
		ExpiresAt        int64    `json:"exp"`
		TrainingCenterID *string  `json:"training_center_id"`
		Features         []string `json:"features"`
	}
	if decodeJSON(parts[1], &payload) != nil || payload.Subject == "" || payload.Issuer != v.issuer {
		return Claims{}, errors.New("invalid token claims")
	}
	if payload.ExpiresAt <= time.Now().Unix() {
		return Claims{}, ErrTokenExpired
	}
	center := ""
	if payload.TrainingCenterID != nil {
		center = *payload.TrainingCenterID
	}
	return Claims{payload.Subject, center, payload.Features}, nil
}
func (v *JWTVerifier) key(kid string) *rsa.PublicKey {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.keys[kid]
}
func (v *JWTVerifier) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("jwks unavailable")
	}
	var doc jwksDocument
	if err = json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.KTY != "RSA" || k.Alg != "RS256" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(k.N)
		e2, e2err := base64.RawURLEncoding.DecodeString(k.E)
		if e1 != nil || e2err != nil {
			continue
		}
		e := 0
		for _, b := range e2 {
			e = e<<8 + int(b)
		}
		if e <= 1 {
			continue
		}
		keys[k.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: e}
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}
func decodeJSON(encoded string, target any) error {
	b, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, target)
}

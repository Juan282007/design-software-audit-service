package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type claimsContextKey struct{}

type auditScope struct{ TrainingCenterID *model.UUID }
type auditScopeContextKey struct{}

// RequireAuditView verifies the JWT and resolves the exact AUDIT_LOG_VIEW scope.
func RequireAuditView(verifier *JWTVerifier, next http.Handler) http.Handler {
	return requireAuditFeature(verifier, "AUDIT_LOG_VIEW", next)
}

// RequireAuditExport does not delegate to RequireAuditView: export permission
// is a distinct contract feature and cannot be inferred from view permission.
func RequireAuditExport(verifier *JWTVerifier, next http.Handler) http.Handler {
	return requireAuditFeature(verifier, "AUDIT_EXPORT", next)
}

func requireAuditFeature(verifier *JWTVerifier, feature string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) == "" {
			writeError(w, r, http.StatusUnauthorized, "MISSING_TOKEN", "Se requiere un token Bearer.", nil)
			return
		}
		claims, err := verifier.Verify(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil {
			code := "TOKEN_INVALID"
			if err == ErrTokenExpired {
				code = "TOKEN_EXPIRED"
			}
			writeError(w, r, http.StatusUnauthorized, code, "El token de acceso no es válido.", nil)
			return
		}
		var scope auditScope
		if feature == "AUDIT_EXPORT" {
			scope, err = claims.AuditExportScope()
		} else {
			scope, err = claims.AuditViewScope()
		}
		if err == errMissingFeature {
			writeError(w, r, http.StatusForbidden, "INSUFFICIENT_PERMISSIONS", "No tiene permisos para consultar auditoría.", nil)
			return
		}
		if err != nil {
			writeError(w, r, http.StatusForbidden, "SCOPE_VIOLATION", "El alcance del token no es válido.", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(context.WithValue(r.Context(), claimsContextKey{}, claims), auditScopeContextKey{}, scope)))
	})
}

func scopeFromContext(ctx context.Context) auditScope {
	return ctx.Value(auditScopeContextKey{}).(auditScope)
}

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/adapter/in/httpapi"
	"github.com/code-sena/design-software-audit-service/internal/adapter/out/persistence"
	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/config"
	"github.com/code-sena/design-software-audit-service/internal/observability"
	"github.com/code-sena/design-software-audit-service/internal/readiness"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	cfg, err := config.LoadQueryAPI()
	if err != nil {
		slog.Error("audit_query_api_configuration_failed", "error_type", "configuration")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTelemetry, err := observability.Init(ctx, "audit-query-api", cfg.ServiceEnv)
	if err != nil {
		slog.Error("audit_query_api_observability_failed", "error_type", "observability")
		return
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutdownCtx)
	}()
	db, err := persistence.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("audit_query_api_database_pool_failed", "error_type", "database_connection")
		return
	}
	defer db.Close()
	dependencyReadiness := readiness.New(ctx, cfg.ReadinessInterval, func(checkCtx context.Context) error { return persistence.Ping(checkCtx, db) })
	defer dependencyReadiness.Close()
	readinessServer, err := readiness.StartInternalHTTP(cfg.ReadinessAddress, dependencyReadiness.Ready)
	if err != nil {
		slog.Error("audit_query_api_readiness_listener_failed", "error_type", "readiness_listener")
		return
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = readinessServer.Shutdown(shutdownCtx)
	}()
	store := persistence.AuditStore{Pool: db}
	server := &http.Server{Addr: ":" + cfg.Port, Handler: httpapi.NewQueryRouter(
		httpapi.NewJWTVerifier(cfg.IAMJWTIssuer, cfg.IAMJWKSURL), usecase.ListAudit{Reader: store}, usecase.GetAudit{Reader: store}, usecase.ExportAudit{Reader: store}, cfg.CursorSecret,
	)}
	slog.Info("audit_query_api_started", "environment", cfg.ServiceEnv, "port", cfg.Port)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("audit_query_api_stopped", "error_type", "http_server")
	}
}

package main

import (
	"context"
	"github.com/code-sena/design-software-audit-service/internal/adapter/in/amqp"
	"github.com/code-sena/design-software-audit-service/internal/adapter/out/persistence"
	"github.com/code-sena/design-software-audit-service/internal/application/usecase"
	"github.com/code-sena/design-software-audit-service/internal/config"
	"github.com/code-sena/design-software-audit-service/internal/observability"
	"github.com/code-sena/design-software-audit-service/internal/readiness"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	cfg, err := config.LoadWorker()
	if err != nil {
		slog.Error("audit_worker_configuration_failed", "error_type", "configuration")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTelemetry, err := observability.Init(ctx, "audit-worker", cfg.ServiceEnv)
	if err != nil {
		slog.Error("audit_worker_observability_failed", "error_type", "observability")
		return
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutdownCtx)
	}()
	db, err := persistence.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("audit_worker_database_pool_failed", "error_type", "database_connection")
		return
	}
	defer db.Close()
	databaseReadiness := readiness.New(ctx, cfg.ReadinessInterval, func(checkCtx context.Context) error { return persistence.Ping(checkCtx, db) })
	defer databaseReadiness.Close()
	worker := amqp.NewConsumer(amqp.Config{URL: cfg.MessageBrokerURL, Exchange: cfg.AuditEventsExchange, Queue: cfg.BrokerConsumerGroup, RoutingKey: cfg.BrokerTopicPattern, MaxRetries: cfg.MaxRetries, RetryDelayMilliseconds: cfg.RetryDelayMilliseconds, DatabaseReady: databaseReadiness.Ready}, usecase.AppendAudit{Store: persistence.AuditStore{Pool: db}})
	readinessServer, err := readiness.StartInternalHTTP(cfg.ReadinessAddress, worker.Ready)
	if err != nil {
		slog.Error("audit_worker_readiness_listener_failed", "error_type", "readiness_listener")
		return
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = readinessServer.Shutdown(shutdownCtx)
	}()
	slog.Info("audit_worker_started", "environment", cfg.ServiceEnv, "queue", cfg.BrokerConsumerGroup, "routing_key", cfg.BrokerTopicPattern)
	if err := worker.Run(ctx); err != nil {
		slog.Error("audit_worker_stopped", "error_type", "worker")
	}
}

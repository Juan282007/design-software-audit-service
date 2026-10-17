# design-software-audit-service

Servicio de auditoria Go con arquitectura hexagonal y dos ejecutables independientes: `cmd/audit-query-api` y `cmd/audit-worker`.

La configuración se carga exclusivamente desde ambiente y falla antes de aceptar tráfico o consumir mensajes. Revise `.env.example`; no contiene credenciales válidas. La API requiere `DATABASE_URL`, `IAM_JWKS_URL` e `IAM_JWT_ISSUER`. El worker requiere `DATABASE_URL`, `MESSAGE_BROKER_URL`, `BROKER_TOPIC_PATTERN=#` y `BROKER_CONSUMER_GROUP` (nombre de la cola durable).

```powershell
go test ./...
go build ./cmd/audit-query-api
go build ./cmd/audit-worker
```

## API de consulta y exportacion

- `GET /api/v1/health` es liveness del proceso; no representa disponibilidad de PostgreSQL.
- `GET /api/v1/audit-records` y `GET /api/v1/audit-records/{id}` exigen `AUDIT_LOG_VIEW`.
- `GET /api/v1/audit-records/export` exige de forma independiente `AUDIT_EXPORT`; acepta `format=csv` (predeterminado) o `format=json`, aplica los filtros declarados y el mismo scope `GLOBAL`/`TRAINING_CENTER` del listado. No acepta cursor ni limit.

La exportacion abre el stream PostgreSQL antes de enviar el encabezado HTTP y recorre filas incrementalmente ordenadas por `received_at DESC, id DESC`. CSV conserva `payload` como JSON valido en una celda; JSON conserva `payload` como objeto.

## Readiness and observability

`GET /api/v1/health` remains the public liveness probe: it reports only that the query-api process is alive and never claims that PostgreSQL is available. Each executable also exposes an internal-only readiness listener, which must not be routed by Traefik or documented as a public API route:

| Component | Default internal readiness address | Ready when |
|---|---|---|
| `audit-query-api` | `:8081/ready` (`AUDIT_QUERY_READINESS_ADDR`) | PostgreSQL can be reached continuously. |
| `audit-worker` | `:8082/ready` (`AUDIT_WORKER_READINESS_ADDR`) | PostgreSQL and its RabbitMQ consumer session are available. |

`READINESS_CHECK_INTERVAL_MS` controls the dependency check interval (default `5000`). A worker whose PostgreSQL readiness is false does not acknowledge deliveries; the message follows the existing retry/DLQ topology.

Both executables initialize OpenTelemetry with W3C `traceparent` propagation. Configure `OTEL_EXPORTER_OTLP_ENDPOINT` (or the traces/metrics endpoint-specific variables) to send OTLP/gRPC signals to the environment collector. The service emits HTTP RED metrics (`audit.http.server.requests`, `audit.http.server.duration`) and worker outcome metrics (`audit.worker.events` with `inserted`, `duplicate`, `retry` or `dlq`). Spans retain only operation-level metadata; logs, metrics and spans exclude payloads, Authorization headers, query values, DSNs and credentials. `correlation_id` from the event envelope is telemetry-only and is never persisted as an `audit_record` column.

## Validation

```powershell
go test ./...
go test -tags=integration ./...
go test -covermode=atomic -coverpkg=./... -coverprofile=coverage ./...
go tool cover -func=coverage
```

The integration command requires the migrated PostgreSQL 16 schema and RabbitMQ topology. Set `TEST_AUDIT_READER_DSN`, `TEST_AUDIT_WRITER_DSN`, `E2E_DATABASE_URL` and `E2E_AMQP_URL` without committing their values. The E2E worker suite validates the exact resilience sequence: valid event, duplicate, invalid event to `audit.dlq`, and a subsequent distinct valid event.

## Imagenes

```powershell
docker build -f deploy/Dockerfile.query-api -t audit-query-api .
docker build -f deploy/Dockerfile.worker -t audit-worker .
```

Las etapas finales son distroless y se ejecutan como `nonroot`; `.dockerignore` excluye caches, archivos de entorno y fuentes de prueba del contexto de construccion.

## Query API configuration

| Variable | Required | Description |
|---|---|---|
| `AUDIT_CURSOR_SECRET` | Yes, for `audit-query-api` | High-entropy HMAC secret used to sign and verify pagination cursors. It must be provisioned as a non-versioned secret and must not be reused across environments. |

`AUDIT_CURSOR_SECRET` is required for `audit-query-api` startup. DEV and QA must provision it as a secret or environment variable; its value is never versioned in this repository.

HU-AUD-002 implementa el consumidor RabbitMQ: cola durable `BROKER_CONSUMER_GROUP`, binding `#` al exchange global `AUDIT_EVENTS_EXCHANGE` (por defecto `audit-events`), ack manual posterior al commit, retry acotado mediante cola TTL y `audit.dlq` para envelopes inválidos o fallos agotados. `WORKER_MAX_RETRIES` y `WORKER_RETRY_DELAY_MS` tienen valores seguros de desarrollo (`3` y `5000`); no son secretos.

El adaptador PostgreSQL aplica en una única transacción la reserva de `audit.audit_event_seen` y el registro append-only; requiere el changeset complementario en `audit-db` que añade privilegios y la proyección técnica `training_center_id`. Los endpoints de consulta/exportación permanecen en HU-AUD-003 y HU-AUD-004.

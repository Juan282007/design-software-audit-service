# R5 - Contracts and compatibility evidence

> Owner: Juan Pablo Gómez Perdomo  
> Service: audit-service  
> Status: In progress - executable gates added

## Delivered controls

| R5 criterion | Evidence |
| --- | --- |
| Provider Pact verification in CI | `.github/workflows/contract-compatibility.yml`, job `pact-provider`; consumer is `audit-mfe`. |
| Semantic OpenAPI and incompatible-change blocking | `api/openapi/audit.yaml` uses `info.version: 1.0.0`; job `openapi-breaking-change` executes `oasdiff breaking` against `develop`. |
| JSON Schema for events consumed/published | `api/events/audit-consumed-event.schema.json` and `TestCanonicalEventSchemaMatchesWorkerEnvelopeRules`. audit-service publishes no domain events. |
| Demonstrable rejected incompatible change | See the command below; removing a route, required response property, or narrowing a schema in a temporary branch makes `oasdiff` exit non-zero. |

## Local verification

```powershell
# audit-service
go test ./...
go test ./internal/adapter/in/amqp -run TestCanonicalEventSchemaMatchesWorkerEnvelopeRules -count=1
go run ./cmd/audit-contract-provider

# separate terminal, with Docker running
docker run --rm -v "${PWD}\contracts\pact:/pacts:ro" pactfoundation/pact-cli:1.5.0.5 pact_verifier_cli --provider-name audit-query-api --hostname host.docker.internal --port 18080 --file /pacts/audit-mfe-audit-query-api.json --no-colour

# audit-mfe
npm ci
npm run test:contract
```

## Demonstrating the blocking gate

1. Create a temporary local branch from this branch.
2. Remove `GET /audit-records` or a response field from `api/openapi/audit.yaml` without a compatible versioning strategy.
3. Run the `oasdiff breaking` command from the CI workflow using the original spec as the base.
4. Expected result: non-zero exit and a breaking-change report. Do not commit or merge that temporary change.

The CI runs the same gates for every PR targeting `develop`; a red contract check blocks the PR until the contract is made compatible or the versioned migration is reviewed.

## Local Pact execution

Executed on 2026-10-18 with Docker Desktop and `pactfoundation/pact-cli:1.5.0.5`:

```text
Verifying a pact between audit-mfe and audit-query-api
a paginated audit-record listing request
  has status code 200 (OK)
  includes Content-Type application/json (OK)
  has a matching body (OK)
```

The command exited with code `0`. The previously attempted `0.55.0` image was not found; the workflow now pins the available `1.5.0.5` image and invokes `pact_verifier_cli`.

# ADR-R5: Executable contract compatibility gates

> Status: Accepted  
> Date: 2026-10-18  
> Scope: audit-service R5 - Contracts and compatibility

## Context

`audit-mfe` is the concrete HTTP consumer of `audit-query-api`. `audit-worker` consumes canonical AMQP envelopes from domain publishers. An API or envelope change must be detected before it reaches `develop`.

## Decision

- `api/openapi/audit.yaml` remains the provider contract and its `info.version` must use Semantic Versioning.
- The CI workflow runs `oasdiff breaking` against the `develop` version. A breaking OpenAPI change fails the PR check.
- `audit-mfe` publishes a Pact interaction for `GET /api/v1/audit-records`; its generated Pact is versioned under `contracts/pact/` and verified by the provider job using the production query router, RS256 JWT validation and a deterministic read port.
- `api/events/audit-consumed-event.schema.json` defines the canonical envelope audit-worker consumes. The schema gate validates both an accepted and rejected fixture and cross-checks the production parser.
- audit-service is a terminal consumer and does not publish domain events. Therefore R5 has no producer-event schema for this service; the consumed-event schema is the applicable event contract.

## Consequences

Compatibility checks are reproducible locally and in pull requests. A deliberate removal or incompatible type change in OpenAPI is blocked by `oasdiff`; changing the Pact response shape is blocked by provider verification; malformed event envelopes are rejected by the schema and worker parser tests.

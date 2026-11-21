# Medication API

A production-minded REST API in Go for a medication catalog: full CRUD on PostgreSQL with
optimistic concurrency, keyset pagination, API-key auth, and a simulated price comparison
that fans out to several pricing networks under a fixed latency budget.

- **Contract:** [`openapi.yaml`](openapi.yaml) (OpenAPI 3.1). A test keeps it in sync with the router.
- **Try it:** [`requests.http`](requests.http) for the JetBrains HTTP Client, or `make smoke` for the same flow with curl.
- **How AI was used:** [`AI_USAGE.md`](AI_USAGE.md).

## How I approached the brief

| The brief says | How I read it | What I delivered |
|---|---|---|
| RESTful CRUD in Go | Create, read, list, update and delete, each with correct status codes | Five operations under `/v1/medications`; see [API at a glance](#api-at-a-glance) and [`openapi.yaml`](openapi.yaml) |
| Medication object (ID, Name, Dosage, Form) and custom fields | Model it the way pharmacy systems do, with public standards | `id`, `name`, `form`, `dosage` as an exact decimal strength with units, plus `route`, `rxcui`, `ndc`, `version` and timestamps; see [Domain model](#domain-model) |
| Performance and scalability | Queries that stay fast as the table grows; a stateless service | Keyset pagination, partial and trigram indexes checked with `EXPLAIN`, connection pooling with server-side timeouts; see [Design decisions](#design-decisions-and-trade-offs) |
| RESTful best practices | Standard HTTP semantics rather than custom conventions | ETags with `If-Match` / `If-None-Match`, `Location` and `Link` headers, RFC 9457 problem+json errors, versioned paths |
| Production-ready, full operational responsibility | I run it, so I must be able to deploy, observe and debug it | Validated config, probes, graceful shutdown, structured logs, build info, opt-in pprof, container, CI; see [Operations](#operations) |
| Any packages | Pick few, well-known dependencies and say why | Standard library HTTP, pgx, goose, testcontainers; see [Go tooling](#go-tooling) |
| Enhance to showcase expertise | One extension that fits the domain and shows Go concurrency | Price comparison fanned out under a latency budget, tested with `testing/synctest`; see [Why this extension](#why-this-extension-price-comparison-under-a-latency-budget) |
| Not required to complete everything (TODOs) | Finish the core properly; list the rest honestly | [Not done (TODO)](#not-done-todo) |

### Assumptions I made

- **A catalog, not patient data.** Nothing stored is PHI; prescriptions would be a separate,
  PHI-scoped resource.
- **Identity is the clinical drug** (ingredient, strength, form, as in RxNorm), with one
  representative NDC per entry. Multiple NDCs would come from a `/products` child resource.
- **Dosage means product strength** (for example 250 mg per 5 mL), not the sig, the patient's
  directions for use.

### Questions I'd ask the team

- Where does the catalog come from: manual entry, or import from a drug database?
- Do consumers need one entry per NDC (package level) rather than per clinical drug?
- Who calls this API, and which auth model do they already use?
- What latency target do price quotes need to meet?

### How I worked

- **Plan first:** a written plan of small vertical slices, with open questions and risks.
- **Small slices**, one commit each, so the git history mirrors the plan.
- **Core before extension:** CRUD, validation and persistence were done and tested before the
  price comparison.
- **Every commit reviewed by me**; [`AI_USAGE.md`](AI_USAGE.md) explains how the AI agent was used.

<!-- time spent: fill in -->
- Time spent: about 12 hours over three days.

## Quick start

**Docker, plus curl and jq for `make smoke`:**

```bash
make up      # builds the image; starts db → migrate → seed → api; waits until healthy (~45s cold)
make smoke   # end-to-end: auth, CRUD, ETags, pagination, prices, error cases; PASS/FAIL per step
make down    # stop and remove containers (keeps the data volume)
```

The API listens on `http://localhost:8080`. The local key is `local-dev-key-change-me`:

```bash
curl -s -H 'X-API-Key: local-dev-key-change-me' 'localhost:8080/v1/medications?limit=3' | jq
```

**With Go 1.27 (for development):**

```bash
cp .env.example .env   # run/migrate/seed/dev refuse to start without it
make dev               # db-up, build, migrate, seed, then the API in the foreground
                       # Ctrl+C stops the API gracefully; `make stop` stops the database
```

`make up` and `make dev` both use port 8080, so run one at a time. `make up` refuses to start
if something else already answers there. A local API bound to `[::]:8080` and Docker bound to
`127.0.0.1:8080` can coexist without error, and `localhost` would then quietly reach the wrong one.

**Postman/Insomnia:** import `openapi.yaml` and set the `X-API-Key` header.

**VS Code users:** `requests.http` targets the JetBrains HTTP Client: its response handlers
capture IDs and ETags and assert status codes. Use `make smoke` or curl instead.

`make help` lists every target. The main ones:

| Target | What it does |
|---|---|
| `up` / `down` / `logs` | Full Docker stack |
| `dev` / `stop` | Local development with Go; `stop` keeps the data volume |
| `db-up`, `migrate`, `seed`, `run` | The individual steps of `dev` |
| `smoke` | End-to-end check against a running API (`BASE_URL`, `API_KEY`) |
| `check` | Everything CI runs except integration: fmt, vet, lint, govulncheck, `test -race` |
| `test-integration` | Real PostgreSQL via testcontainers (needs Docker) |
| `bench`, `fuzz` | Benchmarks (`b.Loop`) and fuzzing of the decimal parser |

## API at a glance

| Method & path | Notes |
|---|---|
| `POST /v1/medications` | 201 + `Location` + `ETag`; 409 if an active entry has the same identity or NDC |
| `GET /v1/medications` | `name` (substring), `form`, `limit` (1–100), `cursor`; newest first; `Link: rel="next"` |
| `GET /v1/medications/{id}` | `ETag`; `If-None-Match` → 304 |
| `PUT /v1/medications/{id}` | Full replace; `If-Match` **required** (428 if missing, 412 if stale) |
| `DELETE /v1/medications/{id}` | Soft delete, 204; `If-Match` optional (412 if given and stale) |
| `GET /v1/medications/{id}/prices?quantity=N` | Simulated price comparison (see below) |
| `GET /healthz`, `/readyz`, `/version` | Public: liveness, readiness, build info |

A medication:

```json
{
  "id": "019a9c3e-7a10-7000-8000-000000000001",
  "name": "Amoxicillin",
  "dosage": { "value": 250, "unit": "mg", "per_value": 5, "per_unit": "mL" },
  "form": "liquid",
  "ndc": "00093-4155-73",
  "route": "oral",
  "version": 1,
  "created_at": "2026-11-20T21:14:09Z",
  "updated_at": "2026-11-20T21:14:09Z"
}
```

Errors are RFC 9457 `application/problem+json` with a `request_id` that matches the
`X-Request-ID` header and the server logs. Validation errors list **every** invalid field in
`errors[]`, so a client can fix them all in one round trip:

- **400** when the request has the wrong shape: malformed JSON, unknown fields, bad query parameters.
- **422** when the shape is right but the content is invalid.
- **415** when `Content-Type` is not JSON, **413** when the body is too large.
- **503** with `Retry-After` when the request deadline or a database statement timeout expires.

## Domain model

- **Identity is the clinical drug:** name + strength + dose form, the same granularity as an
  RxNorm *clinical drug* (ingredient + strength + dose form). A partial unique index enforces
  one active entry per identity (name compared case-insensitively).
- **`ndc` is a representative NDC.** One clinical drug is sold under many NDCs, one per
  manufacturer and package. The catalog stores one, normalized to 11 digits and unique among
  active entries. A `/v1/medications/{id}/products` child resource holding every NDC is a TODO.
- **`dosage` means product strength** ("500 mg", "250 mg / 5 mL"), not a patient's dose or
  directions (the *sig*). Amounts are exact decimals (`NUMERIC(12,4)` in the database, a
  fixed-point type in Go, `json.Number` on the wire), never floats.
- **Catalog, not patient data.** Nothing here is PHI. Per-patient prescriptions would be a
  separate resource with PHI, and therefore HIPAA, in scope: access control per patient,
  audit trails, encryption and retention policies. This project makes no HIPAA-compliance claim.
- Public standards only: NDC, RxNorm (`rxcui`) and a route-of-administration enum.
  - The NDCs and RxCUIs in the seed data are format-valid illustrations, not looked up in the
    FDA NDC Directory or RxNorm.
  - TODOs: DEA schedule, Rx/OTC status, and importing from a public drug database.

## Why this extension: price comparison under a latency budget

In e-prescribing, the useful moment to show a drug's cash price is while the prescription is
being written or while the patient is at the pharmacy counter, and neither moment waits. The analogy I had in mind is an ad exchange:

- The request fans out to every bidder at once.
- The auction closes at a fixed deadline.
- Whoever has not answered by then is simply not in it.

`GET /v1/medications/{id}/prices?quantity=30` does the same with pricing networks:

- **Fan-out under one total budget** (`PRICING_BUDGET`, 300 ms by default). One slow network
  costs at most the budget, never more, even if it ignores cancellation.
- **One answer per network, collected over a channel sized to the number of networks.** A
  network that answers late can still send and exit, so no goroutine leaks. A test proves it:
  making the channel unbuffered makes that test fail.
- **Missing networks are reported, not hidden.** The response lists `quotes` (cheapest first),
  `best`, and `unavailable` with a reason of `timeout` or `error`. If no network answers, it is
  a 503 with `Retry-After`. Prices are int64 cents with `currency: "USD"`, and responses carry
  `Cache-Control: no-store`.
- **The default networks are tuned to show the trade-off.** The cheapest network takes 450 ms,
  so it always misses the 300 ms budget: a cheaper price that arrives too late is worth nothing.
- **Tested deterministically** with `testing/synctest` (virtual time, so the 300 ms budget is
  exact and instant), plus `ExampleComparer_BestPrice` and `b.Loop` benchmarks.

**The prices are simulated.**

- No real pricing source is contacted. Each price is derived from a hash of the NDC and the
  quantity, and every response says `"simulated": true`.
- Real cash prices are set per NDC (per product and package). This demo prices the
  catalog's representative NDC.
- A medication without an NDC gets a 422.

## Project layout

```
cmd/api/                  entry point: serve, migrate, seed, healthcheck; route table
internal/medication/      catalog domain: model, validation, service, HTTP handlers, PostgreSQL repository
internal/pricing/         price comparison: network interface, simulated networks, fan-out comparer, handler
internal/seed/            sample catalog for local development and demos
internal/platform/
  admin/                  pprof on a separate, opt-in listener
  auth/                   static API keys, constant-time comparison
  buildinfo/              version and VCS stamp from the Go build metadata
  config/                 environment configuration, validated at startup
  db/                     pgx connection pool and goose migrations
  dbtest/                 throwaway PostgreSQL container for integration tests
  health/                 liveness and readiness probes
  httpx/                  server, strict JSON decoding, problem+json, ETags
  logging/                structured JSON logger
  middleware/             request ID, access log, recover, request deadline, body limit
  requestid/              request ID generation and context propagation
migrations/               SQL migrations, embedded into the binary
scripts/smoke.sh          end-to-end check with curl and jq
openapi.yaml              API contract (OpenAPI 3.1)
requests.http             walkthrough for the JetBrains HTTP Client
```

## Design decisions and trade-offs

**HTTP layer**
- **Standard library `net/http`** with Go 1.22+ method patterns. A framework would add little
  for nine operations. The middleware chain, request ID → access log → recover → auth →
  request deadline → body limit, is a handful of small, separately tested functions (about 230 lines including auth).
- **Strict decoding:** unknown fields are rejected (a typo such as `"dosge"` must not be
  silently ignored), numbers never pass through `float64`, and the body must be exactly one
  JSON object.

**Concurrency and data integrity**
- **Optimistic concurrency with strong ETags** (`"<version>"`). The check and the write are a
  single `UPDATE … WHERE version = ANY($2)`, so of two writers holding the same ETag exactly
  one wins; an integration test races ten of them.
  - `If-Match` is required on PUT, because a lost update is silent data loss.
  - It is optional on DELETE, because deleting stale data loses nothing.
- **Soft delete:** deleted rows stay for audit, and a delete bumps the version so any
  outstanding ETag goes stale.
  - The unique indexes are partial (`WHERE deleted_at IS NULL`), so a deleted product can be
    created again; it gets a new ID.
  - TODOs: a restore endpoint and a retention purge.
- **Duplicates are detected by the unique index, not by a lookup first**, which avoids a
  check-then-insert race. A duplicate POST returns 409, and PostgreSQL logs one
  `ERROR: duplicate key value violates unique constraint` line for it. That line is expected:
  it is the atomic duplicate check working, not a failure. The seed command uses
  `INSERT … ON CONFLICT DO NOTHING` instead, so re-running it leaves no such lines.
- **IDs are UUIDv7:** time-ordered, so inserts stay near the end of the B-tree index and
  "newest first" is simply `ORDER BY id DESC`.

**Listing**
- **Keyset pagination** (`id < cursor`, with one extra row fetched to detect another page)
  stays fast at any depth and stable under concurrent inserts, unlike `OFFSET`.
- **Opaque cursor bound to its filters:** the cursor is base64url and carries a fingerprint of
  the filters, so reusing it with different filters is a 400, not a silently wrong page.
- **Name search:** `ILIKE '%term%'` backed by a `pg_trgm` GIN index.
- **Dynamic SQL in `List`** rather than `($1 IS NULL OR …)`, so the partial indexes stay
  usable. An integration test runs `EXPLAIN` on 20k rows to confirm the indexes are used.

**Schema and validation**
- **Migrations are embedded and run as a separate step** (`api migrate`), never by every
  replica on startup in production; `MIGRATE_ON_START` exists for local use only.
- **Index builds use `CREATE INDEX CONCURRENTLY`**, so they don't block writes.
- **The database mirrors the domain rules:** CHECK constraints enforce the same rules, so a
  bad row can't get in even by bypassing the API.
- **Validation errors are complete:** all invalid fields are reported together, by API path
  (e.g. `dosage.per_unit`).
- **Text normalization:** names are NFC-normalized, units canonicalized (`MG` → `mg`,
  `ug` → `mcg`), and NDCs accepted in every published layout.

**Considered and not used**
- **No PATCH yet.** PUT with full replacement is simpler to reason about with ETags; JSON Merge
  Patch is a TODO.
- **sqlc.** One table, and the one query that matters (`List`) is built dynamically on
  purpose. sqlc's static queries would need the `($1 IS NULL OR col = $1)` pattern, which
  hides the partial indexes from the planner under generic plans. Hand-written SQL in a single
  file (`postgres.go`) is easy to review at this size; with more tables, sqlc would earn its keep.
- **PGO (profile-guided optimization).** It needs a CPU profile from representative production
  traffic, which a new service does not have. The workload is mostly waiting on PostgreSQL and
  pricing networks, where PGO gains little. The pprof listener is there to collect that
  profile once there is real traffic.

## Operations

**Configuration.** All settings come from environment variables; see
[`.env.example`](.env.example) for the full list with defaults.
- Startup fails, listing every invalid variable at once, if anything is wrong.
- Secrets (`DATABASE_URL`, `API_KEYS`) are redacted in every print path, including logs,
  `%v`/`%+v`/`%#v` and JSON.
- Cross-checks: `PRICING_BUDGET` < `REQUEST_TIMEOUT` < `HTTP_WRITE_TIMEOUT`, and `ADMIN_ADDR`
  must differ from `HTTP_ADDR`.

**Timeouts.** Each layer ends before the one outside it, so the client always gets an answer:
- `DB_STATEMENT_TIMEOUT` (5 s) stops a single query on the server.
- `REQUEST_TIMEOUT` (10 s) is a deadline on all the work behind one authenticated request.
  When it, or the statement timeout, expires, the API answers 503 problem+json with
  `Retry-After: 1`.
- `HTTP_WRITE_TIMEOUT` (15 s) is the last resort: past it, the connection is dropped.

**Authentication.**
- **Header and challenge:** clients send `X-API-Key`; keys are configured as `API_KEYS=id:key,…`
  and must be at least 16 characters. A missing or wrong key is a 401 with
  `WWW-Authenticate: ApiKey realm="medication-api", header="X-API-Key"`.
  - The scheme is not `Bearer`, which promises an OAuth 2.0 token and is kept free for the
    OAuth TODO.
- **Comparison:** keys are compared as SHA-256 digests with `crypto/subtle`, checking every
  key without stopping early, so timing reveals neither a key's length nor which key matched.
- **Deny by default:** only `/healthz`, `/readyz` and `/version` are public, matched exactly.
  - Unknown paths get 401 rather than 404, so unauthenticated callers can't map the routes.
  - An empty `API_KEYS` rejects everything and logs a warning at startup.
- **Logging:** the access log records the key's ID (`key_id`), or `auth_failure` with a reason
  of `missing` or `invalid`, never the key itself.
- TODOs: OAuth 2.0 client credentials or mTLS for partners, key rotation without a restart,
  per-key scopes (where 403 would come in), and per-key rate limits.

**Probes and shutdown.**
- `/healthz` never checks dependencies, so a database outage doesn't restart the pod.
- `/readyz` pings the database with an 800 ms timeout, below the 1 s Kubernetes default.
- On SIGTERM, `/readyz` starts failing first. After `SHUTDOWN_DRAIN_DELAY` (5 s) the server
  stops accepting connections, then drains in-flight requests for up to `SHUTDOWN_TIMEOUT` (20 s).
- A second signal exits immediately.
- Compose gives the API container `stop_grace_period: 25s`, because Docker's default 10 s would
  cut the drain short.

**Database.**
- **Server-side timeouts:** every pooled connection sets `statement_timeout` (5 s) and
  `idle_in_transaction_session_timeout`, so a runaway query is stopped by PostgreSQL, not just
  abandoned by the client.
- **Separate migration pool:** migrations use their own short-lived pool with no statement
  timeout and a 10 s `lock_timeout`, and goose's advisory lock keeps concurrent migrators safe.
- **Identifiable connections:** `application_name` makes the service's connections visible in
  `pg_stat_activity`.

**Logs.**
- JSON (`log/slog`), with a `request_id` on every line.
- The access log records method, path, status, bytes and duration. Bodies, query strings and
  headers are never logged.
- Probes are logged at debug level, so they don't drown real traffic.
- Levels follow who has to act. 5xx responses are `ERROR`. A client that disconnects
  mid-request is recorded as status `499` at `INFO`, since nothing failed on our side. A pricing
  network that misses the budget is `DEBUG` (the budget working as designed); one that returns
  an error is `WARN`.

**Build info.**
- `GET /version` and the startup log report the module version, commit, commit time, a
  dirty-tree flag and the Go version.
- All of it comes from the metadata the Go toolchain embeds at build time
  (`runtime/debug.ReadBuildInfo`); no `-ldflags` needed. The Docker build keeps `.git` in the
  build stage so the commit stamp survives.

**Profiling.**
- pprof runs on a separate listener at `ADMIN_ADDR` (e.g. `127.0.0.1:6060`). It is off by
  default and uses a private mux, so the public port never serves `/debug/pprof` (it returns 404).
- It has no authentication: bind it to loopback or a private network only.

**Container.**
- Multi-stage build to `distroless/static:nonroot`: about 9.7 MB, no shell, runs as UID 65532.
- Distroless has no curl, so the healthcheck runs the binary itself (`api healthcheck`), which
  needs nothing but `HTTP_ADDR`.

**CI** (`.github/workflows/ci.yml`) has three jobs:
- `make check`, including govulncheck;
- integration tests against real PostgreSQL;
- an end-to-end job that runs `make up` and then `make smoke`.

**Seed data:** `api seed` / `make seed` inserts 16 sample entries. It is idempotent (existing
entries are skipped through the unique indexes) and is never part of `serve` or migrations. It
only runs when invoked by name, so a production catalog cannot receive sample data by accident.

## Testing

- **Unit tests** for the domain, handlers, middleware, config, auth and pricing.
- **Integration tests** (`-tags=integration`) against real PostgreSQL 18 in testcontainers:
  - constraints, partial indexes and `EXPLAIN` plans;
  - concurrent writers holding the same ETag (exactly one wins);
  - a delete racing an update;
  - migrations applied on a populated database;
  - seeding that leaves no `ERROR` lines in the PostgreSQL log.
- **`testing/synctest`** for the pricing fan-out: exact virtual timings, and leak detection.
- **Fuzzing** of the decimal parser's round trip.
- **Contract test:** every operation in `openapi.yaml` must be routed, documented paths may
  not serve undocumented methods, and the spec's public operations must match the auth
  middleware's public paths.
- **`make smoke`:** the same end-to-end flow against any running instance; CI runs it against
  the Docker stack.
- **`make openapi-lint`:** lints `openapi.yaml` with a pinned Redocly CLI (needs Node.js). It is
  a manual step, not part of `make check` or CI. [`redocly.yaml`](redocly.yaml) turns off three
  rules, each with its reason.

## Go tooling

| Tool | Why |
|---|---|
| Go 1.27, `net/http` `ServeMux` | Method and path patterns in the standard library; no framework needed |
| `log/slog` | Structured JSON logs; a small wrapper handler adds `request_id` from the context |
| `jackc/pgx/v5` + `pgxpool` | Native PostgreSQL driver: exact NUMERIC as text, typed errors (unique violations), connection pool |
| `pressly/goose/v3` | Migrations embedded in the binary, advisory lock, `NO TRANSACTION` for concurrent index builds |
| `google/uuid` | UUIDv7: time-ordered IDs |
| `golang.org/x/text/unicode/norm` | NFC normalization, so visually identical names compare equal |
| `testcontainers-go` | Integration tests against the same PostgreSQL image as compose |
| `testing/synctest` | Deterministic tests of timeouts and goroutine lifecycles in virtual time |
| `go test -race`, fuzzing, `b.Loop`, `Example` tests | Data races, parser robustness, benchmarks, executable docs |
| `runtime/debug.ReadBuildInfo` | `/version` and the startup log without build-time flags |
| `net/http/pprof` | Profiling on the admin listener |
| `golangci-lint` v2 (pinned, run via `go run`) | One linter run including gosec, errorlint, bodyclose, sqlclosecheck, contextcheck |
| `govulncheck` (pinned) | Reports only vulnerabilities in code that is actually reachable |
| `go.yaml.in/yaml/v3` (tests only) | Parses `openapi.yaml` for the contract test |

## Not done (TODO)

- `Idempotency-Key` on POST, so a retried request replays the original response instead of getting 409.
- `ndc` and `rxcui` list filters (the NDC is already indexed), and PATCH with JSON Merge Patch.
- The `/products` child resource for multiple NDCs; a restore endpoint and retention purge.
- Metrics and tracing (OpenTelemetry, served on the admin listener) and rate limiting.
- OAuth 2.0 / mTLS, key rotation, per-key scopes and rate limits.
- Import from public drug databases; DEA schedule; Rx/OTC status.

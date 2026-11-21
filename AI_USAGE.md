# AI usage

This project was built with **Claude Code** (Anthropic's coding agent, Claude Opus model) as a
pair programmer. The agent wrote most of the code. The scope, the decisions, the review and
every commit were mine. This file explains how that worked and where it went wrong.

Commit messages carry no AI attribution; that was my choice, and this file is the disclosure instead.

## How I directed the agent

The same loop for every part of the project:

1. **Spec first.** I wrote the architecture decisions before any code: stdlib `net/http`,
   PostgreSQL with pgx, layered packages, problem+json errors, optimistic concurrency, soft
   delete, keyset pagination, and what "production-ready" had to include.
2. **Plan, no code.** The agent proposed the work as small vertical slices, with open questions
   and risks, and stopped for my approval.
3. **I answered the open questions** with decisions, so ambiguity was resolved by me, not filled
   in by the agent.
4. **One slice at a time.** Each slice ended with the checks (gofmt, vet, lint, `go test -race`,
   integration tests), a diff summary, an explanation and a suggested commit message. Then it
   stopped.
5. **I reviewed, ran it by hand, and committed it myself.** One commit per slice; the agent
   never committed or pushed.

Standing rules I gave it: English only; never commit or push; no AI attribution; comments
explain *why*, never *what*; public standards only (NDC, RxNorm); never claim HIPAA compliance.

## Who decided what

**Mine:**
- Scope and the cut line: what is built and what is a TODO.
- The domain model: catalog identity at RxNorm's clinical-drug level, a representative NDC,
  and `dosage` as product strength. This comes from my experience with e-prescribing.
- Adding NDC, RxNorm and route as a **new** migration instead of editing an applied one.
- The price-comparison extension and its framing (my ad-exchange analogy).
- The Go tooling to showcase: `testing/synctest`, `b.Loop`, `Example` tests, govulncheck,
  pprof, build info.
- Developer tooling after testing by hand: the seed command, `requests.http`, `make smoke`,
  `make dev`.

**Proposed by the agent during plan review, accepted by me:**
- Concentration dosages (`per_value`/`per_unit`): "500 mg" does not describe a syrup.
- Name normalization and a case-insensitive unique index, so near-duplicates are caught.
- Real API-key auth instead of the stub I had planned.
- Migrations as a separate `migrate` step instead of running on startup.
- Strong ETags with `If-Match` required on PUT (428 when missing).
- A cursor bound to its filters, and readiness failing before shutdown.

## How the output was checked

- **Tests that prove what they claim.** Where a test guards a property, the code was broken on
  purpose to see the test fail. Example: making the pricing result channel unbuffered makes the
  leak test fail with `blocked goroutines remain`.
- **Real infrastructure.** Integration tests run against PostgreSQL 18 in testcontainers:
  concurrent writers, `EXPLAIN` checks that the indexes are used, migrations on a populated
  database.
- **By hand, after every slice.** I ran `requests.http` in GoLand and `make smoke` against the
  binary and later the Docker stack. "Done" meant I had seen it run.
- **External linting.** Redocly on `openapi.yaml` found a YAML error that the agent's checks
  missed (below); `make openapi-lint` keeps it repeatable.

## Where it went wrong

**Caught by the guardrails**
- **A destructive command.** One long shell command from the agent ended with a stray
  `mv .env.example /dev/null`, which would have deleted a tracked file. Claude Code's
  permission check blocked the whole command, so nothing ran. The agent confirmed the file was
  intact and reported it. Lesson: long compound commands hide mistakes, so I keep the
  guardrails on.
- **Testing the wrong process.** A local API on IPv6 and the Docker container on IPv4 were both
  on port 8080, so `localhost` reached the local one and a smoke run tested the wrong process.
  The `/version` endpoint exposed it (an old commit). The run was redone over IPv4, and
  `make up` now refuses to start if port 8080 is already taken.

**Caught by me**
- **Commit messages with AI attribution** in the first suggestions. Removed, and made a rule.
- **A request in `requests.http` priced a medication deleted earlier in the file**, so it got
  404 instead of 422. Found by my manual run; fixed and re-run until every assertion passed.
- **A slice started before my plan update**, so the new fields were missing. I committed it as
  it was and added the fields in a follow-up migration.
- **Slice 9 was still uncommitted when slice 10 started**, mixing both in the working tree. I
  stopped it and had the agent separate them, so each slice is still its own commit.
- **Noisy seed logs.** Re-running the seed made PostgreSQL log 16 duplicate-key errors, which
  looks broken to a reviewer. The seed now uses `ON CONFLICT DO NOTHING`.
- **The OpenAPI YAML error.** An unquoted description with a comma inside a flow mapping had
  silently become an extra property. The contract test only checks routes and security, so it
  could not see it.

## Not yet verified

- **CI has not run on GitHub** (no remote yet). The workflow passes `actionlint`, and every
  job runs the same `make` targets that pass locally.
- **The OpenAPI linter is not in CI**, because it needs Node.js. `make openapi-lint` runs it
  locally.
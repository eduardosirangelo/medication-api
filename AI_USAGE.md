# AI usage

This project was built with **Claude Code** (Anthropic's coding agent, Claude Opus model) as a
pair programmer. This file says what the agent did, what I decided, how the output was
checked, and what went wrong along the way.

Commit messages carry no AI attribution; that was my choice, and this file is the disclosure instead.

## How the work was split

**I decided:**
- The scope, the plan and every design decision. The main ones:
  - exact decimal dosages;
  - catalog identity at the clinical-drug level, with a representative NDC;
  - real API-key auth that denies by default;
  - `If-Match` required on PUT and optional on DELETE;
  - substring search backed by `pg_trgm`;
  - PUT only (PATCH left as a TODO);
  - catalog only, with no PHI in scope.
- The price-comparison extension and its framing: e-prescribing, plus my ad-exchange analogy
  for the latency budget.
- Developer tooling: the seed command, the smoke script, `make dev` and `make up`.
- Code style: names and small functions carry the meaning, and comments explain only *why*.

**The agent:**
- Proposed the plan as small vertical slices and listed open questions and risks.
- Implemented one approved slice at a time.
- Ran the checks after each slice (gofmt, `go vet`, `go test -race`, integration tests, lint,
  plus a manual run against PostgreSQL), then summarized the diff and suggested a commit message.

**Review and commits.** I reviewed every slice, asked for changes, and made every commit
myself. The agent never committed or pushed.

## How the output was checked

- **Tests that prove what they claim.** Where a test guards a specific property, the agent
  broke the code on purpose to confirm the test fails:
  - The pricing leak test fails with `blocked goroutines remain` when the result channel is
    made unbuffered.
  - The OpenAPI contract test catches each of three deliberate spec mutations: a removed
    operation, a wrong security setting, and a renamed path.
- **Real infrastructure.** Integration tests run against PostgreSQL 18 in testcontainers,
  including:
  - races between concurrent writers;
  - `EXPLAIN` checks that the indexes are used;
  - migrations applied on a populated database.
- **Manual runs after every slice.** The binary was run against compose PostgreSQL, and later
  the full Docker stack, with `scripts/smoke.sh` covering the whole flow.
- **Claims checked against code.** While writing the docs, the agent re-checked statements
  against the code and fixed two that were wrong:
  - the response type of `dosage.value` (a JSON number, not a string);
  - an overstated "about 100 lines" for the middleware.

## Incidents

### 1. A destructive command blocked by the guardrail

**What happened.** During slice 9 (auth), the agent composed one long shell command to update
`.env.example`, the Makefile, `requests.http` and the smoke script. Tacked onto it was a stray
`mv .env.example /dev/null`, which would have deleted a tracked file. It served no purpose.

**How it was stopped.** Claude Code's auto-mode permission classifier blocked the **whole**
command as a destructive action, so nothing in it ran. The agent then:
- confirmed with `git status` and `ls` that `.env.example` was intact and unmodified;
- reran the edits without the stray part, each replacement asserting its exact match first;
- reported the mistake to me in the slice summary rather than leaving it buried in the
  tool log.

**Lesson.** The guardrail did its job. The agent's own review of a long, mechanical command
did not.
- Long compound commands hide mistakes.
- Destructive operations need a second, independent check.
- "Verify, then report" after a blocked action is the right reflex.

### 2. The IPv4/IPv6 trap: verifying the wrong process

**Symptom.** While verifying slice 10 (Docker), `GET /version` on `localhost:8080` reported an
old commit, although the image had just been rebuilt.

**Diagnosis.** The agent narrowed it down step by step instead of assuming a cause:
1. It suspected Docker's build cache and inspected the build steps; they were cached.
2. It extracted the binaries from the image and from the running container and read their
   VCS stamp with `go version -m`. Both carried the **new** commit, so the image was fine.
3. It ran `lsof -nP -iTCP:8080 -sTCP:LISTEN`, which showed two listeners:
   - Docker on `127.0.0.1:8080` (IPv4);
   - a local `./bin/api` on `*:8080` (IPv6), most likely left over from a `make dev` session.

   macOS allows both binds without an error, and `curl localhost` tries `::1` first, so the
   requests went to the local process.
4. `curl -4` and `curl -6` against the same URL confirmed it: two different commits.

**Consequences and fix.**
- **The earlier check was invalid.** The smoke run the agent had reported as "against the
  container" in that same turn had actually tested the local process. The agent said so
  explicitly and reran the tests over IPv4 (`BASE_URL=http://127.0.0.1:8080`). The container's
  own logs showed the requests' key IDs, proving they reached it this time.
- **`make up` now refuses to start** when something other than the stack's API already answers
  on port 8080, with a message naming the likely cause.
- **The other process was left alone.** The agent had not started it, so it did not kill it.

### Other actions the guardrail blocked

The classifier also denied two verification steps that would have downloaded and run
third-party code:
- pulling the JetBrains HTTP Client Docker image to run `requests.http`;
- running an npm-based OpenAPI linter.

The agent did not look for a way around either block. Instead:
- it checked each `requests.http` expectation with curl;
- it validated the spec with the Go contract test plus a temporary local check that every
  `$ref` resolves and every component is used.

Running those external tools remains a manual step:
`npx @redocly/cli lint openapi.yaml`, or opening `requests.http` in GoLand.

## Mistakes I caught

- **A walkthrough step used a deleted resource.** A step added to `requests.http` for slice 8.5
  priced the medication deleted earlier in the walkthrough, so it got a 404 instead of the
  intended 422.
  - The agent had validated that file with curl in an earlier slice but not this new step.
  - The smoke script, which covers the same flow, did not have the bug.
  - Fixed by capturing the re-created entry's ID into its own variable, then auditing every
    variable in the file.
- **Two slices mixed in one working tree.** The agent started slice 10 while slice 9 was still
  uncommitted, so changes from both ended up in the same files. When I stopped it, the agent:
  - rebuilt a slice-9-only tree in a scratch directory;
  - verified that tree on its own (build, vet, race tests, lint, and a smoke run);
  - produced a patch I could stage with `git apply --cached`, so slice 9 could be committed
    separately without touching my working tree.

## What remains unverified

- **CI has not run on GitHub yet** (no remote). The workflow passes `actionlint`, and every job
  runs the same `make` targets that pass locally.
- **No external OpenAPI linter has been run** (see above).

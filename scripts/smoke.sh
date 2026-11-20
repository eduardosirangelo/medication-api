#!/usr/bin/env bash
# End-to-end smoke test against a running API: create → get → 304 → list →
# replace (200/412/428) → delete → 404 → re-create → prices → error cases.
# Usage: BASE_URL=http://localhost:8080 scripts/smoke.sh   (needs curl and jq)
set -uo pipefail

BASE_URL=${BASE_URL:-http://localhost:8080}
API_KEY=${API_KEY:-dev-local-key} # placeholder until slice 9 enforces it
NAME="Smoke Amoxicillin $(date +%s)-$$" # unique, so the script can be re-run
BODY=$(mktemp) HEADERS=$(mktemp)
trap 'rm -f "$BODY" "$HEADERS"' EXIT
failures=0

# request METHOD PATH [curl args...]: sets $status; body and headers land in files.
request() {
  local method=$1 path=$2; shift 2
  status=$(curl -sS -o "$BODY" -D "$HEADERS" -w '%{http_code}' -X "$method" \
    -H "X-API-Key: $API_KEY" "$@" "$BASE_URL$path") || status=000
}

header() { grep -i "^$1:" "$HEADERS" | head -1 | cut -d' ' -f2- | tr -d '\r'; }

# expect WANT STEP: one PASS/FAIL line per step.
expect() {
  if [[ $status == "$1" ]]; then
    printf 'PASS  %-40s %s\n' "$2" "$status"
  else
    printf 'FAIL  %-40s got %s, want %s\n' "$2" "$status" "$1"
    head -c 300 "$BODY"; echo
    failures=$((failures + 1))
  fi
}

json() { jq -n --arg name "$NAME" --argjson value "$1" \
  '{name: $name, form: "liquid", route: "oral",
    dosage: {value: $value, unit: "mg", per_value: 5, per_unit: "mL"}}'; }

# NDCs are unique among active entries, so each run derives a fresh one.
NDC=$(printf '%011d' $(( ($(date +%s) * 1000 + $$ % 1000) % 100000000000 )))

request GET /healthz;                                   expect 200 "liveness"
request GET /readyz;                                    expect 200 "readiness"

request POST /v1/medications -H 'Content-Type: application/json' -d "$(json 250)"
expect 201 "create"
id=$(jq -r .id "$BODY") etag=$(header ETag)

request GET "/v1/medications/$id";                      expect 200 "get"
request GET "/v1/medications/$id" -H "If-None-Match: $etag"
expect 304 "get with If-None-Match"

request GET "/v1/medications?name=smoke%20amox&form=liquid&limit=1"
expect 200 "list with filters"
[[ $(jq -r '.data[0].id' "$BODY") == "$id" ]] || { echo "FAIL  list did not return the new item"; failures=$((failures + 1)); }

request PUT "/v1/medications/$id" -H 'Content-Type: application/json' -H "If-Match: $etag" -d "$(json 125)"
expect 200 "replace with current ETag"
stale=$etag etag=$(header ETag)

request PUT "/v1/medications/$id" -H 'Content-Type: application/json' -H "If-Match: $stale" -d "$(json 100)"
expect 412 "replace with stale ETag"
request PUT "/v1/medications/$id" -H 'Content-Type: application/json' -d "$(json 100)"
expect 428 "replace without If-Match"

request DELETE "/v1/medications/$id" -H "If-Match: $etag"; expect 204 "delete"
request GET "/v1/medications/$id";                      expect 404 "get after delete"
request DELETE "/v1/medications/$id";                   expect 404 "delete again"

request POST /v1/medications -H 'Content-Type: application/json' -d "$(json 125)"
expect 201 "re-create after delete"
recreated=$(jq -r .id "$BODY")
request POST /v1/medications -H 'Content-Type: application/json' -d "$(json 125)"
expect 409 "duplicate"
request GET "/v1/medications/$recreated/prices?quantity=30"
expect 422 "prices without NDC"

request POST /v1/medications -H 'Content-Type: application/json' \
  -d "$(json 500 | jq --arg ndc "$NDC" '.name += " priced" | .ndc = $ndc')"
expect 201 "create with NDC"
priced=$(jq -r .id "$BODY")
request GET "/v1/medications/$priced/prices?quantity=30"
expect 200 "prices (simulated fan-out)"
jq -e '.simulated and .best.price_cents == .quotes[0].price_cents' "$BODY" >/dev/null ||
  { echo "FAIL  prices body: $(head -c 300 "$BODY")"; failures=$((failures + 1)); }
request GET "/v1/medications/$priced/prices?quantity=0"
expect 400 "prices with invalid quantity"

request POST /v1/medications -H 'Content-Type: text/plain' -d 'name=x'
expect 415 "wrong content type"
request POST /v1/medications -H 'Content-Type: application/json' -d '{"name": ""}'
expect 422 "invalid content"
request POST /v1/medications -H 'Content-Type: application/json' -d '{"name":'
expect 400 "malformed JSON"
request GET "/v1/medications?colour=red";               expect 400 "unknown query parameter"

if ((failures > 0)); then echo "$failures step(s) failed"; exit 1; fi
echo "all steps passed"

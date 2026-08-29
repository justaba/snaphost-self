#!/usr/bin/env bash
set -uo pipefail

# External probe of the SnapHost production surfaces. Runs from CI, but is
# deliberately a plain script so an operator can run the identical checks from
# a laptop during an incident.
#
#   .github/scripts/uptime-check.sh
#
# Every check is an invariant that fails loudly if a whole layer is down, and
# each one is chosen because it exercises something the previous one does not:
#
#   1. api /health          — the gateway container answers at all
#   2. api authenticated 401 — JWT middleware is running, not just the health
#                              route that is registered before all middleware
#   3. dashboard 200        — Caddy and the static release symlink are intact
#   4. unknown deploy host  — the full runtime path: DNS, the Yandex API
#      404                    Gateway, router-svc, and its route lookup back
#                              into user-billing. A 502/504 here means the
#                              router is down; a 200 would mean it resolved a
#                              host that does not exist.
#   5. certificate expiry   — renewal is automatic and therefore silent when
#                              it breaks.

API_BASE=${SNAPHOST_MONITOR_API:-https://api.snaphost.ru}
DASHBOARD=${SNAPHOST_MONITOR_DASHBOARD:-https://snaphost.ru}
DEPLOY_SUFFIX=${SNAPHOST_MONITOR_DEPLOY_SUFFIX:-snaphost.pw}
CERT_MIN_DAYS=${SNAPHOST_MONITOR_CERT_MIN_DAYS:-14}
ATTEMPTS=${SNAPHOST_MONITOR_ATTEMPTS:-3}
RETRY_DELAY=${SNAPHOST_MONITOR_RETRY_DELAY:-10}
TIMEOUT=${SNAPHOST_MONITOR_TIMEOUT:-20}

# A hostname that cannot ever be a real deploy, so the expected answer stays
# 404 no matter what is deployed.
UNKNOWN_DEPLOY_HOST="proj-monitor-probe-does-not-exist.${DEPLOY_SUFFIX}"

FAILURES=()
REPORT=()

record_ok() { REPORT+=("PASS  $1"); }
record_fail() { REPORT+=("FAIL  $1 — $2"); FAILURES+=("$1: $2"); }

# Retries because a single transient blip is not an outage; a real outage
# still fails all attempts.
http_status() {
  local url=$1 attempt status
  for ((attempt = 1; attempt <= ATTEMPTS; attempt++)); do
    status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
      --max-time "$TIMEOUT" "$url" 2>/dev/null) || status=000
    if [[ "$status" != 000 ]]; then
      printf '%s' "$status"
      return 0
    fi
    (( attempt < ATTEMPTS )) && sleep "$RETRY_DELAY"
  done
  printf '000'
}

check_status() {
  local label=$1 url=$2 expected=$3 actual attempt
  for ((attempt = 1; attempt <= ATTEMPTS; attempt++)); do
    actual=$(http_status "$url")
    if [[ "$actual" == "$expected" ]]; then
      record_ok "$label ($url -> $actual)"
      return 0
    fi
    (( attempt < ATTEMPTS )) && sleep "$RETRY_DELAY"
  done
  record_fail "$label" "expected HTTP $expected from $url, got ${actual:-no response}"
}

check_certificate() {
  local label=$1 host=$2 end_date end_epoch now_epoch days
  end_date=$(echo | openssl s_client -servername "$host" -connect "$host:443" 2>/dev/null \
    | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
  if [[ -z "$end_date" ]]; then
    record_fail "$label" "could not read a TLS certificate from $host"
    return
  fi
  end_epoch=$(date -u -d "$end_date" +%s 2>/dev/null) || {
    record_fail "$label" "could not parse the certificate expiry '$end_date' for $host"
    return
  }
  now_epoch=$(date -u +%s)
  days=$(( (end_epoch - now_epoch) / 86400 ))
  if (( days < CERT_MIN_DAYS )); then
    record_fail "$label" "the certificate for $host expires in $days days (threshold $CERT_MIN_DAYS)"
  else
    record_ok "$label ($host, $days days remaining)"
  fi
}

check_status 'api health'            "${API_BASE%/}/health"          200
check_status 'api rejects anonymous' "${API_BASE%/}/api/v1/deploys"  401
check_status 'dashboard'             "${DASHBOARD%/}/"               200
check_status 'runtime router'        "https://${UNKNOWN_DEPLOY_HOST}/" 404

check_certificate 'control-plane certificate' "${API_BASE#https://}"
check_certificate 'deploy certificate'        "$UNKNOWN_DEPLOY_HOST"

printf '%s\n' "${REPORT[@]}"

if (( ${#FAILURES[@]} > 0 )); then
  printf '\n%d check(s) failed\n' "${#FAILURES[@]}"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    {
      printf 'failed=true\n'
      printf 'summary<<EOF_SUMMARY\n'
      printf '%s\n' "${FAILURES[@]}"
      printf 'EOF_SUMMARY\n'
      printf 'report<<EOF_REPORT\n'
      printf '%s\n' "${REPORT[@]}"
      printf 'EOF_REPORT\n'
    } >>"$GITHUB_OUTPUT"
  fi
  exit 1
fi

printf '\nall %d checks passed\n' "${#REPORT[@]}"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    printf 'failed=false\n'
    printf 'report<<EOF_REPORT\n'
    printf '%s\n' "${REPORT[@]}"
    printf 'EOF_REPORT\n'
  } >>"$GITHUB_OUTPUT"
fi

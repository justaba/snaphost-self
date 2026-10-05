#!/usr/bin/env bash
set -Eeuo pipefail

# Exercise the pinned stock Caddy image with its internal CA. Public ACME still
# requires the separate VPS acceptance drill with operator-owned domains.
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
IMAGE='caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d'
TMP=$(mktemp -d)
ID=$$
NET=snaphost-edge-test-$ID
MOCK=snaphost-edge-mock-$ID
EDGE=snaphost-edge-caddy-$ID
cleanup() { docker rm -f "$EDGE" "$MOCK" >/dev/null 2>&1 || true; docker network rm "$NET" >/dev/null 2>&1 || true; rm -rf "$TMP"; }
trap cleanup EXIT

command -v openssl >/dev/null
docker image inspect "$IMAGE" >/dev/null
docker run --rm \
  -e SNAPHOST_CONTROL_DOMAIN=panel.control.test \
  -e SNAPHOST_ACME_EMAIL=acme@control.test \
  -v "$ROOT/infra/Caddyfile.production.example:/etc/caddy/Caddyfile:ro" \
  "$IMAGE" caddy adapt --config /etc/caddy/Caddyfile --adapter caddyfile 2>/dev/null |
  python3 -c '
import json, sys
d = json.load(sys.stdin)
tls = d["apps"]["tls"]["automation"]
policies = tls["policies"]
assert tls["on_demand"]["permission"]["endpoint"] == "http://snaphost:8082/tls/ask"
assert any(p.get("on_demand") and not p.get("subjects") for p in policies)
assert not any(any(s.startswith("*.") for s in p.get("subjects", [])) for p in policies)
routes = d["apps"]["http"]["servers"]["srv0"]["routes"]
assert routes[0]["match"][0]["host"] == ["panel.control.test"]
assert "match" not in routes[1]
'

printf A >"$TMP/alias"
mkdir -p "$TMP/data"
cat >"$TMP/mock.py" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
import os

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        host = self.headers.get('Host', '').split(':', 1)[0]
        if self.path.startswith('/tls/ask?'):
            from urllib.parse import parse_qs, urlsplit
            domain = parse_qs(urlsplit(self.path).query).get('domain', [''])[0]
            self.send_response(200 if domain == 'ok.custom.test' else 403)
            self.end_headers()
            return
        if host == 'ok.custom.test':
            value = open('/test/alias').read().strip()
            if value == 'detached':
                self.send_response(404)
                self.end_headers()
                return
        else:
            value = 'control'
        self.send_response(200)
        self.end_headers()
        self.wfile.write(value.encode())
    def log_message(self, *args):
        pass

HTTPServer(('0.0.0.0', int(os.environ['PORT'])), Handler).serve_forever()
PY
cat >"$TMP/Caddyfile" <<'CADDY'
{
  on_demand_tls {
    ask http://snaphost:8082/tls/ask
  }
}
panel.control.test {
  tls internal
  reverse_proxy snaphost:8080
}
https:// {
  tls {
    on_demand
    issuer internal
  }
  reverse_proxy snaphost:8081
}
CADDY

docker network create "$NET" >/dev/null
docker run -d --name "$MOCK" --network "$NET" --network-alias snaphost \
  -e PORT=8080 -v "$TMP:/test" python:3.12-alpine python /test/mock.py >/dev/null
docker exec "$MOCK" sh -c 'PORT=8081 python /test/mock.py >/dev/null 2>&1 & PORT=8082 python /test/mock.py >/dev/null 2>&1 &'

start_edge() {
  docker run -d --name "$EDGE" --network "$NET" -p 127.0.0.1::443 \
    -v "$TMP:/test:ro" -v "$TMP/data:/data" "$IMAGE" \
    caddy run --config /test/Caddyfile --adapter caddyfile >/dev/null
  PORT=$(docker port "$EDGE" 443/tcp | awk -F: 'NR==1 {print $NF}')
  for _ in $(seq 1 30); do
    if [[ $(curl --noproxy '*' -ksS --connect-timeout 1 --max-time 2 --resolve "panel.control.test:$PORT:127.0.0.1" "https://panel.control.test:$PORT/" 2>/dev/null || true) == control ]]; then return; fi
    sleep 1
  done
  echo 'Caddy did not become ready' >&2
  exit 1
}
request() { curl --noproxy '*' -ksS --max-time 10 --resolve "$1:$PORT:127.0.0.1" "https://$1:$PORT/"; }

start_edge
[[ $(request panel.control.test) == control ]]
[[ $(request ok.custom.test) == A ]]
printf B >"$TMP/alias"
[[ $(request ok.custom.test) == B ]]
printf A >"$TMP/alias"
[[ $(request ok.custom.test) == A ]]
for denied in unknown pending revoked stopped; do
  if request "$denied.custom.test" >/dev/null 2>&1; then
    echo "$denied project domain unexpectedly obtained a certificate" >&2
    exit 1
  fi
done
FINGERPRINT=$(echo | openssl s_client -connect "127.0.0.1:$PORT" -servername ok.custom.test 2>/dev/null | openssl x509 -noout -fingerprint -sha256)
printf detached >"$TMP/alias"
[[ $(curl --noproxy '*' -ksS -o /dev/null -w '%{http_code}' --resolve "ok.custom.test:$PORT:127.0.0.1" "https://ok.custom.test:$PORT/") == 404 ]]
docker rm -f "$EDGE" >/dev/null

# Restart against the same persistent /data store. The previously issued
# certificate must be reused rather than ordered again.
printf A >"$TMP/alias"
start_edge
RESTORED_FINGERPRINT=$(echo | openssl s_client -connect "127.0.0.1:$PORT" -servername ok.custom.test 2>/dev/null | openssl x509 -noout -fingerprint -sha256)
[[ "$FINGERPRINT" == "$RESTORED_FINGERPRINT" ]]
if docker logs "$EDGE" 2>&1 | grep -q 'obtaining certificate.*ok.custom.test'; then
  echo 'persistent project-domain certificate triggered issuance after restart' >&2
  exit 1
fi
printf 'project certificate SHA-256 fingerprint before restart: %s\n' "$FINGERPRINT"
printf 'project certificate SHA-256 fingerprint after restart: %s\n' "$RESTORED_FINGERPRINT"
printf 'Caddy handshakes passed: control 200, project domain 200, alias B/A, detach 404, denied SNI; persistent TLS state reused the certificate without a new order\n'

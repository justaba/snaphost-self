# Production edge: project-domain-only local rehearsal

Date: 2026-09-24
Status: Local checks passed; superseded for VDS evidence by the 2026-09-26 run

This rehearsal supersedes the wildcard/Cloudflare portions of the 2026-09-21
and 2026-09-22 records. Production now uses only explicit verified project
domains and stock digest-pinned Caddy.

## Observed results

| Check | Result |
| --- | --- |
| Production Compose rendering without `DOMAIN_SUFFIX` or DNS secret | Passed |
| Stock Caddy 2.10.2 validation under read-only/drop-cap/no-new-privileges restrictions | `Valid configuration` |
| Go build, vet and tests | Passed |
| Deployment suite | 59 passed, 0 failed |
| Backup suite | 41 passed, 0 failed |
| Install/upgrade suite | 26 passed, 0 failed |
| Panel tests and production build | 47 passed; ESLint, Prettier, TypeScript and Vite build passed |
| Shell syntax, ShellCheck and `git diff --check` | Passed |
| Real local TLS handshakes | Control 200; verified project domain 200; unknown, pending, revoked and stopped SNI denied |
| Alias behavior | A → B → A without Caddy reload; detach returned 404 |
| Persistent Caddy state | Same SHA-256 certificate fingerprint before and after restart; no new order logged |

The final local certificate fingerprint was
`0C:47:6B:7D:30:52:92:EA:C4:8B:96:6B:49:EA:98:48:58:6C:3E:87:81:A0:9A:19:19:1E:D8:03:3B:C2:B4:8C`.

## VPS rerun boundary

The current isolated test was prepared for `77.95.201.53`, but SSH correctly
refused the connection because the host key changed after the 2026-09-22 run:

- previously trusted ED25519 fingerprint:
  `SHA256:emlVQe2Tt+gZnf9MrRDZoB1o4XGiJeGjc+TUMl2Ep7I`;
- currently presented ED25519 fingerprint:
  `SHA256:EwMoOc4HcWr+mJa6V8lw3VgAfKLFF015F4eeWu07rAk`.

That host was not used again. The operator supplied a new VDS, documented in
the [2026-09-26 rehearsal](2026-09-26-production-edge-vds.md). Public ACME
remains unavailable because no control or project domain points to the new VDS.

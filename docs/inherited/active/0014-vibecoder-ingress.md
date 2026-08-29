# Task 14 — Vibecoder ingress: editor extension, API keys, multi-source deploy

**Status:** Planned
**Created:** 2026-07-08

## Product intent

The target user is a "vibecoder" — someone building a site or web app locally in
an AI editor (VS Code, Cursor, Claude Code). The intended flow:

1. They build a project locally.
2. They connect their editor to Snaphost once (an editor extension; later also
   an MCP server for AI agents).
3. They press "deploy".
4. The client packages the project and sends it to the Snaphost API.
5. Snaphost analyses the project, generates a Dockerfile with AI, builds, runs,
   and returns a public URL.

Steps 5–6 (AI Dockerfile → build → Yandex → router → URL) are implemented and
proven end-to-end (Task 11 Phase 5). The gap is the "ingress mile" — steps 2–4:
there is no editor entry point, no non-browser auth, and no way to deploy a local
folder that is not a public git repo.

## Current gaps

- **No editor entry point.** The audience lives in an AI editor, not a browser.
- **Auth is browser-shaped.** api-gateway accepts only a Supabase `Bearer` JWT
  ([middleware/jwt.go](../../../snaphost-backend/internal/gateway/middleware/jwt.go)).
  A "press deploy in VS Code" client needs a long-lived **API key**.
- **Ingest is git-only.** builder validates and clones a public `repo_url`
  (host allowlist, private-IP filter). A local project is often not in a public
  repo, so it cannot be deployed. The saga job carries only `repo_url` + `branch`
  with no source type.

## Design

Owner decisions (2026-07-08): the first client is an **MCP server** — the
audience already works inside AI agents (Claude Code, Cursor), so "tell the agent
to deploy" is more natural than installing an editor button, and a stdio MCP
server runs on the user's machine with filesystem access, so it can package the
local folder directly. A VS Code editor extension follows as a second client
(not a browser extension). Code reaches Snaphost by **both** an uploaded archive
and a git repo (public or private).

Introduce a **deploy source** abstraction instead of a bare `repo_url`:

```
source = { type: "git_public" | "git_private" | "archive", ... }
```

The saga job, deploy row, and builder all carry the source type; builder
dispatches to clone (git) or unpack (archive). All three sources reuse the same
downstream pipeline (AI Dockerfile → BuildKit → Trivy → push → run → router).

## Work plan

### 14a — API-key authentication (foundation for every non-browser client) — DONE

1. [x] Mint and revoke API keys (store only a SHA-256 hash; show the secret
   once). Owned by user-billing (`api_keys` table, migration 0007).
2. [x] api-gateway accepts an `sk_` bearer as an alternative to the Supabase JWT,
   verifying it against user-billing `/internal/keys/verify` and setting the same
   `X-User-ID`/role the JWT path sets. JWT still works for the browser.
3. [x] `/api/v1/keys` CRUD proxied with a Casbin `user` policy; key id logged,
   never the secret. Proven end-to-end on staging (mint → deploy with key →
   revoke → 401).

Follow-ups deferred: per-key rate limiting and scopes.

**14a-2 — API keys in the dashboard UI.** The first manual user-journey test
(2026-07-19) showed the acquisition gap: a normal user can only mint a key by
extracting their JWT from devtools and calling `POST /api/v1/keys` with curl.
Add a dashboard page (`/dashboard/keys`) backed by the existing endpoints:

- list active keys (`GET /api/v1/keys` — prefix, name, created, last used);
- create a key with an optional name (`POST /api/v1/keys`), showing the
  plaintext `sk_` exactly once in a modal with a copy button and a "you won't
  see this again" warning;
- revoke (`DELETE /api/v1/keys/:id`) behind a confirmation;
- short inline hint on wiring the key into the MCP server / editor.

No backend changes — the 14a API already covers all three operations.

### 14b — Multi-source ingest

Owner decision (2026-07-08): store the uploaded archive as a **Redis blob with a
TTL** (bounded ~50 MB), not a new object-storage bucket. Redis is already the
shared channel between user-billing and the builder worker, it needs no new cloud
resource, and a vibecoder project without `node_modules` is small. Larger
archives / horizontal scale can move to Yandex S3 later behind the same
`upload_id` indirection.

Flow:

```
POST /api/v1/deploys/upload  (user-billing) — store archive in Redis, return upload_id
POST /api/v1/deploys         source={type:"archive", upload_id}   (or git_public/git_private)
  -> saga job carries source_type + upload_id
  -> builder job carries them; worker unpacks the blob instead of cloning
```

Deliver in sub-commits, each green and deployed before the next:

**14b-1 — source abstraction (no behaviour change) — DONE.** `source_type`
(default `git_public`) plus `upload_id` threaded through every link:
`CreateDeployRequest` (`repo_url` conditional — required for git, absent for
archive), `deploys` row (migration 0008, also makes `repo_url` nullable),
`SagaJob`, `deploy_sagas` (for resume — the sweeper rebuilds source fields from
the saga row), `BuildRequest`, builder `queue.Job`. Validation matrix enforced
in both user-billing and builder APIs: git_* requires repo_url+branch; archive
requires upload_id. Deployed to staging 2026-07-12 (37e3bea3), migration 0008
verified applied.

**14b-2 — archive upload + safe unpack — DONE.**

- user-billing `POST /api/v1/deploys/upload`: accept a `tar.gz` (bounded body,
  e.g. 50 MB), store bytes in Redis under `upload:<uuid>` with a short TTL,
  return `upload_id`. Proxy the route in api-gateway with a Casbin `user` policy
  and a matching body-size limit.
- builder worker: for `source_type=archive`, read the blob from Redis and unpack
  into the build workdir instead of cloning. **Untrusted unpack — security
  critical:**
  - reject entries with absolute paths, `..` traversal, or that escape the
    workdir after `filepath.Clean`/join;
  - reject symlinks and hardlinks (or drop them);
  - skip device/fifo/special entries; only regular files and dirs;
  - enforce per-file and total uncompressed size limits and a max file count
    (zip-bomb defence);
  - cap decompression ratio.
  - delete the Redis blob after unpack (or on failure); rely on TTL as backstop.
- Everything downstream (detect → AI Dockerfile → BuildKit → Trivy → push → run)
  is unchanged: the workdir looks the same whether cloned or unpacked.

**14b-3 — private git — DONE.** Implemented in f10e0817 (deployed to staging
2026-07-12): `git_token` (+ optional `git_username`) accepted on
`POST /deploys` for `source_type=git_private`, parked in Redis
(`gitcred:<uuid>`, `GIT_CRED_TTL_MIN` default 30m); only the opaque
`credential_id` travels through the saga job, `deploy_sagas` (migration 0009,
for resume), the `BuildRequest`, and the builder queue. The worker resolves it
right before the clone, verifies the owner, passes it to go-git as transport
BasicAuth (never in the URL — URLs with embedded userinfo are now rejected
everywhere), and deletes the key after the clone attempt; host allowlist, DNS
resolution + private-IP filter, and the IP-pinned transport unchanged.
Security review: no findings (go-git v5.12 error strings verified
credential-free).

Staging e2e proof (2026-07-12, public gateway + 14a API key): a private
GitHub repo deployed with a token → public URL 200 → DELETE 204; negative
control — the same repo as `git_public` failed with "authentication
required" and refunded; `gitcred:*` keys in Redis after the run: 0.

Implemented in a4ea173b (deployed to staging 2026-07-12): user-billing
`POST /api/v1/deploys/upload` (raw tar.gz body, gzip magic check,
`MAX_UPLOAD_SIZE_MB` default 50, `UPLOAD_TTL_MIN` default 15) stores the blob
as a Redis hash `upload:<uuid>` carrying `user_id`; ownership is verified both
at `POST /deploys` (404/403 before coin reservation) and again in the builder
worker before unpack. api-gateway proxies the route with a Casbin `user`
policy and a mirrored body-size limit. The unpack
(builder-svc/internal/unpack) implements the full checklist above — limits
configurable via `MAX_ARCHIVE_FILES` / `MAX_ARCHIVE_FILE_MB` /
`MAX_ARCHIVE_TOTAL_MB` — with a security-focused test matrix (traversal,
absolute/drive paths, symlink/hardlink rejection, special-entry skip, count /
per-file / total / decompression-ratio caps, setuid stripping). Security
review of the new surface: no findings.

Staging e2e proof (2026-07-12, entirely through the public gateway with a
14a API key): mint key → upload tar.gz → `POST /deploys
{source_type:archive, upload_id}` → build → running →
`https://proj-<id>.kinocassa.ru` returned 200 → DELETE 204. git_public
regression on the same code: deploy → 200 → DELETE 204.

Security review required for 14b-3 credential handling (14b-2 review done).

### 14c — MCP server (first client)

8. [x] A stdio MCP server the user adds to their AI agent (Claude Code, Cursor).
   Shipped in its own public repository,
   [justaba/snaphost-mcp](https://github.com/justaba/snaphost-mcp) (TypeScript,
   `@modelcontextprotocol/sdk`), published to npm as `@snaphost/mcp`; see its
   README for Claude Code / Cursor registration.
   Tools: `snaphost_deploy` (packs the folder respecting `.gitignore` /
   `.snaphostignore`, always drops `node_modules`/`.git`/`.env*`, uploads via
   14b-2, waits, returns the URL; on failure returns `failure_reason` + recent
   build logs), `snaphost_status`, `snaphost_logs` (paged), `snaphost_list`,
   `snaphost_delete`. Authenticates with a 14a API key (`SNAPHOST_API_KEY`).
   The deploy path (pack → upload → archive deploy → URL → delete) is proven
   live against staging. Task 13b recommendations plug into `snaphost_deploy`
   / `snaphost_logs` output when 13b ships.

**Extracted from the monorepo 2026-08-03.** The client is what a user installs,
so it has to be installable without cloning the platform — and it cannot be
public while it lives next to the private control plane. It moved to
[justaba/snaphost-mcp](https://github.com/justaba/snaphost-mcp) with its two
commits of history preserved, and publishes to npm as `@snaphost/mcp` from its
own tag-gated workflow.

The seam between the two repositories is the public API surface it consumes:
`POST /api/v1/deploys/upload`, `POST/GET/DELETE /api/v1/deploys`, and
`GET /api/v1/deploys/:id/logs`, with a 14a `sk_` key. Nothing enforces that
contract across the repository boundary, so a change to any of those endpoints
now breaks an installed client silently. The same applies to 14d (the VS Code
extension) when it ships.

Its default `SNAPHOST_API_URL` was moved from the staging gateway
(`api.kinocassa.ru`) to `https://api.snaphost.ru` — a published package must
not point users at staging. That address depends on Task 11's remaining launch
work, so the package is publishable but not usefully installable until
production answers there.

### 14e — stable project identity for uploads

10. [x] **Server side, done 2026-08-06.** `POST /api/v1/deploys` accepts an
    optional `project_key`: a client-supplied stable identity that decides which
    project the deploy joins. Without it every upload became its own project,
    which made custom domains unusable on the archive path — a domain belongs to
    a project, so the alias could never be moved to a newer build, and
    publishing a new version meant detaching, re-attaching, and asking the
    customer to edit DNS for a fresh token. Found by the Task 16c end-to-end
    proof, not by review.

    The key is namespaced server-side (`client:`) so it cannot collide with a
    derived git key, validated to letters, digits and `- _ . : /`, capped at
    128 characters, and scoped per account. Omitting it keeps the previous
    behaviour, so nothing deploying today changes.

11. [ ] **Client side, not done.** The MCP server must generate a key once per
    project and send it on every deploy — the natural place is a file beside the
    project, the way `.vercel/project.json` works. Until that ships, deploys
    from an AI agent still land in a fresh project each time and cannot hold a
    custom domain. It lives in
    [justaba/snaphost-mcp](https://github.com/justaba/snaphost-mcp), so it is a
    separate release.

    The VS Code extension (14d) needs the same thing in its workspace state.

### 14d — VS Code extension (second client)

9. [ ] "Snaphost: Deploy" command reusing 14a+14b: authenticate with an API key,
   package the workspace folder, upload, stream build status, and show the URL
   in-editor with the Task 13b recommendation on failure.

## Acceptance criteria

- A user mints an API key and deploys without any browser JWT flow.
- A local folder with no git remote deploys via archive upload and returns a
  public URL.
- A private git repo deploys with a supplied credential that is never persisted.
- The MCP server deploys the open workspace from inside an AI agent and returns
  the URL; the VS Code extension does the same as a second client.
- Archive handling rejects oversized inputs, path traversal, and symlinks.

## Dependencies and ordering

14a is done and proven on staging. 14b makes local projects deployable and is
split into 14b-1 (source abstraction, no behaviour change), 14b-2 (archive upload
+ safe unpack), and 14b-3 (private git); ship them as separate green,
staging-verified sub-commits. 14c (MCP server) is the first user-visible client
and the primary payoff for the vibecoder audience; 14d (VS Code extension) reuses
14a+14b afterwards. Task 13b (failure recommendations) feeds both clients'
failure UX. Security review is required for 14b-2 archive unpacking and 14b-3
credential handling.

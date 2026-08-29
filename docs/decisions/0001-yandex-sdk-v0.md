# Task 1 Step 1.1 — Yandex Cloud Go SDK Reconnaissance

Status: Accepted decision with retained investigation
Type: ADR / technical investigation
Updated: 2026-07-03

Decision: use `github.com/yandex-cloud/go-sdk@v0.31.0` for the current Yandex
adapter because its Serverless Containers and API Gateway wrappers minimize
adapter boilerplate. Re-evaluate when v0 is deprecated or a required feature
exists only in v2.

Comparison of `github.com/yandex-cloud/go-sdk` candidate versions for the
`shared/yandexauth/` package and the future runner-svc Yandex backend rewrite.

- **v0.31.0** — latest of the v0 line (module path `github.com/yandex-cloud/go-sdk`).
- **v2.102.0** — current `v2.latest` (module path `github.com/yandex-cloud/go-sdk/v2`).

Reconnaissance performed against locally fetched module caches:

- `~/go/pkg/mod/github.com/yandex-cloud/go-sdk@v0.31.0/`
- `~/go/pkg/mod/github.com/yandex-cloud/go-sdk/v2@v2.102.0/`

## Comparison Table

| Question | **v0.31.0** | **v2.102.0** (v2.latest) |
|---|---|---|
| Package path for SDK builder | `github.com/yandex-cloud/go-sdk` (import alias `ycsdk`). Construct via `ycsdk.Build(ctx, ycsdk.Config{Credentials: …})` | `github.com/yandex-cloud/go-sdk/v2` (import alias `ycsdk`). Construct via `ycsdk.Build(ctx, options.WithCredentials(creds), …)` (functional options) |
| Package path for `iamkey` | `github.com/yandex-cloud/go-sdk/iamkey` | `github.com/yandex-cloud/go-sdk/v2/pkg/iamkey` |
| Function for reading authorized key from JSON | `iamkey.ReadFromJSONFile(path) (*iamkey.Key, error)` | Same name, same signature: `iamkey.ReadFromJSONFile(path) (*iamkey.Key, error)` (under v2 path) |
| Function for building credentials from key | `ycsdk.ServiceAccountKey(key) (Credentials, error)` | `credentials.ServiceAccountKey(key) (ExchangeableCredentials, error)` (`github.com/yandex-cloud/go-sdk/v2/credentials`). Also one-shot helper: `credentials.ServiceAccountKeyFile(path) (Credentials, error)` |
| Method to get current IAM token from SDK | `sdk.CreateIAMToken(ctx)` — uncached at this entry point, hits IAM each call. Internal gRPC interceptor (`IamTokenMiddleware`, unexported access) caches for SDK-issued gRPC calls. | `sdk.CreateIAMToken(ctx)` — same shape, uncached at entry point. Internal `pkg/transport/middleware/authentication` caches for SDK gRPC calls. |
| Return type of token method (struct field name) | `*iampb.CreateIamTokenResponse` from `yandex/cloud/iam/v1`, fields `IamToken string`, `ExpiresAt *timestamppb.Timestamp` | `authentication.IamToken` **interface** from `pkg/authentication`: `GetIamToken() string`, `GetExpiresAt() time.Time` |
| Does serverless/containers package exist? | **YES** — `sdk.Serverless().Containers()` returns `*containers.Container` (gen wrapper around `yandex.cloud.serverless.containers.v1`) | **NO convenience wrapper.** Endpoint name `"serverless-containers"` is registered in `pkg/endpoints/dynamic_endpoints.go`, but caller constructs the gRPC client directly from `go-genproto/.../serverless/containers/v1` and passes `sdk.GetConnection(ctx, method)` |
| Does serverless/apigateway package exist? | **YES** — `sdk.Serverless().APIGateway()` returns `*apigateway.Apigateway` | **NO convenience wrapper.** Endpoint name registered; caller wires gRPC client by hand. |
| Operation result extraction pattern | `sdk.WrapOperation(op, err)` → `*sdk_operation.Operation`. Methods: `Wait(ctx) error`, `Response() (proto.Message, error)`, `Metadata() (proto.Message, error)`, `Id()`, `Done()`, `Failed()`, `Error()` | `pkg/operation.NewOperation(pb, concretization)` → `*operation.Operation`. Methods: `Wait(ctx) (proto.Message, error)` (returns response directly), `Response() proto.Message` (panics if not done), `Metadata() proto.Message` (no error), `ResourceID()`, `Done()` |
| **Token debug / forced reissue helper** (bonus for future rewrite) | No public API to inspect cached token or force invalidate. `sdk.CreateIAMToken(ctx)` bypasses cache (fresh exchange) — usable as "force reissue" + debug accessor. `sdk.Resolve()` is unrelated (resource-name lookup). | Same story: no public cache-inspect / invalidate. `sdk.CreateIAMToken(ctx)` returns fresh `authentication.IamToken` with both `GetIamToken()` and `GetExpiresAt()` — better for debug since interface exposes expiry directly. No `Resolver`-style helper for tokens in either. |

## Key Impact Deltas

Not in the prompt's 9 questions but load-bearing for version pick:

- **Service surface:** v0 ships hand-written wrappers for serverless containers + API gateway (matches current `runner-svc/internal/backend/yandex/yandex.go` call shape exactly: `sdk.Serverless().Containers().Container().Create(...)`). v2 dropped those wrappers — caller builds gRPC stubs from `go-genproto` + `sdk.GetConnection`. v2 = more boilerplate at the call sites.
- **Builder API:** v0 uses a `Config` struct. v2 uses functional options (`options.WithCredentials`, `options.WithEndpoint`, …). v2 builder is cleaner.
- **Operation API:** v2 `Wait` returns `(proto.Message, error)` directly; v0 requires a separate `Response()` call after `Wait`. v2 is more ergonomic but changes every call site.
- **Logger injection:** v2 first-class `options.WithLogger(*zap.Logger)`; v0 uses `grpclog`.
- **Iterators:** v2 has `pkg/iterator` for paginated APIs; v0 has manual paging.
- **Maintenance:** v0 line at v0.31.0 still receives genproto bumps but appears in pseudo-stagnant feature mode. v2 publishes frequently (`v2.0.0` … `v2.102.0` in the listed versions) — actively developed. New features (e.g. workload identity refresh) will land in v2 only.

## Recommendation Framing (no pick — user chooses)

- **v0.31.0** = minimum-diff path for the runner-svc Yandex rewrite. Existing code already calls `sdk.Serverless().Containers()` / `…APIGateway()`. The current compile errors are in 6 specific spots (`iamkey.Key_RSA_2048`, `op.GetMetadata`, `Provisioned`, `gw.GetSpec` ×3) — likely fixable in place against v0.31.0 without restructuring.
- **v2.latest** = future-proof, actively maintained, but the runner-svc Yandex rewrite would expand significantly (build genproto clients by hand, rewrite every call site for the new operation API). Task 1 itself (`shared/yandexauth/`) is roughly equivalent effort under either — both expose `NewSDK` + token getter cleanly.

## Status

Stop. Awaiting user version pick before Task 1 Step 1.2.

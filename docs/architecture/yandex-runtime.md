# Yandex runtime architecture

Status: Current
Type: Architecture
Updated: 2026-07-30

Yandex is an adapter behind the runtime boundary. Runner Yandex code is
compiled with the `yandex` build tag.

The preferred mode is `YANDEX_ROUTING_MODE=router`:

1. Runner creates and deletes per-deploy Serverless Containers.
2. Terraform owns one static API Gateway wildcard route to `router-svc`.
3. Router validates `Host`, asks billing for a running mapping, resolves the
   container invocation URL, obtains an IAM token, and proxies the request.
4. Runner does not mutate API Gateway per deploy.

Legacy `gateway` mode remains available, but cannot safely provide concurrent
hostname routing through one shared greedy path.

## The port contract

The runtime sets `PORT` in the container's environment and sends every request
to that port. This is the whole contract a deployed image has to satisfy, and
it holds for both runtime backends: Serverless Containers inject `PORT` (8080),
and the local Docker path routes to the port resolved from the build.

An application that binds a fixed port instead builds, scans, pushes, and
starts — and then answers nothing, because traffic arrives somewhere it is not
listening. On Yandex this surfaces as
`{"errorType":"UserCodeError","errorMessage":"exit status 1"}` on every request.

Three places enforce or explain the contract, and all three should stay
consistent with this section:

- AI-generated Dockerfiles bind `$PORT` (Task 11.17).
- `builder-svc` warns when a user-shipped Dockerfile exposes a fixed port and
  never mentions `PORT` — advisory only, since a Dockerfile cannot show what
  the application reads at runtime (Task 15a).
- `runner-svc` probes the started container and refuses to report it `running`
  unless something answers on the injected port (Task 15b). A deploy that fails
  the probe is torn down, marked `failed` with a reason naming `PORT`, and its
  coin reservation is refunded by the saga rather than committed.

Builder uses a narrow registry-pusher account. Runner uses a separate account
for container lifecycle and pull/invocation. Router uses metadata credentials
and reads its webhook secret from Lockbox.

See [runtime configuration](../operations/yandex-runtime.md) and
[cloud setup](../operations/yandex-cloud-setup.md).

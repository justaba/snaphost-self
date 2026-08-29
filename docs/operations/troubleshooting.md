# Troubleshooting

Status: Current
Type: Operations
Updated: 2026-07-03

## Build fails before clone

Check repository URL validation, allowed Git hosts, DNS resolution, and whether
the resolved address was rejected as private or metadata space.

## Build fails during scan

Distinguish a permanent vulnerability result from a transient Trivy database
or network failure. Transient retry activation is not implemented yet.

## Runner rejects an image

Verify that:

- the image is under `REGISTRY_ALLOWED_PREFIXES` on a path boundary;
- its tag equals `deploy_id`;
- billing stores the same user, image reference, and deploy state;
- strict validation is enabled with the intended production prefix.

## Yandex revision fails

- Do not pass `PORT`; Yandex reserves and supplies it.
- Runner needs `iam.serviceAccounts.user` when deploying with a service account.
- Memory must be a 128 MiB multiple; runner rounds configured values.
- Core fraction must be a valid multiple of five.

## Public URL returns 404

Confirm the deploy is `running`, has a non-empty `container_id`, and route lookup
returns the expected host mapping. In router mode, verify API Gateway points to
`router-svc` and the router can reach billing.

## TTL cleanup does not run

Check watchdog backend selection, billing connectivity, the expired-deploy
query, stored container ID, and runner credentials. Successful stop must leave
`status='stopped'` and a non-null `stopped_at`.

Provider-specific setup failures are also listed in
[yandex-cloud-setup.md](yandex-cloud-setup.md).

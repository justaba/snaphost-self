#!/usr/bin/env bash
set -Eeuo pipefail

pipeline=.github/workflows/pipeline.yml
production=.github/workflows/production-deploy.yml
helper=.github/scripts/deploy-remote.sh
legacy=.github/workflows/ci.yml
compose=infra/docker-compose.prod.yml
buildkit_apparmor=infra/apparmor/snaphost-buildkit-rootless
builder_dockerfile=snaphost-backend/builder-svc/Dockerfile

grep -q '^  pull_request:' "$pipeline"
grep -q '^  push:' "$pipeline"
! grep -Fq 'cloudflare/wrangler-action' "$pipeline"
grep -A5 '^  images:' "$pipeline" | grep -Fq 'needs: [go, terraform, shell]'
grep -A8 '^  deploy-staging:' "$pipeline" | grep -Fq 'needs: [images]'
grep -A8 '^  deploy-staging:' "$pipeline" | grep -Fq "github.event_name == 'push'"
grep -A8 '^  deploy-staging:' "$pipeline" | grep -Fq "github.ref == 'refs/heads/main'"
grep -A12 '^  deploy-staging:' "$pipeline" | grep -Fq 'environment: staging'
grep -A14 '^  deploy-staging:' "$pipeline" | grep -Fq 'cancel-in-progress: false'
! grep -Eq 'snaphost-ui|frontend-dist|deploy-frontend-remote|VITE_' "$pipeline" "$production"
! test -e .github/scripts/deploy-frontend-remote.sh

grep -q '^  workflow_dispatch:' "$production"
! grep -q '^  push:' "$production"
! grep -q '^  pull_request:' "$production"
grep -A8 '^  deploy-production:' "$production" | grep -Fq 'environment: production'
grep -A12 '^  deploy-production:' "$production" | grep -Fq 'cancel-in-progress: false'
grep -Fq '^[0-9a-fA-F]{40}$' "$production"
grep -Fq 'git merge-base --is-ancestor "$TARGET_SHA" origin/main' "$production"
grep -Fq 'git fetch --no-tags origin main' "$production"

grep -Fq 'StrictHostKeyChecking=yes' "$helper"
! grep -Fq 'ssh-keyscan' "$helper"
grep -Fq 'root SSH deployment is forbidden' "$helper"
grep -Fq 'infra/docker-compose.prod.yml' "$helper"
grep -Fq 'infra/deploy.sh' "$helper"
! grep -Fq 'infra/docker-compose.yml' "$helper"
! grep -Eq '(^|[^a-z])latest([^a-z]|$)' "$helper"
! grep -Eq 'github\.ref|github\.head_ref' "$helper"
! grep -Eq 'docker-compose\.yml|username: root|ssh-action|scp-action' "$legacy"
grep -Fq 'apparmor=snaphost-buildkit-rootless' "$compose"
! grep -Fq 'apparmor=unconfined' "$compose"
grep -Fq 'systempaths=unconfined' "$compose"
grep -Fq 'profile snaphost-buildkit-rootless flags=(unconfined)' "$buildkit_apparmor"
grep -Fq 'userns,' "$buildkit_apparmor"
grep -Fq 'useradd --uid 1000 --gid 1000' "$builder_dockerfile"
grep -Fq 'USER 1000:1000' "$builder_dockerfile"

for image in api-gateway user-billing builder-svc runner-svc ai-orchestrator router-svc; do
  grep -Fq "$image" "$helper"
done

! grep -REn 'echo .*\$\{\{ *secrets\.|set +-x' "$pipeline" "$production" "$helper"
grep -Fq 'environment: staging' "$pipeline"
grep -Fq 'environment: production' "$production"
grep -Fq 'deploy-staging' "$pipeline"
grep -Fq 'deploy-production' "$production"

preflight_line=$(grep -n 'deploy.sh" preflight' "$helper" | tail -1 | cut -d: -f1)
deploy_line=$(grep -n 'deploy.sh" deploy' "$helper" | tail -1 | cut -d: -f1)
switch_line=$(grep -n 'mv -Tf -- "$link_tmp" "$root/current"' "$helper" | tail -1 | cut -d: -f1)
(( preflight_line < deploy_line && deploy_line < switch_line ))

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
touch "$tmp/key" "$tmp/known-hosts"
marker="$tmp/injected"

run_invalid() {
  local name=$1 value=$2
  if env \
    DEPLOY_HOST=host.invalid \
    DEPLOY_USER=deployer \
    DEPLOY_PORT=22 \
    DEPLOY_ROOT=/opt/snaphost \
    DEPLOY_SMOKE_URL=https://control.invalid \
    DEPLOY_GHCR_USERNAME=deploy-user \
    DEPLOY_COMPOSE_PROJECT=snaphost-test \
    SSH_KEY_FILE="$tmp/key" \
    SSH_KNOWN_HOSTS_FILE="$tmp/known-hosts" \
    GHCR_IMAGE_PREFIX=ghcr.io/acme/snaphost \
    "$name=$value" \
    "$helper" 0123456789abcdef0123456789abcdef01234567 >"$tmp/output" 2>&1; then
    echo "unsafe value accepted for $name" >&2
    exit 1
  fi
  [[ ! -e "$marker" ]]
}

run_invalid DEPLOY_SMOKE_URL "https://control.invalid/;touch $marker"
run_invalid DEPLOY_SMOKE_URL "https://control.invalid/\$(touch $marker)"
run_invalid DEPLOY_SMOKE_URL "https://control.invalid/\`touch $marker\`"
run_invalid DEPLOY_SMOKE_URL $'https://control.invalid/line\nbreak'
run_invalid DEPLOY_SMOKE_URL 'https://control.invalid/has space'
run_invalid DEPLOY_SMOKE_URL 'https://user@control.invalid'
run_invalid DEPLOY_GHCR_USERNAME 'name;touch'
run_invalid DEPLOY_GHCR_USERNAME '$(touch)'
run_invalid DEPLOY_GHCR_USERNAME 'name with space'
run_invalid DEPLOY_COMPOSE_PROJECT 'Uppercase'
run_invalid DEPLOY_COMPOSE_PROJECT 'name with space'
run_invalid DEPLOY_COMPOSE_PROJECT '../stack'
run_invalid DEPLOY_ROOT '/opt/../../tmp'
run_invalid DEPLOY_ROOT '/opt/a/../b'
run_invalid DEPLOY_ROOT '/opt//x'
run_invalid DEPLOY_ROOT '/tmp/x'
run_invalid DEPLOY_ROOT '/opt/x/'
run_invalid DEPLOY_ROOT '/opt/./x'

echo 'CD contract checks passed'

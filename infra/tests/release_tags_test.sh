#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

export IMAGE=ghcr.io/justaba/snaphost-self/snaphost
export SHA=0123456789abcdef0123456789abcdef01234567
export GITHUB_OUTPUT="$tmp/output"

export REF=refs/heads/main
bash "$root/infra/resolve-image-tags.sh"
diff -u <(printf 'tags<<TAGS_EOF\n%s:%s\nTAGS_EOF\n' "$IMAGE" "$SHA") "$GITHUB_OUTPUT"

: >"$GITHUB_OUTPUT"
export IMAGE=ghcr.io/Example/SnapHost-Self/snaphost
bash "$root/infra/resolve-image-tags.sh"
diff -u <(printf 'tags<<TAGS_EOF\nghcr.io/example/snaphost-self/snaphost:%s\nTAGS_EOF\n' "$SHA") "$GITHUB_OUTPUT"
export IMAGE=ghcr.io/justaba/snaphost-self/snaphost

: >"$GITHUB_OUTPUT"
export REF=refs/tags/v1.2.3
bash "$root/infra/resolve-image-tags.sh"
diff -u <(printf 'tags<<TAGS_EOF\n%s:v1.2.3\n%s:%s\nTAGS_EOF\n' "$IMAGE" "$IMAGE" "$SHA") "$GITHUB_OUTPUT"

for REF in refs/tags/v1.2 refs/tags/v01.2.3 refs/tags/v1.02.3 refs/tags/v1.2.03 refs/tags/v1.2.3-rc1 refs/tags/latest; do
  export REF
  : >"$GITHUB_OUTPUT"
  if bash "$root/infra/resolve-image-tags.sh" >"$tmp/stdout" 2>"$tmp/stderr"; then
    echo "invalid release tag was accepted: $REF" >&2
    exit 1
  fi
  [[ ! -s "$GITHUB_OUTPUT" ]] || { echo "invalid release tag wrote image tags: $REF" >&2; exit 1; }
done

echo 'Release image tags: main, SemVer and invalid-tag cases passed'

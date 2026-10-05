#!/usr/bin/env bash
set -Eeuo pipefail

# Enforce strict SemVer (including the leading-zero rule) before the
# publishing step receives any release tags.
image=$(printf '%s' "${IMAGE:?IMAGE is required}" | tr '[:upper:]' '[:lower:]')
ref=${REF:?REF is required}
sha=${SHA:?SHA is required}
output=${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}

[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo 'SHA must be a 40-character lowercase Git commit' >&2; exit 1; }
[[ "$image" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+/snaphost$ ]] || {
  echo 'IMAGE must be the repository GHCR package' >&2
  exit 1
}

tags="$image:$sha"
if [[ "$ref" == refs/tags/* ]]; then
  version=${ref#refs/tags/}
  [[ "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
    echo "tag $version is not vMAJOR.MINOR.PATCH" >&2
    exit 1
  }
  tags="$image:$version"$'\n'"$tags"
fi

{
  echo 'tags<<TAGS_EOF'
  echo "$tags"
  echo 'TAGS_EOF'
} >>"$output"

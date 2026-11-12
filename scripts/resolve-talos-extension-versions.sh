#!/usr/bin/env bash
#
# Resolve Talos extension image versions from their discovery sources.
#
# Discovery sources:
#   github-release: latest release tag of a GitHub repository (release tag
#                   equals image tag; used for the Kommodity extensions).
#   catalog:        the tag pinned for the requested Talos version in the
#                   official Talos extensions catalog
#                   (ghcr.io/siderolabs/extensions:<talos_version>), the same
#                   source the Talos Image Factory uses.
#
# Extension specs are passed as arguments: the script knows no specific
# extension, callers decide which extensions exist and how they toggle.
#
# Output (GITHUB_OUTPUT when set, stdout otherwise):
#   extensions: comma-separated full image refs of every list extension
#   versions:   "<image-basename>=<version>" line per resolved extension,
#               including version-only ones and extras
#
# When GITHUB_STEP_SUMMARY is set, a markdown table describing per-extension
# version resolution is appended to it.
#
# Usage:
#   resolve-talos-extension-versions.sh \
#     --talos-version <version> \
#     [--github-release-extension "<owner/repo> <image> [<version-override>]"] \
#     [--catalog-extension "<image> [<version-override>]"] \
#     [--catalog-extension-version-only "<image> [<version-override>]"] \
#     [--extra-extension "<full-image-ref-with-tag>"] ...
#
# "version-only" catalog extensions are resolved and reported but excluded
# from the extensions list; callers read their version from the versions map.

set -euo pipefail

GITHUB_API="https://api.github.com"
CATALOG_REF_PREFIX="ghcr.io/siderolabs/extensions:"

error() {
  echo "::error::${*}" >&2
}

usage() {
  grep '^#' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 1
}

# Latest release tag of a GitHub repository, or fail loudly.
latest_release() {
  local repo="$1"
  local response
  local tag

  response=$(curl -sS --max-time 30 "${api_headers[@]}" "${GITHUB_API}/repos/${repo}/releases/latest") || {
    error "failed to reach the GitHub API while looking up the latest release of ${repo}"
    return 1
  }

  tag=$(jq -r '.tag_name // empty' <<<"${response}" 2>/dev/null)
  if [[ -z "${tag}" ]]; then
    local message
    message=$(jq -r '.message // empty' <<<"${response}" 2>/dev/null)
    if [[ -n "${message}" ]]; then
      error "no releases found for ${repo}: ${message}"
    else
      error "no releases found for ${repo}"
    fi
    return 1
  fi

  echo "${tag}"
}

# Extract the image-digests file from the Talos extensions catalog into stdout.
fetch_catalog() {
  local tmpdir
  tmpdir=$(mktemp -d)

  # Plain `oras pull` does not work: catalog layers lack the title annotation.
  oras copy --to-oci-layout "${CATALOG_REF_PREFIX}${talos_version}" "${tmpdir}/layout" >/dev/null || {
    error "failed to fetch the Talos extensions catalog for ${talos_version} (${CATALOG_REF_PREFIX}${talos_version})"
    rm -rf "${tmpdir}"
    return 1
  }

  # Follow the manifest chain: index -> manifest -> layers. Some catalog tags
  # are image indexes even though the runner only needs the amd64 manifest.
  local media_type
  local manifest_blob
  manifest_blob="${tmpdir}/layout/blobs/sha256/$(jq -r '.manifests[0].digest' "${tmpdir}/layout/index.json" | cut -d: -f2)"
  media_type=$(jq -r '.mediaType' "${manifest_blob}")
  if [[ "${media_type}" == *"image.index"* ]]; then
    manifest_blob="${tmpdir}/layout/blobs/sha256/$(jq -r '.manifests[] | select(.platform.architecture == "amd64" and .platform.os == "linux") | .digest' "${manifest_blob}" | head -n1 | cut -d: -f2)"
  fi

  local blob
  for blob in $(jq -r '.layers[].digest' "${manifest_blob}" | cut -d: -f2); do
    if tar -tf "${tmpdir}/layout/blobs/sha256/${blob}" 2>/dev/null | grep -qx 'image-digests'; then
      tar -xOf "${tmpdir}/layout/blobs/sha256/${blob}" image-digests
      rm -rf "${tmpdir}"
      return 0
    fi
  done

  error "Talos extensions catalog for ${talos_version} has no image-digests file"
  rm -rf "${tmpdir}"
  return 1
}

catalog=""
need_catalog() {
  if [[ -z "${catalog}" ]]; then
    catalog=$(fetch_catalog)
  fi
}

# Tag pinned in the catalog for a Siderolabs extension, or fail loudly listing
# what the catalog actually offers for this Talos version.
catalog_version() {
  local image="$1"
  local catalog="$2"
  local ref

  ref=$(grep -F "${image}:" <<<"${catalog}" | head -n1 | sed 's/@sha256:.*$//')
  if [[ -z "${ref}" ]]; then
    local available
    available=$(sed 's|ghcr\.io/siderolabs/||; s|:.*||' <<<"${catalog}" | sort -u | tr '\n' ' ')
    error "extension ${image##*/} is not available for Talos ${talos_version}. Available extensions: ${available}"
    return 1
  fi

  echo "${ref##*:}"
}

talos_version=""
github_specs=()
catalog_specs=()
catalog_version_only_specs=()
extra_refs=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --talos-version)
      talos_version="$2"
      shift 2
      ;;
    --github-release-extension)
      github_specs+=("$2")
      shift 2
      ;;
    --catalog-extension)
      catalog_specs+=("$2")
      shift 2
      ;;
    --catalog-extension-version-only)
      catalog_version_only_specs+=("$2")
      shift 2
      ;;
    --extra-extension)
      extra_refs+=("$2")
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

[[ -n "${talos_version}" ]] || { error "--talos-version is required"; exit 1; }

if [[ ! "${talos_version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
  error "invalid --talos-version '${talos_version}', expected semver (e.g., v1.14.5)"
  exit 1
fi

github_token="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null || true)}"

api_headers=()
if [[ -n "${github_token}" ]]; then
  api_headers=(-H "Authorization: Bearer ${github_token}")
fi

refs=()
version_lines=()
summary_lines=()

# add_resolved <image> <version> <version-source> <in-list: true|false>
add_resolved() {
  local image="$1"
  local version="$2"
  local source="$3"
  local in_list="$4"

  if [[ "${in_list}" == "true" ]]; then
    refs+=("${image}:${version}")
  fi
  version_lines+=("$(basename "${image%%@*}")=${version}")
  summary_lines+=("| \`${image}\` | \`${version}\` | ${source} |")
}

# Resolve a "<image> [<override>]" spec against a discovery function; the
# override short-circuits discovery. Sources: 'github-release' specs carry a
# repository as their first word, catalog specs do not.
resolve_spec() {
  local spec="$1"
  local discover="$2"
  local in_list="$3"
  local source_label="$4"

  local repo image override version
  if [[ "${discover}" == "discover_github" ]]; then
    read -r repo image override <<<"${spec}"
  else
    repo=""
    read -r image override <<<"${spec}"
  fi
  if [[ -z "${image}" ]]; then
    error "invalid extension spec '${spec}'"
    exit 1
  fi

  if [[ -n "${override}" ]]; then
    add_resolved "${image}" "${override}" "explicit override" "${in_list}"
    return 0
  fi

  version=$("${discover}" "${repo}" "${image}")
  add_resolved "${image}" "${version}" "${source_label}" "${in_list}"
}

discover_github() {
  latest_release "$1"
}

discover_catalog() {
  need_catalog
  catalog_version "$2" "${catalog}"
}

for spec in ${github_specs[@]+"${github_specs[@]}"}; do
  resolve_spec "${spec}" discover_github true "latest GitHub release"
done

for spec in ${catalog_specs[@]+"${catalog_specs[@]}"}; do
  resolve_spec "${spec}" discover_catalog true "Talos extensions catalog for ${talos_version}"
done

for spec in ${catalog_version_only_specs[@]+"${catalog_version_only_specs[@]}"}; do
  resolve_spec "${spec}" discover_catalog false "Talos extensions catalog for ${talos_version}"
done

for extra in ${extra_refs[@]+"${extra_refs[@]}"}; do
  # Full image refs must carry a tag: last path segment needs ':<tag>'.
  if [[ "$(basename "${extra%%@*}")" != *:* ]]; then
    error "extra extension '${extra}' has no tag; use a full image ref with a tag (e.g., ghcr.io/siderolabs/my-extension:v1.0.0)"
    exit 1
  fi
  refs+=("${extra}")
  extra_image="${extra%:*}"
  extra_tag="${extra##*:}"
  version_lines+=("$(basename "${extra_image%%@*}")=${extra_tag}")
  summary_lines+=("| \`${extra_image}\` | \`${extra_tag}\` | extra extension input |")
done

extensions=$(
  IFS=,
  echo "${refs[*]:-}"
)

versions=""
if [[ ${#version_lines[@]} -gt 0 ]]; then
  versions=$(printf '%s\n' "${version_lines[@]}")
fi

summary="| Extension image | Version | Version source |\n|---|---|---|\n"
if [[ ${#summary_lines[@]} -gt 0 ]]; then
  joined="${summary_lines[0]}"
  for line in "${summary_lines[@]:1}"; do
    joined="${joined}\\n${line}"
  done
  summary+="${joined}"
fi

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "### Resolved extension versions"
    echo ""
    printf '%b\n' "${summary}"
  } >> "${GITHUB_STEP_SUMMARY}"
fi

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    echo "extensions=${extensions}"
    echo "versions<<EOF"
    echo "${versions}"
    echo "EOF"
  } >> "${GITHUB_OUTPUT}"
else
  echo "extensions=${extensions}"
  echo "versions<<EOF"
  echo "${versions}"
  echo "EOF"
  printf '%b\n' "${summary}"
fi

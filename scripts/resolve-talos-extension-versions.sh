#!/usr/bin/env bash
#
# Resolve extension image versions for the talos-cloud-image workflow.
#
# Kommodity extensions (attestation, autobootstrap): latest GitHub release tag
# of the respective extension repository (release tag equals image tag).
# Siderolabs extensions (qemu-guest-agent, NVIDIA production): the tag pinned
# for the requested Talos version in the official Talos extensions catalog
# (ghcr.io/siderolabs/extensions:<talos_version>), same source the Talos Image
# Factory uses.
#
# Output (GITHUB_OUTPUT when set, stdout otherwise):
#   extensions:      comma-separated full image refs (empty when no extension enabled)
#   qemu_ga_version: qemu-guest-agent version (used by the openstack image only)
#
# When GITHUB_STEP_SUMMARY is set, a markdown table describing per-extension
# version resolution is appended to it.
#
# Usage:
#   resolve-talos-extension-versions.sh \
#     --talos-version v1.14.5 \
#     [--attestation <bool|override>] \
#     [--autobootstrap <bool|override>] \
#     [--nvidia-container-toolkit-production <bool|override>] \
#     [--nvidia-open-gpu-kernel-modules-production <bool|override>] \
#     [--qemu-guest-agent <override>] \
#     [--extra-extension <full-image-ref:tag>] ...   (repeatable)

set -euo pipefail

GITHUB_API="https://api.github.com"
CATALOG_REF_PREFIX="ghcr.io/siderolabs/extensions:"
ATTESTATION_REPO="kommodity-io/kommodity-attestation-extension"
ATTESTATION_IMAGE="ghcr.io/kommodity-io/kommodity-attestation-extension"
AUTOBOOTSTRAP_REPO="kommodity-io/kommodity-autobootstrap-extension"
AUTOBOOTSTRAP_IMAGE="ghcr.io/kommodity-io/kommodity-autobootstrap-extension"
QEMU_GA_IMAGE="ghcr.io/siderolabs/qemu-guest-agent"
NVIDIA_TOOLKIT_IMAGE="ghcr.io/siderolabs/nvidia-container-toolkit-production"
NVIDIA_OPEN_KERNEL_IMAGE="ghcr.io/siderolabs/nvidia-open-gpu-kernel-modules-production"

error() {
  echo "::error::${*}" >&2
}

usage() {
  grep '^#' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 1
}

# Parse "<bool>|<version-override>": toggles accept true/false or an explicit
# version string that implies true.
parse_toggle() {
  local value="$1"
  if [[ "${value}" == "true" || "${value}" == "false" ]]; then
    echo "${value}|"
    return
  fi
  echo "true|${value}"
}

talos_version=""
attestation=""
autobootstrap=""
nvidia_toolkit=""
nvidia_open_kernel=""
qemu_ga=""
qemu_ga_requested="false"
extra_extensions=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --talos-version)
      talos_version="$2"
      shift 2
      ;;
    --attestation)
      attestation="$(parse_toggle "$2")"
      shift 2
      ;;
    --autobootstrap)
      autobootstrap="$(parse_toggle "$2")"
      shift 2
      ;;
    --nvidia-container-toolkit-production)
      nvidia_toolkit="$(parse_toggle "$2")"
      shift 2
      ;;
    --nvidia-open-gpu-kernel-modules-production)
      nvidia_open_kernel="$(parse_toggle "$2")"
      shift 2
      ;;
    --qemu-guest-agent)
      qemu_ga="$2"
      qemu_ga_requested="true"
      shift 2
      ;;
    --extra-extension)
      extra_extensions+=("$2")
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

catalog=""
need_catalog() {
  if [[ -z "${catalog}" ]]; then
    catalog=$(fetch_catalog)
  fi
}

refs=()
summary_lines=()

# Add an extension ref to the output list and record how its version was set.
add_extension() {
  local image="$1"
  local version="$2"
  local source="$3"

  refs+=("${image}:${version}")
  summary_lines+=("| \`${image}\` | \`${version}\` | ${source} |")
}

resolve_kommodity_extension() {
  local enabled="$1"
  local override="$2"
  local repo="$3"
  local image="$4"

  [[ "${enabled}" == "true" ]] || return 0

  if [[ -n "${override}" ]]; then
    add_extension "${image}" "${override}" "explicit override"
    return 0
  fi

  local version
  version=$(latest_release "${repo}")
  add_extension "${image}" "${version}" "latest GitHub release of ${repo}"
}

resolve_catalog_extension() {
  local enabled="$1"
  local override="$2"
  local image="$3"

  [[ "${enabled}" == "true" ]] || return 0

  if [[ -n "${override}" ]]; then
    add_extension "${image}" "${override}" "explicit override"
    return 0
  fi

  need_catalog

  local version
  version=$(catalog_version "${image}" "${catalog}")
  add_extension "${image}" "${version}" "Talos extensions catalog for ${talos_version}"
}

IFS='|' read -r attestation_enabled attestation_override <<<"${attestation:-false|}"
IFS='|' read -r autobootstrap_enabled autobootstrap_override <<<"${autobootstrap:-false|}"
IFS='|' read -r toolkit_enabled toolkit_override <<<"${nvidia_toolkit:-false|}"
IFS='|' read -r kernel_enabled kernel_override <<<"${nvidia_open_kernel:-false|}"

resolve_kommodity_extension "${attestation_enabled}" "${attestation_override}" "${ATTESTATION_REPO}" "${ATTESTATION_IMAGE}"
resolve_kommodity_extension "${autobootstrap_enabled}" "${autobootstrap_override}" "${AUTOBOOTSTRAP_REPO}" "${AUTOBOOTSTRAP_IMAGE}"
resolve_catalog_extension "${toolkit_enabled}" "${toolkit_override}" "${NVIDIA_TOOLKIT_IMAGE}" nvidia-container-toolkit-production
resolve_catalog_extension "${kernel_enabled}" "${kernel_override}" "${NVIDIA_OPEN_KERNEL_IMAGE}" nvidia-open-gpu-kernel-modules-production

qemu_ga_version=""
if [[ -n "${qemu_ga}" ]]; then
  qemu_ga_version="${qemu_ga}"
  qemu_ga_source="explicit override"
elif [[ "${qemu_ga_requested}" == "true" ]]; then
  need_catalog
  qemu_ga_version=$(catalog_version "${QEMU_GA_IMAGE}" "${catalog}")
  qemu_ga_source="Talos extensions catalog for ${talos_version}"
fi
if [[ -n "${qemu_ga_version}" ]]; then
  summary_lines+=("| \`${QEMU_GA_IMAGE}\` | \`${qemu_ga_version}\` | ${qemu_ga_source} (required for the openstack image) |")
fi


for extra in ${extra_extensions[@]+"${extra_extensions[@]}"}; do
  # Full image refs must carry a tag: last path segment needs ':<tag>'.
  if [[ "$(basename "${extra%%@*}")" != *:* ]]; then
    error "extra extension '${extra}' has no tag; use a full image ref with a tag (e.g., ghcr.io/siderolabs/my-extension:v1.0.0)"
    exit 1
  fi
  refs+=("${extra}")
  extra_image="${extra%:*}"
  extra_tag="${extra##*:}"
  summary_lines+=("| \`${extra_image}\` | \`${extra_tag}\` | extra extension input |")
done


extensions=$(
  IFS=,
  echo "${refs[*]:-}"
)

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
    echo "qemu_ga_version=${qemu_ga_version}"
  } >> "${GITHUB_OUTPUT}"
else
  echo "extensions=${extensions}"
  echo "qemu_ga_version=${qemu_ga_version}"
  printf '%b\n' "${summary}"
fi

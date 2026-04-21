#!/usr/bin/env bash
# Build a Talos Scaleway boot image with fscrypt (CONFIG_FS_CRYPTO=y) in the kernel.
#
# Usage:
#   ./scripts/build-talos-fscrypt-image.sh <talos_version>
#
# Example:
#   ./scripts/build-talos-fscrypt-image.sh v1.12.3
#
# Output:
#   Custom installer image pushed to ghcr.io/kommodity-io
#
# Requirements:
#   - docker + docker buildx
#   - git

set -euo pipefail

DOCKER=docker
BUILDX="docker buildx"

# ---------------------------------------------------------------------------
# Args
# ---------------------------------------------------------------------------
TALOS_VERSION="${1:-}"
if [[ -z "${TALOS_VERSION}" ]]; then
  echo "Usage: $0 <talos_version>  (e.g. v1.12.3)"
  exit 1
fi

# siderolabs/pkgs does not publish per-patch-release tags.
# Replace the patch component with 0 so v1.12.3 → v1.12.0 for the pkgs checkout.
PKGS_VERSION="${TALOS_VERSION%.*}.0"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORK_DIR="${REPO_ROOT}/_out/fscrypt-build"
OUT_DIR="${REPO_ROOT}/_out"
PKGS_DIR="${WORK_DIR}/pkgs"

REGISTRY="ghcr.io/kommodity-io"
INSTALLER_IMAGE="${REGISTRY}/kommodity-talos-installer-fscrypt:${TALOS_VERSION}"

echo "=========================================="
echo "Talos version : ${TALOS_VERSION}"
echo "Pkgs version  : ${PKGS_VERSION}"
echo "Work dir      : ${WORK_DIR}"
echo "Installer tag : ${INSTALLER_IMAGE}"
echo "=========================================="

mkdir -p "${WORK_DIR}" "${OUT_DIR}"

# Set to "true" to skip steps 1-4 (kernel build) when vmlinuz already exists.
SKIP_KERNEL_BUILD="${SKIP_KERNEL_BUILD:-false}"

VMLINUZ_DIR="${WORK_DIR}/vmlinuz"

if [[ "${SKIP_KERNEL_BUILD}" != "true" ]]; then

# ---------------------------------------------------------------------------
# Step 1 — Clone siderolabs/pkgs at the matching tag
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 1: Cloning siderolabs/pkgs @ ${PKGS_VERSION}"

if [[ -d "${PKGS_DIR}/.git" ]]; then
  echo "    (already cloned — fetching tags)"
  git -C "${PKGS_DIR}" fetch --tags --quiet
  git -C "${PKGS_DIR}" checkout "${PKGS_VERSION}" --quiet
else
  git clone \
    --branch "${PKGS_VERSION}" \
    --depth 1 \
    https://github.com/siderolabs/pkgs.git \
    "${PKGS_DIR}"
fi

# ---------------------------------------------------------------------------
# Step 2 — Patch kernel config: enable filesystem encryption (fscrypt)
#
# The kernel symbol is CONFIG_FS_CRYPTO (renamed from CONFIG_FS_ENCRYPTION in
# kernel ~5.4). We set both for compatibility, plus required dependencies:
#   CONFIG_FS_CRYPTO=y          — core fscrypt support
#   CONFIG_FS_CRYPTO_USER_API=y — userspace API for setting encryption policies
#   CONFIG_CRYPTO_CTS=y         — cipher-text stealing (filename encryption)
#   CONFIG_CRYPTO_HKDF=y        — HKDF key derivation (must be built-in, not module)
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 2: Patching kernel config"

CONFIG_FILES=()
for pattern in \
  "${PKGS_DIR}/kernel/build/linux-amd64.config" \
  "${PKGS_DIR}/kernel/build/config-amd64" \
  "${PKGS_DIR}/kernel/amd64.config"; do
  [[ -f "${pattern}" ]] && CONFIG_FILES+=("${pattern}")
done

if [[ ${#CONFIG_FILES[@]} -eq 0 ]]; then
  echo "ERROR: No amd64 kernel config found. Listing kernel dir:"
  find "${PKGS_DIR}/kernel" -name "*.config" -o -name "config-*" 2>/dev/null | head -20
  exit 1
fi

FSCRYPT_CONFIGS=(
  "CONFIG_FS_CRYPTO=y"
  "CONFIG_FS_CRYPTO_USER_API=y"
  "CONFIG_FS_ENCRYPTION=y"
  "CONFIG_CRYPTO_CTS=y"
  "CONFIG_CRYPTO_HKDF=y"
)

for config in "${CONFIG_FILES[@]}"; do
  echo "    Patching: ${config}"
  for kconf in "${FSCRYPT_CONFIGS[@]}"; do
    key="${kconf%%=*}"
    # Remove any existing setting (both disabled and enabled forms)
    sed -i.bak "/^# ${key} is not set/d" "${config}"
    sed -i.bak "/^${key}[= ]/d" "${config}"
    echo "${kconf}" >> "${config}"
  done
  rm -f "${config}.bak"
  echo "    Result:"
  grep -E 'FS_CRYPTO|FS_ENCRYPTION|CRYPTO_CTS|CRYPTO_HKDF' "${config}" | grep -v "^#" | sed 's/^/      /'
done

# ---------------------------------------------------------------------------
# Create buildx builder (docker-container driver, required for bldr mergeop)
# Used by both Step 2b and Step 3.
# ---------------------------------------------------------------------------
BUILDER_NAME="talos-kernel-builder"
KERNEL_CACHE_DIR="${HOME}/.cache/buildkit/talos-kernel-${TALOS_VERSION}"
SOURCE_DATE_EPOCH="$(git -C "${PKGS_DIR}" log "$(git -C "${PKGS_DIR}" rev-list --max-parents=0 HEAD)" --pretty=%ct)"

if ! ${BUILDX} inspect "${BUILDER_NAME}" &>/dev/null; then
  echo "    Creating buildx builder: ${BUILDER_NAME}"
  ${BUILDX} create --name "${BUILDER_NAME}" --use
else
  echo "    Using existing buildx builder: ${BUILDER_NAME}"
  ${BUILDX} use "${BUILDER_NAME}"
fi

# ---------------------------------------------------------------------------
# Step 2b — Resolve config dependencies via kernel-olddefconfig
#
# Equivalent to `make kernel-olddefconfig` in the pkgs Makefile.
# Runs `make olddefconfig` inside the kernel build environment via BuildKit,
# then writes the resolved config back to kernel/build/config-amd64.
# Without this, options with unmet dependencies are silently dropped.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 2b: Running kernel-olddefconfig to resolve config dependencies"

${BUILDX} build \
  --builder="${BUILDER_NAME}" \
  --target=kernel-build \
  --file="${PKGS_DIR}/Pkgfile" \
  --platform=linux/amd64 \
  --provenance=false \
  --build-arg="SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}" \
  --build-arg="KERNEL_TARGET=olddefconfig" \
  --output="type=local,dest=${PKGS_DIR}/kernel/build" \
  "${PKGS_DIR}"

echo "    Verifying fscrypt configs survived olddefconfig:"
grep -E 'FS_CRYPTO|FS_ENCRYPTION|CRYPTO_CTS|CRYPTO_HKDF' "${PKGS_DIR}/kernel/build/config-amd64" | sed 's/^/      /'

# ---------------------------------------------------------------------------
# Step 3 — Build the kernel using bldr (BuildKit frontend via Pkgfile)
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 3: Building custom kernel (amd64) — this will take a while"

mkdir -p "${VMLINUZ_DIR}"

KERNEL_FS_DIR="${WORK_DIR}/kernel-amd64-fs"
mkdir -p "${KERNEL_FS_DIR}"

${BUILDX} build \
  --builder="${BUILDER_NAME}" \
  --platform linux/amd64 \
  --target kernel \
  --file "${PKGS_DIR}/Pkgfile" \
  --cache-to "type=local,dest=${KERNEL_CACHE_DIR},mode=max" \
  --cache-from "type=local,src=${KERNEL_CACHE_DIR}" \
  --output "type=local,dest=${KERNEL_FS_DIR}" \
  "${PKGS_DIR}"

VMLINUZ_SRC=$(find "${KERNEL_FS_DIR}" -name "vmlinuz" | head -1)
if [[ -z "${VMLINUZ_SRC}" ]]; then
  echo "ERROR: vmlinuz not found. Contents:"
  find "${KERNEL_FS_DIR}" | head -30
  exit 1
fi
cp "${VMLINUZ_SRC}" "${VMLINUZ_DIR}/vmlinuz"

# ---------------------------------------------------------------------------
# Step 4 — Verify vmlinuz
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 4: vmlinuz extracted"
ls -lh "${VMLINUZ_DIR}/vmlinuz"

fi  # end SKIP_KERNEL_BUILD

if [[ ! -f "${VMLINUZ_DIR}/vmlinuz" ]]; then
  echo "ERROR: vmlinuz not found at ${VMLINUZ_DIR}/vmlinuz"
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 5 — Build custom installer image (swap kernel into official installer)
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 5: Building custom installer image"

DOCKERFILE="${WORK_DIR}/Dockerfile.installer"
cat > "${DOCKERFILE}" << EOF
ARG TALOS_VERSION
FROM ghcr.io/siderolabs/installer:\${TALOS_VERSION}
COPY vmlinuz /usr/install/amd64/vmlinuz
EOF

${BUILDX} build \
  --platform linux/amd64 \
  --build-arg "TALOS_VERSION=${TALOS_VERSION}" \
  --file "${DOCKERFILE}" \
  --load \
  --tag "${INSTALLER_IMAGE}" \
  "${VMLINUZ_DIR}"

echo "    Built: ${INSTALLER_IMAGE}"

# Push so the Talos imager can pull it from the registry at runtime
echo "    Pushing: ${INSTALLER_IMAGE}"
${DOCKER} push "${INSTALLER_IMAGE}"
echo "    Pushed: ${INSTALLER_IMAGE}"

echo ""
echo "=========================================="
echo "Build complete!"
echo "Installer image: ${INSTALLER_IMAGE}"
echo "=========================================="

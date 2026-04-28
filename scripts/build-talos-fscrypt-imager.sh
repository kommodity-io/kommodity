#!/usr/bin/env bash
# Build a custom Talos imager with fscrypt (CONFIG_FS_ENCRYPTION=y) kernel support.
#
# Follows the official Talos kernel customization process:
#   Phase 1 — Build custom kernel in siderolabs/pkgs, push to build registry
#   Phase 2 — Build kernel + initramfs + imager in siderolabs/talos
#   Phase 3 — Retag and push final imager to output registry
#
# References:
#   https://docs.siderolabs.com/talos/v1.12/build-and-extend-talos/custom-images-and-development/customizing-the-kernel
#   https://docs.siderolabs.com/talos/v1.12/build-and-extend-talos/custom-images-and-development/building-images
#
# Usage:
#   ./scripts/build-talos-fscrypt-image.sh <talos_version>
#
# Example:
#   ./scripts/build-talos-fscrypt-image.sh v1.12.3
#
# Environment variables:
#   PKGS_DIR          Path to pre-cloned siderolabs/pkgs checkout (required)
#   TALOS_DIR         Path to pre-cloned siderolabs/talos checkout (required)
#   REGISTRY          Build registry for intermediate images (default: 127.0.0.1:5005)
#   OUTPUT_REGISTRY   Registry for final installer image (default: ghcr.io/kommodity-io)
#   PLATFORM          Target platform (default: linux/amd64)
#
# Caller responsibilities (script does NOT do these):
#   - Install Docker (with buildx) and configure /etc/docker/daemon.json
#   - Authenticate to ghcr.io (docker login) for the OUTPUT_REGISTRY
#   - Install crane and jq

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
TALOS_VERSION="${1:?Usage: $0 <talos_version> (e.g. v1.12.3)}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORK_DIR="${REPO_ROOT}/_out/fscrypt-build"

PKGS_DIR="${PKGS_DIR:?PKGS_DIR must point to a pre-cloned siderolabs/pkgs checkout}"
TALOS_DIR="${TALOS_DIR:?TALOS_DIR must point to a pre-cloned siderolabs/talos checkout}"

REGISTRY="${REGISTRY:-127.0.0.1:5005}"
OUTPUT_REGISTRY="${OUTPUT_REGISTRY:-ghcr.io/kommodity-io}"
PLATFORM="${PLATFORM:-linux/amd64}"
ARCH="${PLATFORM#linux/}"

IMAGER_IMAGE="${OUTPUT_REGISTRY}/kommodity-talos-imager-fscrypt:${TALOS_VERSION}"

# Kernel config required for fscrypt support.
# CONFIG_FS_ENCRYPTION is the Kconfig symbol (the internal rename to
# CONFIG_FS_CRYPTO only affects kernel source, not Kconfig).
FSCRYPT_CONFIG="CONFIG_FS_ENCRYPTION=y"

echo "=========================================="
echo "Talos fscrypt kernel builder"
echo "------------------------------------------"
echo "Talos version   : ${TALOS_VERSION}"
echo "Build registry  : ${REGISTRY}"
echo "Output image    : ${IMAGER_IMAGE}"
echo "Platform        : ${PLATFORM}"
echo "=========================================="

mkdir -p "${WORK_DIR}"

# ---------------------------------------------------------------------------
# Step 1: Start local registry if using localhost
# ---------------------------------------------------------------------------
if [[ "${REGISTRY}" == 127.0.0.1:* ]]; then
  REGISTRY_PORT="${REGISTRY#*:}"
  if ! curl -sf "http://${REGISTRY}/v2/" &>/dev/null; then
    echo ""
    echo ">>> Step 1: Starting local registry on port ${REGISTRY_PORT}"
    docker run -d --restart=always \
      -p "${REGISTRY_PORT}:5000" \
      --name talos-build-registry \
      registry:2 2>/dev/null || true
    for _ in {1..10}; do
      curl -sf "http://${REGISTRY}/v2/" &>/dev/null && break
      sleep 1
    done
  fi
fi

# ===========================================================================
# Phase 1: Build custom kernel (siderolabs/pkgs)
# ===========================================================================

# ---------------------------------------------------------------------------
# Step 2: Patch kernel config — enable fscrypt
#
# Files modified: kernel/build/config-{amd64,arm64}
# See: https://github.com/search?q=repo%3Asiderolabs%2Fpkgs+CONFIG_FS_ENCRYPTION&type=code
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 2: Patch kernel config (config-${ARCH})"

CONFIG_FILE="${PKGS_DIR}/kernel/build/config-${ARCH}"
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "ERROR: Kernel config not found: ${CONFIG_FILE}"
  echo "Available files:"
  ls "${PKGS_DIR}/kernel/build/" 2>/dev/null || echo "  (directory not found)"
  exit 1
fi

FSCRYPT_KEY="${FSCRYPT_CONFIG%%=*}"
# Remove any existing setting (both "# KEY is not set" and "KEY=..." forms)
sed -i.bak -e "/^# ${FSCRYPT_KEY} is not set/d" -e "/^${FSCRYPT_KEY}[= ]/d" "${CONFIG_FILE}"
echo "${FSCRYPT_CONFIG}" >> "${CONFIG_FILE}"
rm -f "${CONFIG_FILE}.bak"

echo "    Applied:"
grep -E 'FS_ENCRYPTION' "${CONFIG_FILE}" | grep -v '^#' | sed 's/^/      /'

# ---------------------------------------------------------------------------
# Step 3: Create buildx builder with docker-container driver
#
# The pkgs build uses bldr (BuildKit frontend) which requires the mergeop
# feature. This is only available with the docker-container driver, not the
# default docker driver.
# ---------------------------------------------------------------------------
BUILDER_NAME="talos-kernel-builder"

if ! docker buildx inspect "${BUILDER_NAME}" &>/dev/null; then
  echo ""
  echo ">>> Step 3: Creating buildx builder: ${BUILDER_NAME}"

  # BuildKit config: allow pushing to HTTP (insecure) local registry
  BUILDKIT_CFG="${WORK_DIR}/buildkitd.toml"
  cat > "${BUILDKIT_CFG}" << 'TOML'
[registry."127.0.0.1:5005"]
  http = true
  insecure = true
TOML

  # network=host so BuildKit container can reach localhost registry
  docker buildx create \
    --name "${BUILDER_NAME}" \
    --use \
    --driver-opt network=host \
    --config "${BUILDKIT_CFG}"
else
  echo ""
  echo ">>> Step 3: Using existing buildx builder: ${BUILDER_NAME}"
  docker buildx use "${BUILDER_NAME}"
fi

# ---------------------------------------------------------------------------
# Step 4: Resolve config dependencies via olddefconfig
#
# Without this, options with unmet dependencies are silently dropped.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 4: kernel-olddefconfig (resolve dependencies)"

(cd "${PKGS_DIR}" && make kernel-olddefconfig)

echo "    Post-olddefconfig verification:"
grep -E 'FS_ENCRYPTION' "${CONFIG_FILE}" | sed 's/^/      /'

# ---------------------------------------------------------------------------
# Step 5: Build custom kernel and push to build registry
#
# Creates: ${REGISTRY}/siderolabs/kernel:${TAG}
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 5: Build custom kernel → ${REGISTRY} (this takes a while)"

(cd "${PKGS_DIR}" && make kernel \
  REGISTRY="${REGISTRY}" \
  PUSH=true \
  PLATFORM="${PLATFORM}")

# ---------------------------------------------------------------------------
# Determine kernel image reference
#
# The pkgs Makefile derives the tag from git state. Query the registry to
# find what was actually pushed, falling back to the pkgs tag.
# ---------------------------------------------------------------------------
KERNEL_IMAGE=""

if [[ "${REGISTRY}" == 127.0.0.1:* ]]; then
  TAGS_JSON=$(curl -sf "http://${REGISTRY}/v2/siderolabs/kernel/tags/list" 2>/dev/null || echo "")
  if [[ -n "${TAGS_JSON}" ]]; then
    # Prefer jq, fall back to grep
    KERNEL_TAG=$(echo "${TAGS_JSON}" | jq -r '.tags[0]' 2>/dev/null) || \
      KERNEL_TAG=$(echo "${TAGS_JSON}" | grep -oP '"[^"]+(?=")' | tail -1) || true
    if [[ -n "${KERNEL_TAG}" && "${KERNEL_TAG}" != "null" ]]; then
      KERNEL_IMAGE="${REGISTRY}/siderolabs/kernel:${KERNEL_TAG}"
    fi
  fi
fi

if [[ -z "${KERNEL_IMAGE}" ]]; then
  KERNEL_TAG=$(git -C "${PKGS_DIR}" describe --tag --always 2>/dev/null || echo "${TALOS_VERSION}")
  KERNEL_IMAGE="${REGISTRY}/siderolabs/kernel:${KERNEL_TAG}"
fi

echo ""
echo "    Kernel image: ${KERNEL_IMAGE}"

# ===========================================================================
# Phase 2: Build Talos artifacts (siderolabs/talos)
# ===========================================================================

# ---------------------------------------------------------------------------
# Step 6: Build kernel + initramfs with custom kernel
#
# This repackages our custom kernel into Talos boot artifacts.
# Output: _out/vmlinuz-${ARCH} and _out/initramfs-${ARCH}.xz
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 6: Build kernel + initramfs"

(cd "${TALOS_DIR}" && make kernel initramfs \
  PKG_KERNEL="${KERNEL_IMAGE}" \
  PLATFORM="${PLATFORM}")

echo "    Output:"
ls -lh "${TALOS_DIR}/_out/vmlinuz-${ARCH}" "${TALOS_DIR}/_out/initramfs-${ARCH}.xz" 2>/dev/null | sed 's/^/      /'

# ---------------------------------------------------------------------------
# Step 7: Build imager with custom kernel
#
# The imager is a container that generates Talos boot assets (ISO,
# disk images). The kernel is bundled into the imager at
# /usr/install/${ARCH}/vmlinuz, so disk images produced by it carry the
# custom kernel.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 7: Build imager → ${REGISTRY}"

(cd "${TALOS_DIR}" && make imager \
  PKG_KERNEL="${KERNEL_IMAGE}" \
  PLATFORM="${PLATFORM}" \
  INSTALLER_ARCH=targetarch \
  PUSH=true \
  REGISTRY="${REGISTRY}")

# ===========================================================================
# Phase 3: Tag and push final imager
# ===========================================================================

# ---------------------------------------------------------------------------
# Step 8: Push custom imager to output registry
#
# The imager contains the custom kernel and is needed by the cloud-image
# workflow to produce disk images with the fscrypt-enabled kernel.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 8: Push imager → ${IMAGER_IMAGE}"

IMAGER_TAG=""
if [[ "${REGISTRY}" == 127.0.0.1:* ]]; then
  TAGS_JSON=$(curl -sf "http://${REGISTRY}/v2/siderolabs/imager/tags/list" 2>/dev/null || echo "")
  if [[ -n "${TAGS_JSON}" ]]; then
    IMAGER_TAG=$(echo "${TAGS_JSON}" | jq -r '.tags[0]' 2>/dev/null || echo "")
  fi
fi
if [[ -z "${IMAGER_TAG}" || "${IMAGER_TAG}" == "null" ]]; then
  IMAGER_TAG="${TALOS_VERSION}-dirty"
fi

BUILT_IMAGER="${REGISTRY}/siderolabs/imager:${IMAGER_TAG}"
echo "    Source: ${BUILT_IMAGER}"

if command -v crane &>/dev/null; then
  crane copy "${BUILT_IMAGER}" "${IMAGER_IMAGE}"
else
  docker pull "${BUILT_IMAGER}"
  docker tag "${BUILT_IMAGER}" "${IMAGER_IMAGE}"
  docker push "${IMAGER_IMAGE}"
fi

echo ""
echo "=========================================="
echo "Build complete!"
echo "Imager: ${IMAGER_IMAGE}"
echo "=========================================="

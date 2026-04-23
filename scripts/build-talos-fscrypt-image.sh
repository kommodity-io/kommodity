#!/usr/bin/env bash
# Build a custom Talos installer with fscrypt (CONFIG_FS_ENCRYPTION=y) kernel support.
#
# Follows the official Talos kernel customization process:
#   Phase 1 — Build custom kernel in siderolabs/pkgs, push to build registry
#   Phase 2 — Build kernel + initramfs + imager + installer in siderolabs/talos
#   Phase 3 — Retag and push final installer to output registry
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
#   REGISTRY          Build registry for intermediate images (default: 127.0.0.1:5005)
#   OUTPUT_REGISTRY   Registry for final installer image (default: ghcr.io/kommodity-io)
#   PLATFORM          Target platform (default: linux/amd64)
#   SKIP_KERNEL       Set "true" to skip kernel build and reuse previously built kernel
#
# Requirements:
#   - docker + docker buildx
#   - git
#   - jq (optional, for registry tag discovery)

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
TALOS_VERSION="${1:?Usage: $0 <talos_version> (e.g. v1.12.3)}"

# pkgs repo doesn't publish per-patch tags; v1.12.3 → v1.12.0
PKGS_TAG="${TALOS_VERSION%.*}.0"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORK_DIR="${REPO_ROOT}/_out/fscrypt-build"
PKGS_DIR="${WORK_DIR}/pkgs"
TALOS_DIR="${WORK_DIR}/talos"

REGISTRY="${REGISTRY:-127.0.0.1:5005}"
OUTPUT_REGISTRY="${OUTPUT_REGISTRY:-ghcr.io/kommodity-io}"
PLATFORM="${PLATFORM:-linux/amd64}"
ARCH="${PLATFORM#linux/}"

SKIP_KERNEL="${SKIP_KERNEL:-false}"

INSTALLER_IMAGE="${OUTPUT_REGISTRY}/kommodity-talos-installer-fscrypt:${TALOS_VERSION}"

# Kernel configs required for fscrypt support.
# CONFIG_FS_ENCRYPTION is the Kconfig symbol (the internal rename to
# CONFIG_FS_CRYPTO only affects kernel source, not Kconfig).
FSCRYPT_CONFIGS=(
  "CONFIG_FS_ENCRYPTION=y"  # Core fscrypt support (built-in, not module)
  "CONFIG_CRYPTO_CTS=y"     # Cipher-text stealing (filename encryption)
  "CONFIG_CRYPTO_HKDF=y"    # HKDF key derivation (must be built-in)
)

echo "=========================================="
echo "Talos fscrypt kernel builder"
echo "------------------------------------------"
echo "Talos version   : ${TALOS_VERSION}"
echo "Pkgs tag        : ${PKGS_TAG}"
echo "Build registry  : ${REGISTRY}"
echo "Output image    : ${INSTALLER_IMAGE}"
echo "Platform        : ${PLATFORM}"
echo "Skip kernel     : ${SKIP_KERNEL}"
echo "=========================================="

mkdir -p "${WORK_DIR}"

# ---------------------------------------------------------------------------
# Step -1: Setup
# ---------------------------------------------------------------------------

export DEBIAN_FRONTEND=noninteractive
curl -fsSL https://get.docker.com | sh

cat > /etc/docker/daemon.json << 'EOF'
{
  "storage-driver": "overlay2",
  "default-ulimits": {
    "nofile":  {"name": "nofile",  "soft": 1048576, "hard": 1048576},
    "memlock": {"name": "memlock", "soft": -1,      "hard": -1}
  }
}
EOF

systemctl restart docker
systemctl enable docker

docker login ghcr.io -u "${GITHUB_ACTOR:-pthuriot-corti}" --password-stdin <<< "${GITHUB_TOKEN}"

apt install make -y

# ---------------------------------------------------------------------------
# Step 0: Start local registry if using localhost
# ---------------------------------------------------------------------------
if [[ "${REGISTRY}" == 127.0.0.1:* ]]; then
  REGISTRY_PORT="${REGISTRY#*:}"
  if ! curl -sf "http://${REGISTRY}/v2/" &>/dev/null; then
    echo ""
    echo ">>> Step 0: Starting local registry on port ${REGISTRY_PORT}"
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

if [[ "${SKIP_KERNEL}" != "true" ]]; then

# ---------------------------------------------------------------------------
# Step 1: Clone siderolabs/pkgs
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 1: Clone siderolabs/pkgs @ ${PKGS_TAG}"

if [[ -d "${PKGS_DIR}/.git" ]]; then
  echo "    Already cloned — checking out ${PKGS_TAG}"
  git -C "${PKGS_DIR}" fetch --tags --quiet
  git -C "${PKGS_DIR}" checkout "${PKGS_TAG}" --quiet
else
  git clone --branch "${PKGS_TAG}" --depth 1 \
    https://github.com/siderolabs/pkgs.git "${PKGS_DIR}"
fi

# ---------------------------------------------------------------------------
# Step 2: Patch kernel config — enable fscrypt + dependencies
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

for kconf in "${FSCRYPT_CONFIGS[@]}"; do
  key="${kconf%%=*}"
  # Remove any existing setting (both "# KEY is not set" and "KEY=..." forms)
  sed -i.bak -e "/^# ${key} is not set/d" -e "/^${key}[= ]/d" "${CONFIG_FILE}"
  echo "${kconf}" >> "${CONFIG_FILE}"
done
rm -f "${CONFIG_FILE}.bak"

echo "    Applied:"
grep -E 'FS_ENCRYPTION|CRYPTO_CTS|CRYPTO_HKDF' "${CONFIG_FILE}" | grep -v '^#' | sed 's/^/      /'

# ---------------------------------------------------------------------------
# Step 2b: Create buildx builder with docker-container driver
#
# The pkgs build uses bldr (BuildKit frontend) which requires the mergeop
# feature. This is only available with the docker-container driver, not the
# default docker driver.
# ---------------------------------------------------------------------------
BUILDER_NAME="talos-kernel-builder"

if ! docker buildx inspect "${BUILDER_NAME}" &>/dev/null; then
  echo ""
  echo ">>> Step 2b: Creating buildx builder: ${BUILDER_NAME}"

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
  echo ">>> Step 2b: Using existing buildx builder: ${BUILDER_NAME}"
  docker buildx use "${BUILDER_NAME}"
fi

# ---------------------------------------------------------------------------
# Step 3: Resolve config dependencies via olddefconfig
#
# Without this, options with unmet dependencies are silently dropped.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 3: kernel-olddefconfig (resolve dependencies)"

(cd "${PKGS_DIR}" && make kernel-olddefconfig)

echo "    Post-olddefconfig verification:"
grep -E 'FS_ENCRYPTION|CRYPTO_CTS|CRYPTO_HKDF' "${CONFIG_FILE}" | sed 's/^/      /'

# ---------------------------------------------------------------------------
# Step 4: Build custom kernel and push to build registry
#
# Creates: ${REGISTRY}/siderolabs/kernel:${TAG}
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 4: Build custom kernel → ${REGISTRY} (this takes a while)"

(cd "${PKGS_DIR}" && make kernel \
  REGISTRY="${REGISTRY}" \
  PUSH=true \
  PLATFORM="${PLATFORM}")

fi  # end SKIP_KERNEL

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
  KERNEL_TAG=$(git -C "${PKGS_DIR}" describe --tag --always 2>/dev/null || echo "${PKGS_TAG}")
  KERNEL_IMAGE="${REGISTRY}/siderolabs/kernel:${KERNEL_TAG}"
fi

echo ""
echo "    Kernel image: ${KERNEL_IMAGE}"

# ===========================================================================
# Phase 2: Build Talos artifacts (siderolabs/talos)
# ===========================================================================

# ---------------------------------------------------------------------------
# Step 5: Clone siderolabs/talos
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 5: Clone siderolabs/talos @ ${TALOS_VERSION}"

if [[ -d "${TALOS_DIR}/.git" ]]; then
  echo "    Already cloned — checking out ${TALOS_VERSION}"
  git -C "${TALOS_DIR}" fetch --tags --quiet
  git -C "${TALOS_DIR}" checkout "${TALOS_VERSION}" --quiet
else
  git clone --branch "${TALOS_VERSION}" --depth 1 \
    https://github.com/siderolabs/talos.git "${TALOS_DIR}"
fi

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
# The imager is a container that generates Talos boot assets (ISO, installer,
# disk images). We push it to the build registry for Step 8.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 7: Build imager → ${REGISTRY}"

(cd "${TALOS_DIR}" && make imager \
  PKG_KERNEL="${KERNEL_IMAGE}" \
  PLATFORM="${PLATFORM}" \
  INSTALLER_ARCH=targetarch \
  PUSH=true \
  REGISTRY="${REGISTRY}")

# ---------------------------------------------------------------------------
# Step 8: Build installer using custom imager
#
# The installer image is what Talos uses to install/upgrade nodes.
# We build it via the custom imager from Step 7.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 8: Build installer"

(cd "${TALOS_DIR}" && make installer \
  PKG_KERNEL="${KERNEL_IMAGE}" \
  PLATFORM="${PLATFORM}" \
  REGISTRY="${REGISTRY}")

# ===========================================================================
# Phase 3: Tag and push final installer
# ===========================================================================

# ---------------------------------------------------------------------------
# Step 9: Retag and push to output registry
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 9: Push installer → ${INSTALLER_IMAGE}"

BUILT_INSTALLER="${REGISTRY}/siderolabs/installer:${TALOS_VERSION}"

# Image may be in local docker (--load) or build registry; handle both
docker pull "${BUILT_INSTALLER}" 2>/dev/null || true

if ! docker inspect "${BUILT_INSTALLER}" &>/dev/null; then
  echo "ERROR: Installer image not found: ${BUILT_INSTALLER}"
  echo "Check 'make installer' output above for the actual image reference."
  echo ""
  echo "Available images in local docker:"
  docker images --format '{{.Repository}}:{{.Tag}}' | grep -i installer | head -10
  if [[ "${REGISTRY}" == 127.0.0.1:* ]]; then
    echo ""
    echo "Available tags in registry:"
    curl -sf "http://${REGISTRY}/v2/siderolabs/installer/tags/list" 2>/dev/null || echo "  (none)"
  fi
  exit 1
fi

docker tag "${BUILT_INSTALLER}" "${INSTALLER_IMAGE}"
docker push "${INSTALLER_IMAGE}"

echo ""
echo "=========================================="
echo "Build complete!"
echo "Installer: ${INSTALLER_IMAGE}"
echo "=========================================="

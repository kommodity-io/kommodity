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

apt install make jq -y

# Install crane (for pushing OCI images)
if ! command -v crane &>/dev/null; then
  CRANE_VERSION="0.20.3"
  curl -sL "https://github.com/google/go-containerregistry/releases/download/v${CRANE_VERSION}/go-containerregistry_Linux_x86_64.tar.gz" \
    | tar -xz -C /usr/local/bin crane
fi

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
# Phase 0b: Clone talos first to determine correct pkgs version
# ===========================================================================
echo ""
echo ">>> Clone siderolabs/talos @ ${TALOS_VERSION}"

if [[ -d "${TALOS_DIR}/.git" ]]; then
  echo "    Already cloned — checking out ${TALOS_VERSION}"
  git -C "${TALOS_DIR}" fetch --tags --quiet
  git -C "${TALOS_DIR}" checkout "${TALOS_VERSION}" --quiet
else
  git clone --branch "${TALOS_VERSION}" --depth 1 \
    https://github.com/siderolabs/talos.git "${TALOS_DIR}"
fi

# Extract the pkgs version that this talos release expects.
# The talos Makefile defines PKGS as the expected pkgs git ref.
PKGS_REF=$(grep -oP '^\s*PKGS\s*\?=\s*\K\S+' "${TALOS_DIR}/Makefile" || true)
if [[ -z "${PKGS_REF}" ]]; then
  # Fallback: use a default derived from the kernel package refs in Makefile
  PKGS_REF=$(grep -oP 'PKG_KERNEL\s*\?=.*:\K[^\s"]+' "${TALOS_DIR}/Makefile" || true)
fi
if [[ -z "${PKGS_REF}" ]]; then
  # Last resort fallback
  PKGS_REF="${TALOS_VERSION%.*}.0"
fi
echo "    Pkgs ref: ${PKGS_REF}"

# ===========================================================================
# Phase 1: Build custom kernel (siderolabs/pkgs)
# ===========================================================================

if [[ "${SKIP_KERNEL}" != "true" ]]; then

# ---------------------------------------------------------------------------
# Step 1: Clone siderolabs/pkgs at the version talos expects
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 1: Clone siderolabs/pkgs @ ${PKGS_REF}"

# PKGS_REF may be a tag (v1.12.0) or a git-describe ref (v1.12.0-35-g15d5d78).
# Parse into usable components.
if [[ "${PKGS_REF}" =~ -[0-9]+-g([0-9a-f]+)$ ]]; then
  # Describe-style ref: extract commit hash and release branch
  PKGS_COMMIT="${BASH_REMATCH[1]}"
  PKGS_MINOR="${PKGS_REF#v}"       # 1.12.0-35-g15d5d78
  PKGS_MINOR="${PKGS_MINOR%%-*}"   # 1.12.0
  PKGS_MINOR="${PKGS_MINOR%.*}"    # 1.12
  PKGS_BRANCH="release-${PKGS_MINOR}"
  echo "    Parsed: branch=${PKGS_BRANCH} commit=${PKGS_COMMIT}"
else
  # Simple tag/branch ref
  PKGS_COMMIT=""
  PKGS_BRANCH="${PKGS_REF}"
fi

# Remove stale shallow clone if it exists (can't reach other commits)
if [[ -d "${PKGS_DIR}/.git" ]] && [[ -n "${PKGS_COMMIT}" ]]; then
  if ! git -C "${PKGS_DIR}" cat-file -e "${PKGS_COMMIT}" 2>/dev/null; then
    echo "    Removing stale shallow clone (commit ${PKGS_COMMIT} not reachable)"
    rm -rf "${PKGS_DIR}"
  fi
fi

if [[ -d "${PKGS_DIR}/.git" ]]; then
  echo "    Already cloned — checking out ${PKGS_REF}"
  git -C "${PKGS_DIR}" fetch --tags --quiet
  git -C "${PKGS_DIR}" checkout "${PKGS_COMMIT:-${PKGS_REF}}" --quiet
else
  if [[ -n "${PKGS_COMMIT}" ]]; then
    # Need full branch history to reach the specific commit
    echo "    Cloning ${PKGS_BRANCH} branch (need commit ${PKGS_COMMIT})"
    git clone --branch "${PKGS_BRANCH}" \
      https://github.com/siderolabs/pkgs.git "${PKGS_DIR}"
    git -C "${PKGS_DIR}" checkout "${PKGS_COMMIT}"
  else
    git clone --branch "${PKGS_BRANCH}" --depth 1 \
      https://github.com/siderolabs/pkgs.git "${PKGS_DIR}"
  fi
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
  KERNEL_TAG=$(git -C "${PKGS_DIR}" describe --tag --always 2>/dev/null || echo "${PKGS_REF}")
  KERNEL_IMAGE="${REGISTRY}/siderolabs/kernel:${KERNEL_TAG}"
fi

echo ""
echo "    Kernel image: ${KERNEL_IMAGE}"

# ===========================================================================
# Phase 2: Build Talos artifacts (siderolabs/talos)
# ===========================================================================

# ---------------------------------------------------------------------------
# Step 5: Validate hack/modules-amd64.txt against actual kernel contents
#
# Remove any module from the list that doesn't exist in the kernel image.
# Causes: configs changed from =m to =y (built-in), or configs disabled
# by olddefconfig, or modules not present at this pkgs commit.
# ---------------------------------------------------------------------------
MODULES_FILE="${TALOS_DIR}/hack/modules-${ARCH}.txt"
if [[ -f "${MODULES_FILE}" ]]; then
  echo ""
  echo ">>> Step 5: Validate modules list against kernel image"

  # Extract module tree from kernel image
  KERNEL_MOD_CHECK="${WORK_DIR}/kernel-modules-check"
  rm -rf "${KERNEL_MOD_CHECK}"
  mkdir -p "${KERNEL_MOD_CHECK}"

  docker pull "${KERNEL_IMAGE}"
  CID=$(docker create "${KERNEL_IMAGE}" /bin/true)
  docker cp "${CID}:/usr/lib/modules" "${KERNEL_MOD_CHECK}/" 2>/dev/null || true
  docker rm "${CID}" &>/dev/null || true

  KERNEL_VERSION=$(ls "${KERNEL_MOD_CHECK}/modules/" 2>/dev/null | head -1)
  KERNEL_MOD_DIR="${KERNEL_MOD_CHECK}/modules/${KERNEL_VERSION}"

  if [[ -d "${KERNEL_MOD_DIR}" ]]; then
    echo "    Kernel version: ${KERNEL_VERSION}"
    echo "    Module dir: ${KERNEL_MOD_DIR}"

    # Filter: keep only modules that exist in the kernel image
    TMP_MODULES=$(mktemp)
    REMOVED=0
    while IFS= read -r mod_path; do
      # Skip empty lines and comments
      [[ -z "${mod_path}" || "${mod_path}" =~ ^[[:space:]]*# ]] && continue
      if [[ -f "${KERNEL_MOD_DIR}/${mod_path}" ]]; then
        echo "${mod_path}" >> "${TMP_MODULES}"
      else
        echo "    Removing: ${mod_path}"
        ((REMOVED++)) || true
      fi
    done < "${MODULES_FILE}"
    mv "${TMP_MODULES}" "${MODULES_FILE}"

    echo "    Removed ${REMOVED} unavailable modules, $(wc -l < "${MODULES_FILE}") remaining"
  else
    echo "    WARNING: Could not extract modules from kernel image, skipping validation"
    echo "    Contents: $(ls "${KERNEL_MOD_CHECK}/" 2>/dev/null)"
  fi

  rm -rf "${KERNEL_MOD_CHECK}"
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
# Step 7a: Build installer-base
#
# The installer-base contains the base filesystem for the installer image.
# Must be pushed to registry before the imager can build the installer.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 7a: Build installer-base → ${REGISTRY}"

(cd "${TALOS_DIR}" && make installer-base \
  PKG_KERNEL="${KERNEL_IMAGE}" \
  PLATFORM="${PLATFORM}" \
  PUSH=true \
  REGISTRY="${REGISTRY}")

# ---------------------------------------------------------------------------
# Step 7b: Build imager with custom kernel
#
# The imager is a container that generates Talos boot assets (ISO, installer,
# disk images). We push it to the build registry for Step 8.
# ---------------------------------------------------------------------------
echo ""
echo ">>> Step 7b: Build imager → ${REGISTRY}"

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

# Talos tags images with git describe, which includes -dirty when
# the worktree is modified (e.g. from our modules-amd64.txt patch).
# Discover the actual tag from the registry.
INSTALLER_TAG=""
if [[ "${REGISTRY}" == 127.0.0.1:* ]]; then
  TAGS_JSON=$(curl -sf "http://${REGISTRY}/v2/siderolabs/installer/tags/list" 2>/dev/null || echo "")
  if [[ -n "${TAGS_JSON}" ]]; then
    INSTALLER_TAG=$(echo "${TAGS_JSON}" | jq -r '.tags[0]' 2>/dev/null || echo "")
  fi
fi
if [[ -z "${INSTALLER_TAG}" || "${INSTALLER_TAG}" == "null" ]]; then
  # Fallback: try common variants
  INSTALLER_TAG="${TALOS_VERSION}-dirty"
fi

BUILT_INSTALLER="${REGISTRY}/siderolabs/installer:${INSTALLER_TAG}"
echo "    Source: ${BUILT_INSTALLER}"

# crane copy is more efficient (no local pull needed), fall back to docker
if command -v crane &>/dev/null; then
  crane copy "${BUILT_INSTALLER}" "${INSTALLER_IMAGE}"
else
  docker pull "${BUILT_INSTALLER}"
  docker tag "${BUILT_INSTALLER}" "${INSTALLER_IMAGE}"
  docker push "${INSTALLER_IMAGE}"
fi

echo ""
echo "=========================================="
echo "Build complete!"
echo "Installer: ${INSTALLER_IMAGE}"
echo "=========================================="

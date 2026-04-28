# Talos Custom Kernel with fscrypt

This document describes how we build, verify, and deploy Talos images with a
custom fscrypt-enabled kernel. It covers the full pipeline from kernel
compilation to disk image creation across cloud platforms.

## Background

Talos disk encryption with fscrypt requires `CONFIG_FS_ENCRYPTION=y` in the
kernel. The official Talos kernel does not enable this option.
We maintain a custom kernel build that adds fscrypt support, producing an
**imager** for generating platform-specific disk images (Scaleway qcow2,
Azure VHD, raw, ISO, etc.).

The kernel is bundled inside the imager at `/usr/install/${ARCH}/vmlinuz`, so
disk images produced by the custom imager carry the fscrypt-enabled kernel.
We do not build a custom installer — the official `siderolabs/installer` is
used as `baseInstaller` for initramfs/extensions, since it does not affect
the kernel in the resulting disk image.

## Architecture

```text
siderolabs/pkgs (kernel source)
  │  patch config-${ARCH}: CONFIG_FS_ENCRYPTION=y
  ▼
Custom kernel image ──► local registry (127.0.0.1:5005)
  │
  ▼
siderolabs/talos (build system)
  ├── make kernel initramfs ──► vmlinuz + initramfs.xz
  └── make imager PUSH=true ──► kommodity-talos-imager-fscrypt (GHCR)
                                        │
                                        ▼
                            GH Workflow: talos-cloud-image.yml
                                        │  uses custom imager
                                        ▼
                            Cloud disk image (qcow2 / VHD / raw)
                            (GHCR + target cloud)
```

## Building the Custom Kernel and Imager

### GitHub Actions (preferred)

`.github/workflows/talos-fscrypt-imager.yml` runs the full build on a
GitHub-hosted runner. Trigger via `workflow_dispatch`:

**Inputs:**

- `talos_version`: e.g. `v1.12.3` (required)
- `platform`: `linux/amd64` or `linux/arm64`

The workflow:

1. Frees disk space on the runner
2. Computes the matching `pkgs` branch from `talos_version`
   (`v1.12.3` → `release-1.12`)
3. Checks out kommodity, `siderolabs/pkgs@release-<minor>`, and
   `siderolabs/talos@<talos_version>`
4. Runs `scripts/build-talos-fscrypt-imager.sh` with `PKGS_DIR` and
   `TALOS_DIR` env vars pointing at the checked-out repos
5. Writes a step summary with the published imager ref

The kernel build takes ~40-60 minutes on `ubuntu-latest`. If the runner
runs out of disk/RAM, switch to a larger runner.

### Local / dedicated VM (alternative)

The script can run on any host with Docker and `git` (~32 vCPU, 128 GB RAM
recommended). It expects `siderolabs/pkgs` and `siderolabs/talos` to be
cloned ahead of time:

```bash
git clone --branch release-1.12 --depth 1 \
  https://github.com/siderolabs/pkgs.git /tmp/pkgs
git clone --branch v1.12.3 --depth 1 \
  https://github.com/siderolabs/talos.git /tmp/talos

PKGS_DIR=/tmp/pkgs \
TALOS_DIR=/tmp/talos \
GITHUB_TOKEN=<YOUR_GHCR_TOKEN> \
  ./scripts/build-talos-fscrypt-imager.sh v1.12.3
```

The script installs Docker, crane, and jq; logs in to GHCR (using
`GITHUB_TOKEN` and `GITHUB_ACTOR`); and runs the build.

### `scripts/build-talos-fscrypt-imager.sh` — Step by Step

Caller responsibilities (script does NOT do these — workflow / VM owner
must set up): install Docker + buildx, write `/etc/docker/daemon.json`
with `nofile` / `memlock` ulimits, `docker login ghcr.io`, install
`crane` and `jq`.

| Step | What                                | Make target / Command                                           |
| ---- | ----------------------------------- | --------------------------------------------------------------- |
| 1    | Start local registry                | `docker run registry:2` on `127.0.0.1:5005`                     |
| 2    | Patch `kernel/build/config-${ARCH}` | Enables `CONFIG_FS_ENCRYPTION=y`                                |
| 3    | Create buildx builder               | `docker-container` driver (required by `bldr` mergeop)          |
| 4    | `make kernel-olddefconfig`          | Resolves Kconfig dependencies                                   |
| 5    | `make kernel PUSH=true`             | Builds kernel in `pkgs`, pushes to local registry               |
| 6    | `make kernel initramfs`             | Repackages custom kernel into Talos boot artifacts              |
| 7    | `make imager PUSH=true`             | **Imager container with custom kernel** (local registry)        |
| 8    | Crane copy to GHCR                  | `ghcr.io/kommodity-io/kommodity-talos-imager-fscrypt:<version>` |

Required env vars: `PKGS_DIR`, `TALOS_DIR`, `GITHUB_TOKEN`.
Optional: `REGISTRY` (default `127.0.0.1:5005`), `OUTPUT_REGISTRY`
(default `ghcr.io/kommodity-io`), `PLATFORM` (default `linux/amd64`),
`GITHUB_ACTOR`.

## Building Cloud Images

### `.github/workflows/talos-cloud-image.yml`

Builds platform-specific disk images from a Talos imager. Supports
multiple platforms via a build matrix (e.g. `scaleway`, `azure`).

1. Runs the **imager** container for each platform
2. Decompresses and converts output (e.g. raw to qcow2, raw to VHD)
3. Pushes as OCI artifact to GHCR via `oras`

**Key inputs:**

- `talos_version`: e.g. `v1.12.3` (defaults to `v1.12.3`)
- `platforms`: `all`, or a comma-separated list (`scaleway`, `azure`, ...)
- `custom_imager_image`: custom imager (for kernel) — **must be set for
  fscrypt builds**
- `extensions`: comma-separated extension image refs (`none` to skip,
  empty for defaults)

**Important:** The imager container bundles the kernel at
`/usr/install/${ARCH}/vmlinuz`. The `baseInstaller` only affects extensions
and initramfs overlay, **not the kernel**. To get a custom kernel into the
output disk image, you must use a custom imager via `custom_imager_image`.
The workflow always uses the official `siderolabs/installer` as
`baseInstaller`.

Example workflow dispatch for fscrypt:

```yaml
talos_version: v1.12.3
platforms: scaleway,azure
custom_imager_image: ghcr.io/kommodity-io/kommodity-talos-imager-fscrypt:v1.12.3
```

## Deploying to a Cloud Provider

The disk-image artifact lives in GHCR as an OCI artifact. Pull with
`oras`, then upload to your cloud provider's image storage.

```bash
oras pull ghcr.io/kommodity-io/kommodity-talos-<platform>:<version>
```

Provider-specific upload steps below.

### Scaleway (qcow2)

Scaleway uses S3-compatible storage — upload the qcow2 to a bucket, then
import as a snapshot and create an image:

```bash
# Get credentials from scw CLI config
eval $(scw object config get type=rclone 2>/dev/null \
  | grep -E '(access_key_id|secret_access_key)' \
  | sed 's/access_key_id/AWS_ACCESS_KEY_ID/' \
  | sed 's/secret_access_key/AWS_SECRET_ACCESS_KEY/' \
  | sed 's/ = /=/' \
  | sed 's/^/export /')

aws s3 cp kommodity-talos-scaleway-<version>.qcow2 \
  s3://talos-image-storage/ \
  --endpoint-url https://s3.fr-par.scw.cloud \
  --region fr-par

scw instance snapshot create \
  zone=fr-par-2 \
  name=kommodity-talos-scaleway-<version> \
  volume-type=l_ssd \
  bucket=talos-image-storage \
  key=kommodity-talos-scaleway-<version>.qcow2 \
  --wait

scw instance image create \
  zone=fr-par-2 \
  name=kommodity-talos-scaleway-<version> \
  arch=x86_64 \
  snapshot-id=<snapshot-id>

scw instance server create \
  zone=fr-par-2 \
  type=DEV1-S \
  name=my-talos-node \
  image=<IMAGE_ID> \
  ip=new
```

### Azure (VHD)

Upload the VHD to Azure Blob Storage as a page blob, then create a
managed image:

```bash
az storage blob upload \
  --account-name <storage-account> \
  --container-name <container> \
  --name kommodity-talos-azure-<version>.vhd \
  --file kommodity-talos-azure-<version>.vhd \
  --type page

az image create \
  --resource-group <resource-group> \
  --name kommodity-talos-azure-<version> \
  --os-type Linux \
  --hyper-v-generation V2 \
  --source https://<storage-account>.blob.core.windows.net/<container>/kommodity-talos-azure-<version>.vhd
```

### Other platforms

For raw / ISO / other platform formats, follow the cloud provider's
custom-image import procedure. The disk image already contains the Talos
fscrypt-enabled kernel — no further customization required.

## Verifying Kernel Config

### On a running Talos node (preferred)

Requires `talosctl` with valid credentials for the node:

```bash
talosctl read /proc/config.gz \
  --nodes <IP> --endpoints <IP> \
  --talosconfig <path> \
  | gunzip | grep CONFIG_FS_ENCRYPTION
```

For unconfigured nodes in maintenance mode, you must first apply a machine
config (`talosctl apply-config --insecure`) since `read` requires
authenticated access.

### From an imager image (without a running node)

The imager image
(`ghcr.io/kommodity-io/kommodity-talos-imager-fscrypt:<version>`) contains
the kernel at `/usr/install/amd64/vmlinuz` (and a UKI at
`/usr/install/amd64/vmlinuz.efi`). The kernel config is embedded inside.

Extraction process:

1. **Extract the UKI from the container:**

   ```bash
   CID=$(docker create <imager-image>)
   docker export $CID | tar -xf - -C /tmp/imager usr/install/amd64/
   docker rm $CID
   chmod +r /tmp/imager/usr/install/amd64/vmlinuz.efi
   ```

2. **Extract the `.linux` section from the PE/UKI:**
   The UKI is a PE executable with sections. The `.linux` section
   contains the bzImage.

   ```bash
   objdump --headers /tmp/imager/usr/install/amd64/vmlinuz.efi
   # Look for .linux section, note offset and size
   ```

   Use a script to extract the `.linux` section via PE header parsing
   (python `struct` module works).

3. **Decompress vmlinux from bzImage:**
   The bzImage header at offset `0x248` contains the payload offset, and
   `0x24c` contains the payload length. The payload is zstd-compressed
   (magic `28 b5 2f fd`).

   **Important:** The kernel uses a 128MB zstd window size, which exceeds
   the default limit of the `zstd` CLI tool. Use streaming decompression
   via `libzstd` (`ZSTD_decompressStream` with `ZSTD_d_windowLogMax`
   set to 28).

4. **Extract config from vmlinux:**
   Search for `IKCFG_ST` marker in the decompressed vmlinux. The config
   is a standard gzip stream between `IKCFG_ST` and `IKCFG_ED`.

   ```python
   import gzip, io
   idx = vmlinux.find(b"IKCFG_ST")
   blob = vmlinux[idx+8 : vmlinux.find(b"IKCFG_ED", idx+8)]
   config = gzip.GzipFile(fileobj=io.BytesIO(blob)).read().decode()
   ```

**Gotcha:** The bzImage-level `IKCFG_ST` (before decompressing vmlinux)
contains a gzip stream with a non-standard flags byte (`0x94`, reserved bit
set). This breaks standard gzip libraries. Always decompress the full vmlinux
first, then extract IKCFG from the decompressed data where the gzip header is
clean (`0x00` flags).

### Expected results

| Image                                            | `CONFIG_FS_ENCRYPTION`              |
| ------------------------------------------------ | ----------------------------------- |
| Official Talos imager (`siderolabs/imager`)      | `# CONFIG_FS_ENCRYPTION is not set` |
| Custom imager (`kommodity-talos-imager-fscrypt`) | `CONFIG_FS_ENCRYPTION=y`            |
| Cloud image built with **official** imager       | `# CONFIG_FS_ENCRYPTION is not set` |
| Cloud image built with **custom** imager         | `CONFIG_FS_ENCRYPTION=y`            |

## Known Pitfall

Earlier builds of `kommodity-talos-scaleway-v1.12.3-fscrypt-v6` were
produced using the official `siderolabs/imager` instead of the custom
`kommodity-talos-imager-fscrypt`. The workflow's `custom_installer_image`
input only affects extensions/initramfs — the **kernel comes from the imager
container**. The resulting cloud images lacked `CONFIG_FS_ENCRYPTION=y`
despite filenames containing "fscrypt".

**Resolution:** Use the `custom_imager_image` input on
`talos-cloud-image.yml`, pointing at the imager pushed by
`talos-fscrypt-imager.yml`. The kernel travels with the imager, not with
`baseInstaller`.

# Talos Custom Kernel: Scaleway Image Pipeline

This document describes how we build, verify, and deploy Talos images with a
custom fscrypt-enabled kernel to Scaleway. It covers the full pipeline from
kernel compilation to VM creation.

## Background

Talos disk encryption with fscrypt requires `CONFIG_FS_ENCRYPTION=y` in the
kernel. The official Talos kernel does not enable this option.
We maintain a custom kernel build that adds fscrypt support, producing an
**imager** (for generating platform-specific disk images like Scaleway qcow2).

The kernel is bundled inside the imager at `/usr/install/${ARCH}/vmlinuz`, so
disk images produced by the custom imager carry the fscrypt-enabled kernel.
We do not build a custom installer — the official `siderolabs/installer` is
used as `baseInstaller` for initramfs/extensions, since it does not affect
the kernel in the resulting disk image.

## Architecture

```text
siderolabs/pkgs (kernel source)
  │  patch config-amd64: CONFIG_FS_ENCRYPTION=y
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
                            Scaleway qcow2 disk image (GHCR + Scaleway)
```

## Building the Custom Kernel and Imager

### Prerequisites

The build requires significant CPU/RAM (~32 vCPU, 128 GB recommended).
The script installs Docker, crane, jq, and other dependencies automatically
— just run it on a fresh Ubuntu VM.

#### 1. Create a build VM on Scaleway

```bash
scw instance server create \
  zone=fr-par-1 \
  project-id=a76e6396-6a8e-4406-a1dc-ad4024c60295 \
  type=POP2-32C-128G \
  image=ubuntu_noble \
  name=talos-kernel-builder \
  root-volume=sbs:150GB \
  ip=new \
  tags.0=talos-build \
  --wait
```

Wait a few minutes for cloud-init to finish (it may reboot once),
then SSH in:

```bash
ssh root@<PUBLIC_IP>
```

#### 2. Upload and run the build script

From your local machine:

```bash
scp scripts/build-talos-fscrypt-image.sh root@<PUBLIC_IP>:/root/
```

On the VM:

```bash
chmod +x /root/build-talos-fscrypt-image.sh
GITHUB_TOKEN=<YOUR_GHCR_TOKEN> /root/build-talos-fscrypt-image.sh v1.12.3
```

The script will install Docker, log in to GHCR (using `GITHUB_TOKEN` and
`GITHUB_ACTOR`, which defaults to `pthuriot-corti`), and run the full build.
The kernel build takes ~40-60 minutes.

To skip the kernel build on re-runs (e.g. to rebuild just the imager):

```bash
SKIP_KERNEL=true GITHUB_TOKEN=<YOUR_GHCR_TOKEN> \
  /root/build-talos-fscrypt-image.sh v1.12.3
```

#### 3. Terminate the build VM

```bash
scw instance server terminate zone=fr-par-1 \
  with-ip=true with-block=true <SERVER_ID>
```

### `scripts/build-talos-fscrypt-image.sh` — Step by Step

| Step | What                                    | Make target / Command                                                 |
| ---- | --------------------------------------- | --------------------------------------------------------------------- |
| 1    | Setup                                   | Installs Docker, crane, jq; logs into GHCR via `GITHUB_TOKEN`         |
| 2    | Start local registry                    | `docker run registry:2`                                               |
| 3    | Clone `siderolabs/talos` at tag         | Determines matching `pkgs` version                                    |
| 4    | Clone `siderolabs/pkgs` at matching ref | Supports tag or git-describe refs                                     |
| 5    | Patch `kernel/build/config-amd64`       | Enables `CONFIG_FS_ENCRYPTION=y`, `CRYPTO_CTS=y`, `CRYPTO_HKDF=y`     |
| 6    | `make kernel-olddefconfig`              | Resolves Kconfig dependencies                                         |
| 7    | `make kernel PUSH=true`                 | Builds kernel, pushes to local registry                               |
| 8    | Validate `hack/modules-amd64.txt`       | Removes modules that no longer exist (e.g. changed from `=m` to `=y`) |
| 9    | `make kernel initramfs`                 | Repackages custom kernel into Talos boot artifacts                    |
| 10   | `make imager PUSH=true`                 | **Imager container with custom kernel**                               |
| 11   | Push imager to GHCR                     | `ghcr.io/kommodity-io/kommodity-talos-imager-fscrypt:<version>`       |

Environment variables: `REGISTRY`, `OUTPUT_REGISTRY`, `PLATFORM`,
`SKIP_KERNEL`, `GITHUB_TOKEN`, `GITHUB_ACTOR`.

## Building Cloud Images

### `.github/workflows/talos-cloud-image.yml`

Builds platform-specific disk images from a Talos imager. Supports
multiple platforms via a build matrix (scaleway, azure).

1. Runs the **imager** container for each platform
2. Decompresses and converts output (e.g. raw to qcow2 for Scaleway)
3. Pushes as OCI artifact to GHCR via `oras`

**Key inputs:**

- `talos_version`: e.g. `v1.12.3` (defaults to `v1.12.3`)
- `platforms`: `all`, `scaleway`, or `azure`
- `custom_imager_image`: custom imager (for kernel) — **must be set for
  fscrypt builds**
- `extensions`: comma-separated extension image refs (`none` to skip,
  empty for defaults)

**Important:** The imager container bundles the kernel at
`/usr/install/amd64/vmlinuz`. The `baseInstaller` only affects extensions
and initramfs overlay, **not the kernel**. To get a custom kernel into the
output disk image, you must use a custom imager via `custom_imager_image`.
The workflow always uses the official `siderolabs/installer` as
`baseInstaller`.

Example workflow dispatch for fscrypt (Scaleway only):

```yaml
talos_version: v1.12.3
platforms: scaleway
custom_imager_image: ghcr.io/kommodity-io/kommodity-talos-imager-fscrypt:v1.12.3
```

## Deploying to Scaleway

### Creating a VM from an existing Scaleway image

```bash
# Find the image
scw instance image list zone=fr-par-2 \
  name=kommodity-talos-scaleway-v1.12.3-fscrypt-v6

# Create instance
scw instance server create \
  zone=fr-par-2 \
  type=DEV1-S \
  name=my-talos-node \
  image=<IMAGE_ID> \
  ip=new
```

### Importing a new qcow2 image to Scaleway

1. Download qcow2 from GHCR:

   ```bash
   oras pull ghcr.io/kommodity-io/kommodity-talos-scaleway:<version>
   ```

2. Upload to an S3 bucket in the target Scaleway zone. Scaleway uses
   S3-compatible storage — use `aws s3` with the Scaleway endpoint:

   ```bash
   # Get credentials from scw CLI config
   eval $(scw object config get type=rclone 2>/dev/null \
     | grep -E '(access_key_id|secret_access_key)' \
     | sed 's/access_key_id/AWS_ACCESS_KEY_ID/' \
     | sed 's/secret_access_key/AWS_SECRET_ACCESS_KEY/' \
     | sed 's/ = /=/' \
     | sed 's/^/export /')

   # Upload qcow2
   aws s3 cp kommodity-talos-scaleway-<version>.qcow2 \
     s3://talos-image-storage/ \
     --endpoint-url https://s3.fr-par.scw.cloud \
     --region fr-par
   ```

3. Create snapshot by importing the qcow2 from S3:

   ```bash
   scw instance snapshot create \
     zone=fr-par-2 \
     name=kommodity-talos-scaleway-<version> \
     volume-type=l_ssd \
     bucket=talos-image-storage \
     key=kommodity-talos-scaleway-<version>.qcow2 \
     --wait
   ```

4. Create image from snapshot:

   ```bash
   scw instance image create \
     zone=fr-par-2 \
     name=kommodity-talos-scaleway-<version> \
     arch=x86_64 \
     snapshot-id=<snapshot-id>
   ```

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
| Scaleway image built with **official** imager    | `# CONFIG_FS_ENCRYPTION is not set` |
| Scaleway image built with **custom** imager      | `CONFIG_FS_ENCRYPTION=y`            |

## Bug Found (2026-04-27)

The Scaleway image `kommodity-talos-scaleway-v1.12.3-fscrypt-v6` was built
using the official `siderolabs/imager` instead of the custom
`kommodity-talos-imager-fscrypt`. The workflow's `custom_installer_image`
input only affects extensions/initramfs — the **kernel comes from the imager
container**. This resulted in a Scaleway image without
`CONFIG_FS_ENCRYPTION=y` despite the name containing "fscrypt".

**Fix:** Added `custom_imager_image` input to the workflow and a new Step 10
in the build script to push the custom imager to GHCR.

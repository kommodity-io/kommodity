# Talos Custom Kernel: Scaleway Image Pipeline

This document describes how we build, verify, and deploy Talos images with a
custom fscrypt-enabled kernel to Scaleway. It covers the full pipeline from
kernel compilation to VM creation.

## Background

Talos disk encryption with fscrypt requires `CONFIG_FS_ENCRYPTION=y` in the
kernel. The official Talos kernel does not enable this option.
We maintain a custom kernel build that adds fscrypt support, producing both an
**installer** (for node install/upgrade) and an **imager** (for generating
platform-specific disk images like Scaleway qcow2).

## Architecture

```text
siderolabs/pkgs (kernel source)
  │  patch config-amd64: CONFIG_FS_ENCRYPTION=y
  ▼
Custom kernel image ──► local registry (127.0.0.1:5005)
  │
  ▼
siderolabs/talos (build system)
  ├── make kernel initramfs    ──► vmlinuz + initramfs.xz
  ├── make imager PUSH=true    ──► kommodity-talos-imager-fscrypt (GHCR)
  └── make installer PUSH=true ──► kommodity-talos-installer-fscrypt (GHCR)
                                        │
                                        ▼
                            GH Workflow: talos-scaleway-image.yml
                                        │  uses custom imager
                                        ▼
                            Scaleway qcow2 disk image (GHCR + Scaleway)
```

## Building the Custom Kernel and Installer

### Prerequisites

The build requires significant CPU/RAM (~32 vCPU, 128 GB recommended) and
Docker. Run it on a remote VM, not locally.

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

Wait a few minutes for cloud-init to finish (it may reboot once), then SSH in:

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

# Log in to GHCR for pushing images
echo "<GHCR_TOKEN>" | docker login ghcr.io -u <GHCR_USER> --password-stdin

./build-talos-fscrypt-image.sh v1.12.3
```

The kernel build takes ~40-60 minutes.

To skip the kernel build on re-runs (e.g. to rebuild just the installer):

```bash
SKIP_KERNEL=true ./build-talos-fscrypt-image.sh v1.12.3
```

#### 3. Terminate the build VM

```bash
scw instance server terminate zone=fr-par-1 \
  with-ip=true with-block=true <SERVER_ID>
```

### `scripts/build-talos-fscrypt-image.sh` — Step by Step

| Step | What                                    | Make target / Command                                                 |
| ---- | --------------------------------------- | --------------------------------------------------------------------- |
| 0    | Start local registry                    | `docker run registry:2`                                               |
| 0b   | Clone `siderolabs/talos` at tag         | Determines matching `pkgs` version                                    |
| 1    | Clone `siderolabs/pkgs` at matching ref | Supports tag or git-describe refs                                     |
| 2    | Patch `kernel/build/config-amd64`       | Enables `CONFIG_FS_ENCRYPTION=y`, `CRYPTO_CTS=y`, `CRYPTO_HKDF=y`     |
| 3    | `make kernel-olddefconfig`              | Resolves Kconfig dependencies                                         |
| 4    | `make kernel PUSH=true`                 | Builds kernel, pushes to local registry                               |
| 5    | Validate `hack/modules-amd64.txt`       | Removes modules that no longer exist (e.g. changed from `=m` to `=y`) |
| 6    | `make kernel initramfs`                 | Repackages custom kernel into Talos boot artifacts                    |
| 7a   | `make installer-base PUSH=true`         | Base filesystem for installer                                         |
| 7b   | `make imager PUSH=true`                 | **Imager container with custom kernel**                               |
| 8    | `make installer`                        | Installer image via custom imager                                     |
| 9    | Push installer to GHCR                  | `ghcr.io/kommodity-io/kommodity-talos-installer-fscrypt:<version>`    |
| 10   | Push imager to GHCR                     | `ghcr.io/kommodity-io/kommodity-talos-imager-fscrypt:<version>`       |

Environment variables: `REGISTRY`, `OUTPUT_REGISTRY`, `PLATFORM`,
`SKIP_KERNEL`.

## Building the Scaleway qcow2 Image

### `.github/workflows/talos-scaleway-image.yml`

Converts a Talos imager output into a Scaleway-compatible qcow2 image:

1. Generates a Talos `profile.yaml` (platform: scaleway, optional extensions)
2. Runs the **imager** container to produce a raw disk image
3. Converts raw to qcow2 via `qemu-img`
4. Pushes qcow2 as OCI artifact to GHCR via `oras`

**Key inputs:**

- `talos_version`: e.g. `v1.12.3`
- `custom_installer_image`: custom installer (for initramfs/extensions)
- `custom_imager_image`: custom imager (for kernel) — **must be set for
  fscrypt builds**
- `extensions`: comma-separated extension image refs

**Important:** The imager container bundles the kernel at
`/usr/install/amd64/vmlinuz`. The `custom_installer_image` in the profile
only affects extensions and initramfs overlay, **not the kernel**. To get a
custom kernel into the output disk image, you must use a custom imager via
`custom_imager_image`.

Example workflow dispatch for fscrypt:

```yaml
talos_version: v1.12.3
custom_installer_image: ghcr.io/kommodity-io/kommodity-talos-installer-fscrypt:v1.12.3
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

3. Create snapshot from the uploaded file:

   ```bash
   scw block snapshot create \
     name=kommodity-talos-scaleway-<version> \
     volume-type=b_ssd \
     bucket=talos-image-storage \
     key=kommodity-talos-scaleway-<version>.qcow2
   ```

4. Create image from snapshot:

   ```bash
   scw instance image create \
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

### From an installer image (without a running node)

The installer image
(`ghcr.io/kommodity-io/kommodity-talos-installer-fscrypt:<version>`) contains
a UKI (Unified Kernel Image) at `/usr/install/amd64/vmlinuz.efi`. The kernel
config is embedded inside.

Extraction process:

1. **Extract the UKI from the container:**

   ```bash
   CID=$(docker create <installer-image>)
   docker export $CID | tar -xf - -C /tmp/installer usr/install/amd64/
   docker rm $CID
   chmod +r /tmp/installer/usr/install/amd64/vmlinuz.efi
   ```

2. **Extract the `.linux` section from the PE/UKI:**
   The UKI is a PE executable with sections. The `.linux` section
   contains the bzImage.

   ```bash
   objdump --headers /tmp/installer/usr/install/amd64/vmlinuz.efi
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

| Image                                                  | `CONFIG_FS_ENCRYPTION`              |
| ------------------------------------------------------ | ----------------------------------- |
| Official Talos installer (`siderolabs/installer`)      | `# CONFIG_FS_ENCRYPTION is not set` |
| Custom installer (`kommodity-talos-installer-fscrypt`) | `CONFIG_FS_ENCRYPTION=y`            |
| Custom imager (`kommodity-talos-imager-fscrypt`)       | `CONFIG_FS_ENCRYPTION=y`            |
| Scaleway image built with **official** imager          | `# CONFIG_FS_ENCRYPTION is not set` |
| Scaleway image built with **custom** imager            | `CONFIG_FS_ENCRYPTION=y`            |

## Bug Found (2026-04-27)

The Scaleway image `kommodity-talos-scaleway-v1.12.3-fscrypt-v6` was built
using the official `siderolabs/imager` instead of the custom
`kommodity-talos-imager-fscrypt`. The workflow's `custom_installer_image`
input only affects extensions/initramfs — the **kernel comes from the imager
container**. This resulted in a Scaleway image without
`CONFIG_FS_ENCRYPTION=y` despite the name containing "fscrypt".

**Fix:** Added `custom_imager_image` input to the workflow and a new Step 10
in the build script to push the custom imager to GHCR.

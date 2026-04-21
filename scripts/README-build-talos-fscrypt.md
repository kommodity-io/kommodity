# Building a Talos Scaleway Image with fscrypt (CONFIG_FS_ENCRYPTION=y)

The build script `build-talos-fscrypt-image.sh` compiles a custom Talos Linux kernel with filesystem encryption enabled and produces a Scaleway boot image. It requires significant CPU/RAM (~32 vCPU, 128 GB recommended) and Docker, so it should be run on a remote VM.

## 1. Create a Scaleway VM

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

## 2. Install Docker

```bash
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
```

## 3. Upload and run the build script

From your local machine:

```bash
scp scripts/build-talos-fscrypt-image.sh root@<PUBLIC_IP>:/root/
```

On the VM:

```bash
chmod +x /root/build-talos-fscrypt-image.sh
./build-talos-fscrypt-image.sh v1.12.3
```

The kernel build takes ~40-60 minutes. The script will:
1. Clone `siderolabs/pkgs` at the matching minor version (e.g. v1.12.0 for v1.12.3)
2. Patch the kernel config to enable `CONFIG_FS_ENCRYPTION=y`
3. Build the kernel using BuildKit
4. Build a custom installer image and push it to `ghcr.io/kommodity-io`
5. Run the Talos imager to produce `scaleway-amd64.raw.zst`

If you need to push the installer image, log in to ghcr.io first:

```bash
echo "<GHCR_TOKEN>" | docker login ghcr.io -u <GHCR_USER> --password-stdin
```

To skip the kernel build on re-runs (e.g. to rebuild just the installer image):

```bash
SKIP_KERNEL_BUILD=true ./build-talos-fscrypt-image.sh v1.12.3
```

## 4. Pull the output locally

From your local machine:

```bash
scp root@<PUBLIC_IP>:/root/kommodity/_out/scaleway-amd64.raw.zst _out/
```

Or to pull just the compiled kernel:

```bash
scp root@<PUBLIC_IP>:/root/kommodity/_out/fscrypt-build/vmlinuz/vmlinuz _out/
```

## 5. Terminate the VM

```bash
scw instance server terminate zone=fr-par-1 with-ip=true with-block=true <SERVER_ID>
```

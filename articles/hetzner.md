# Hetzner Cloud

Kommodity runs Talos workload clusters on Hetzner Cloud through the
[Syself cluster-api-provider-hetzner](https://github.com/syself/cluster-api-provider-hetzner)
(CAPH). Cloud servers only: Hetzner Robot bare metal is not wired up.

## What you need

- A Hetzner Cloud **project**. The API token and the rate limit are both
  project-scoped, so use one project per set of related clusters.
- A **read/write API token** for it (Console → Security → API tokens).
- A **Talos snapshot** in the project (below).

## Talos image

Hetzner has no custom-image upload, so Talos ships as a snapshot. Build the
image with the [`talos-cloud-image` workflow](../.github/workflows/talos-cloud-image.yml)
(platform `hcloud`), then upload it with
[hcloud-upload-image](https://github.com/apricote/hcloud-upload-image):

```bash
hcloud-upload-image upload \
  --image-path kommodity-talos-hcloud-v1.13.0.raw \
  --architecture x86 \
  --description kommodity-talos-hcloud-v1.13.0 \
  --labels caph-image-name=kommodity-talos-hcloud-v1.13.0
```

The `caph-image-name` label is mandatory: snapshots have no name, so CAPH
resolves `imageName` through that label. ARM (CAX) server types need a
separate `--architecture arm` snapshot with its own image name.

Plain factory images work too:

```bash
--image-url https://factory.talos.dev/image/<schematic>/v1.13.0/hcloud-amd64.raw.xz --compression xz
```

## Deploying a cluster

Create the token Secret (CAPH reads key `hcloud`):

```bash
kubectl --kubeconfig kommodity.yaml create secret generic hetzner \
  --from-literal=hcloud=$HCLOUD_TOKEN
```

Then deploy:

```bash
helm template my-cluster charts/kommodity-cluster \
  -f charts/kommodity-cluster/values.hetzner.yaml \
  | kubectl --kubeconfig kommodity.yaml apply -f -
```

[`values.hetzner.yaml`](../charts/kommodity-cluster/values.hetzner.yaml) has
the full option set: `region` (fsn1, nbg1, hel1, ash, hil, sin), `sku`,
replicas, load balancer type, image name. Root disk size is fixed by the
server type; there is no disk knob.

The chart creates a `HetznerCluster` with an hcloud load balancer (`lb11` by
default) fronting the control plane, one `HCloudMachineTemplate` per pool, and
delivers the hcloud CCM and CSI driver as addons.

## Networking

Set by `kommodity.network.ipv4.public`:

- **`true` (default)**: every node gets a public IPv4/IPv6, no private
  network.
- **`false`**: nodes are private-only on a Hetzner network (`nodeCIDR`
  required). Two caveats: Hetzner private networks have **no managed NAT**, so
  nodes cannot pull images without your own egress; and the
  `kommodity.io/node-cidr` annotation routes Talos API traffic through the
  `talos-cluster-proxy` addon, which must stay enabled.

### Egress for private clusters

`public: false` sets `enableIPv4: false` **and** `enableIPv6: false` on every
machine template, so nodes have no route off the private network at all. You
need two things, neither of which the chart can create for you:

1. A NAT server on the same network with forwarding and masquerading. Ubuntu
   plus cloud-init is enough:

   ```yaml
   #cloud-config
   runcmd:
     - sysctl -w net.ipv4.ip_forward=1
     - echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-nat.conf
     - iptables -t nat -A POSTROUTING -s 10.0.0.0/16 -o eth0 -j MASQUERADE
     - DEBIAN_FRONTEND=noninteractive apt-get install -y iptables-persistent
     - netfilter-persistent save
   ```

2. A `0.0.0.0/0` network route on the Hetzner network, pointing at that server's
   **private** IP:

   ```bash
   hcloud network add-route <network> --destination 0.0.0.0/0 --gateway <nat-private-ip>
   ```

Hetzner's DHCP does not announce the network route, so the chart configures the
node-side default route whenever `public: false`, with the gateway at the first
usable address of `nodeCIDR`.

**Ordering.** CAPH creates the network and cannot adopt an existing one, so the
network does not exist before the cluster. Nodes begin pulling images as soon as
they boot, so have a NAT server ready to attach the moment the network appears;
nodes that boot without egress need a reboot once NAT is in place. CAPH reserves
the second usable address of `nodeCIDR` for the control-plane load balancer, so
do not assign it to the NAT server.

**Private clusters require the workflow-built snapshot.** The chart always
delivers an `ExtensionServiceConfig` for `kommodity-autobootstrap`, which is
baked into snapshots from the `talos-cloud-image` workflow and absent from
factory images. With `public: false` there is no public-IP fallback path for
bootstrap, so use a workflow-built snapshot.

## DNS

Nodes point at `1.1.1.1` and `8.8.8.8` instead of Hetzner's recursors
(`185.12.64.1`/`185.12.64.2`), which serve `NXDOMAIN` for newly created public
records well past the record's TTL. That breaks in-cluster self-checks of a
public name, cert-manager's HTTP-01 solver most visibly: the challenge answers
correctly from the internet, but the solver's own lookup fails and the
certificate never issues.

The chart sets this through Talos's `ResolverConfig`; Talos propagates the node
nameservers to CoreDNS, so pod DNS follows. Override or opt out with:

```yaml
kommodity:
  network:
    nameservers: []   # keep whatever Hetzner's DHCP hands out
```

## Rate limits

Hetzner allows **3600 requests/hour per project**, refilling one per second.
The CAPH reconcilers run behind a token-bucket work-queue limiter plus CAPH's
own 5-minute back-off, but the budget is shared with everything else in the
project, including cluster-autoscaler, whose default scan interval exhausts it
on its own. Higher limits can be requested from Hetzner support.

## Costs and teardown

Servers, load balancers, primary IPv4s, snapshots, and volumes all bill
separately. `make run-hetzner-integration-test` creates real infrastructure
and costs real money.

To tear down, delete the `Cluster` (or `helm uninstall` and wait). CAPH
finalizers remove servers, the control-plane load balancer, the network, and
placement groups.

Anything the **CCM or CSI** created inside the cluster is not CAPH's to delete,
so it survives teardown: `Service` type LoadBalancer keeps its load balancer, and
volumes whose PVCs still exist keep their volumes. Delete those workloads before
the cluster, or sweep afterwards:

```bash
hcloud load-balancer list
hcloud volume list
```

`hcloud-upload-image` builds the snapshot on a temporary server and does not
clean up if it fails partway, so an interrupted upload leaves a running server
and an SSH key behind. Check for leftovers named `hcloud-upload-image-*` after
a failed run.

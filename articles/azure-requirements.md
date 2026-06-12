# Azure cluster creation with Kommodity

This document is the complete end-to-end guide for deploying a Kubernetes cluster on Azure using
Kommodity. It covers every prerequisite and every step from a blank Azure subscription to a running
cluster. Cross-reference [`articles/000_baseline.md`](000_baseline.md) for the narrative overview
and [`charts/kommodity-cluster/values.azure.yaml`](../charts/kommodity-cluster/values.azure.yaml)
for the full annotated values reference.

> Status: written against chart v0.20.x and the embedded Azure ARM reconciler + single-secret
> credential materializer. Manual steps are documented honestly as such; planned automation is
> noted with a "Roadmap" callout.

## TL;DR

You need (in order):

1. An Azure **subscription** and a target **region**.
2. A **Talos OS Azure VM image** in that subscription (one-time per Talos version).
3. A **service principal** with Contributor on the subscription.
4. A running **Kommodity management plane** — a single binary serving a Kubernetes API backed by
   PostgreSQL via Kine (no separate cluster to stand up). CAPI, CAPZ, the Talos providers, and the
   embedded Azure ARM reconciler are all bundled into that one binary (no ASO sidecar).
5. An **`AzureClusterIdentity`** + its backing Secret, applied to **Kommodity's own API server**.
   This is the **only** Azure Secret you create by hand; Kommodity's credential materializer derives
   the per-cluster `<release>-aso-secret` and the CCM cloud-config Secret from it.
6. A **`values.azure.yaml`** with your subscription, resource group, image, and identity name.
7. `helm install` (or `helm template | kubectl apply`).

---

## 1. Subscription and region

- Decide which Azure subscription will hold the workload cluster.
- Pick a region with sufficient quota for the VM SKUs you intend to use.
  Default in [`values.azure.yaml`](../charts/kommodity-cluster/values.azure.yaml) is `westeurope`
  with `Standard_D2s_v3`.
- Make sure the `Microsoft.Compute`, `Microsoft.Network`, and `Microsoft.Storage`
  resource providers are registered:

  ```bash
  az provider register --namespace Microsoft.Compute
  az provider register --namespace Microsoft.Network
  az provider register --namespace Microsoft.Storage
  ```

---

## 2. Talos OS Azure VM image

Talos does not publish to the Azure Marketplace, so you must bring your own image. Kommodity
publishes pre-built Talos VHDs as OCI artifacts on GHCR:

- Repository: `ghcr.io/kommodity-io/kommodity-talos-azure`
- Tags: one per Talos release, e.g. `v1.13.0`, plus `latest`.
- Layer: a single fixed-size `.vhd` (~11.7 GiB).

### Turn the VHD into an Azure VM image (one-time per Talos version)

1. Pull the VHD with `oras`:

   ```bash
   oras pull ghcr.io/kommodity-io/kommodity-talos-azure:v1.13.0
   # produces _out/kommodity-talos-azure-v1.13.0.vhd
   ```

2. Create a resource group, storage account, and blob container:

   ```bash
   az group create --name kommodity-images-rg --location westeurope
   az storage account create \
     --name kommodityimages \
     --resource-group kommodity-images-rg \
     --sku Standard_LRS
   az storage container create \
     --account-name kommodityimages \
     --name vhds
   ```

3. Upload the VHD as a **page blob** (block blobs are rejected as VM image sources):

   ```bash
   azcopy copy _out/kommodity-talos-azure-v1.13.0.vhd \
     "https://kommodityimages.blob.core.windows.net/vhds/kommodity-talos-azure-v1.13.0.vhd" \
     --blob-type=PageBlob
   ```

   Alternatively use `az storage blob upload --type page`, but `azcopy` is faster on large VHDs
   because it uploads only the non-zero regions.

4. Create a managed image from the page blob (**Hyper-V generation V2** is required):

   ```bash
   az image create \
     --resource-group kommodity-images-rg \
     --name kommodity-talos-azure-v1.13.0 \
     --source "https://kommodityimages.blob.core.windows.net/vhds/kommodity-talos-azure-v1.13.0.vhd" \
     --os-type Linux \
     --hyper-v-generation V2
   ```

5. Note the resulting resource ID and use it in chart values (see §7):

   ```text
   /subscriptions/<sub-id>/resourceGroups/kommodity-images-rg/providers/Microsoft.Compute/images/kommodity-talos-azure-v1.13.0
   ```

### Alternative: Azure Compute Gallery

You can publish the image into an Azure Compute Gallery and reference it via
`talos.computeGallery.{gallery,name,version}` instead of `talos.id`. See
[`machinetemplate.yaml`](../charts/kommodity-cluster/templates/provider/azure/machinetemplate.yaml)
for all supported image forms (`id`, `computeGallery`, `marketplace`, `sharedGallery`).

> **Roadmap**: a `talos-azure-image.yml` GitHub workflow that builds and publishes a managed image
> per Talos release, so consumers won't have to do this by hand.

---

## 3. Service principal for CAPZ

CAPZ needs Azure credentials to create infrastructure. Simplest path for an MVP is a service
principal with **Contributor** on the target subscription (tighten to a custom role for production):

```bash
az ad sp create-for-rbac \
  --name kommodity-capz \
  --role Contributor \
  --scopes "/subscriptions/<subscription-id>"
```

This prints `appId`, `password`, and `tenant`. Keep all three — you need them throughout the rest
of this guide.

---

## 4. The Kommodity management plane

Kommodity **is** the management plane — you do not stand up a separate Kubernetes cluster to run it.
Kommodity is a single binary that serves a Kubernetes API and persists all of its state in
**PostgreSQL via Kine** (no etcd, no kubelet, no worker nodes). Run it anywhere it can reach a
PostgreSQL database: a container, a VM, an Azure Container App, or your laptop. Every Kubernetes
resource in this guide (`AzureClusterIdentity`, `Cluster`, `AzureCluster`, Secrets, …) is applied to
**Kommodity's own API server** and stored in that database — not to some other cluster.

Everything the Azure workflow needs runs inside that one process:

- **CAPI core controllers** — bundled into the Kommodity binary.
- **CAPZ infrastructure provider** — bundled into the Kommodity binary.
- **Talos bootstrap + control plane providers** — bundled into the Kommodity binary.
- **Embedded Azure ARM reconciler** — materializes the ASO custom resources CAPZ delegates
  (ResourceGroup, VNet, Subnet, NSG, RouteTable, NatGateway) directly to Azure, in-process. No
  separate ASO sidecar is required (see §4.2).

The only external requirements are a reachable **PostgreSQL** database (via Kine) and **outbound
network access** from wherever Kommodity runs to `management.azure.com` and
`login.microsoftonline.com`.

### 4.1 AzureClusterIdentity

CAPZ authenticates to Azure via an `AzureClusterIdentity` resource. You create it — and its backing
Secret — directly in **Kommodity's API server** (the `kubectl --kubeconfig kommodity.yaml` below
points at Kommodity itself; the objects land in PostgreSQL via Kine). There is no separate
management cluster to target.

Its backing Secret **must use `.data` (base64-encoded values), not `.stringData`**: Kommodity's API
server does not perform the standard `stringData`→`data` merge during admission, so `.stringData`
keys are silently discarded, leaving `.data.clientSecret` empty and CAPZ unable to authenticate.

```bash
# Create the identity secret with base64-encoded value
kubectl --kubeconfig kommodity.yaml create secret generic azure-cluster-identity-secret \
  --namespace default \
  --from-literal=clientSecret='<sp-password>'
# kubectl create secret encodes values to base64 automatically
```

Then create the `AzureClusterIdentity`:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: AzureClusterIdentity
metadata:
  name: azure-cluster-identity
  namespace: default
spec:
  type: ServicePrincipal
  tenantID: <tenant-id>
  clientID: <sp-app-id>
  clientSecret:
    name: azure-cluster-identity-secret
    namespace: default
  allowedNamespaces: {}
```

```bash
kubectl --kubeconfig kommodity.yaml apply -f azure-cluster-identity.yaml
```

The name `azure-cluster-identity` is what you reference under `kommodity.provider.identity.name`
in chart values.

### 4.2 Embedded Azure ARM reconciler

CAPZ ≥ v1.10 delegates the network-foundation resources (ResourceGroup, VirtualNetwork, Subnet,
NetworkSecurityGroup, RouteTable, NatGateway) to
[Azure Service Operator (ASO)](https://azure.github.io/azure-service-operator/) custom resources;
it provisions compute, disks, NICs, load balancers, and public IPs directly via the Azure SDK.
Kommodity does **not** run an external ASO sidecar. Instead it embeds a purpose-built ARM
reconciler ([`pkg/controller/reconciler/azurearm/`](../pkg/controller/reconciler/azurearm/)) that
watches those ASO custom resources and reconciles them straight to Azure Resource Manager in the
same process as the rest of Kommodity.

There is nothing to deploy or operate separately — the reconciler starts with the Kommodity binary
whenever the Azure provider is enabled. It authenticates per resource via the
`serviceoperator.azure.com/credential-from` annotation CAPZ stamps on each CR, resolving the
referenced `<release>-aso-secret` (which the credential materializer creates for you, see §5.1).

**Why in-process rather than the ASO sidecar?** ASO's controller wiring lives in its `internal/`
packages (not importable) and its entrypoint expects to own the process, so it cannot be linked
into Kommodity's single binary. Running it as a sidecar would add a second deployable, a second
credential surface, and a CRD-version coupling to manage. The embedded reconciler keeps Azure
support to one binary, one credential path, and the same teardown story as every other provider.
See the package doc comment in
[`pkg/controller/reconciler/azurearm/setup.go`](../pkg/controller/reconciler/azurearm/setup.go) for
the full rationale and the CAPZ/ASO responsibility split.

The embedded Azure CRD set is generated by `make fetch-providers` and tracks CAPZ's pinned provider
release; see [`pkg/provider/crds/azure/`](../pkg/provider/crds/azure/).

---

## 5. Per-cluster setup

### 5.1 Credentials — one secret only

The **only** Azure secret you create per environment is the `AzureClusterIdentity`'s
`clientSecret` Secret (§4.1). From it, Kommodity's in-process **Azure credential materializer**
(`pkg/controller/reconciler/azure_credentials_materializer.go`) derives the other two automatically
for every Azure cluster, keyed off the cluster's `AzureClusterIdentity` and `AzureCluster` spec:

- **`<release-name>-aso-secret`** (`AZURE_SUBSCRIPTION_ID/TENANT_ID/CLIENT_ID/CLIENT_SECRET`) — what
  CAPZ's `serviceoperator.azure.com/credential-from` annotation and the embedded ARM reconciler read.
- the **CCM cloud-config Secret** (named by `provider.secret.name`) — generated from the cluster's
  subscription/RG/location/VNet plus the chart's resource naming, then delivered to the workload
  cluster by the CCM CRS reconciler.

Both materialized Secrets are owned by the `AzureCluster` (garbage-collected on teardown) and are
re-derived if the service principal password rotates. You no longer create `<release>-aso-secret`
or the cloud-config Secret by hand.

> **Escape hatch:** if you pre-create either Secret yourself, the materializer detects it (by the
> absence of its `kommodity.io/azure-credential-materializer` label) and leaves it untouched. Only
> `ServicePrincipal`-type `AzureClusterIdentity` is materialized; other identity types still require
> you to supply the Secrets manually.

### 5.2 Quotas

The first cluster typically asks Azure for:

- **vCPU quota** in the chosen region for the chosen SKU family (e.g. `Dsv3`).
- **Public IPs**: one for the NAT gateway SNAT IP (both modes); one for the API server LB if
  `network.ipv4.public: true`.
- **Standard SSDs** or **Premium SSDs** depending on `os.disk.type`.

```bash
az vm list-usage --location westeurope --output table
```

---

## 6. Chart values

The full annotated reference is [`values.azure.yaml`](../charts/kommodity-cluster/values.azure.yaml).
The sections below describe the key decisions.

### 6.1 Required fields

```yaml
kommodity:
  provider:
    name: Azure
    identity:
      name: azure-cluster-identity        # AzureClusterIdentity created in §4.1
    config:
      subscriptionID: <subscription-id>
      resourceGroup: <workload-rg>        # CAPZ creates this RG automatically
      location: westeurope

talos:
  id: /subscriptions/<sub-id>/resourceGroups/<image-rg>/providers/Microsoft.Compute/images/kommodity-talos-azure-v1.13.0
```

### 6.2 Cloud Controller Manager

The Azure CCM must run inside the workload cluster to assign node provider IDs and manage
`type=LoadBalancer` Services. Enable it with a single flag:

```yaml
kommodity:
  provider:
    cloudControllerManager:
      enabled: true
```

You do **not** write a `cloudConfig` blob. The credential materializer (§5.1) generates the
cloud-config Secret from the `AzureClusterIdentity` + `AzureCluster` spec (subscription, resource
group, location, VNet, and the chart's resource naming), so no SP credentials live in your values
file. Every image and resource block is overridable — see
[`values.azure.yaml`](../charts/kommodity-cluster/values.azure.yaml) for the full annotated set
(controller-manager image, node-manager DaemonSet image, the kubectl initImage, and resource
requests/limits), each defaulting to the value shown there.

**Delivery:** the chart emits a `ClusterResourceSet` ([`ccm-crs.yaml`](../charts/kommodity-cluster/templates/ccm-crs.yaml))
that propagates the CCM manifests (a ConfigMap) and the credential (the `azure-cloud-provider`
Secret in the workload's `kube-system`) into the cluster once it registers. The CCM `Deployment`
carries an `initContainer` that waits for the Secret before starting, so there is no crash loop if
the Secret arrives slightly later than the pod.

The kubelet `--cloud-provider=external` flag is **auto-injected** by the chart whenever
`cloudControllerManager.enabled` is true (see the `mergedStrategicPatch` helper in
[`_helpers.tpl`](../charts/kommodity-cluster/templates/_helpers.tpl)). You do not add it by hand —
omitting it previously left nodes without a providerID, which wedged CAPI NodeRef linking.

> **Security note:** the materialized cloud-config Secret carries the SP credentials and is stored
> by Kommodity (in PostgreSQL via Kine) and delivered to the workload `kube-system`. The only
> credential you supply by hand is the `AzureClusterIdentity`'s `clientSecret` (§4.1) — keep that
> out of version control. Nothing sensitive belongs in your `values.azure.yaml` anymore.

### 6.3 Private cluster (default)

`network.ipv4.public: false` is the default. The chart emits `apiServerLB.type: Internal`:

- The control-plane LB frontend IP is `10.0.0.100` from the CP subnet (private, no PublicIP).
- Both the CP subnet and the node subnet share a single NAT gateway (`<release>-node-natgw-1`)
  for outbound-only internet egress (NTP, package mirrors, Azure APIs). The NAT gateway public IP
  accepts no inbound connections.
- No VM NIC has a public IP.

```yaml
kommodity:
  network:
    ipv4:
      enabled: true
      public: false
      nodeCIDR: 10.0.0.0/8   # required on Azure in BOTH modes (node VMs are always private)
```

**Accessing the cluster from outside the VNet:** The API server (`apiserver.<release>.capz.io:6443`)
resolves inside the VNet only. To run `kubectl`/`helm`/`talosctl` from a developer laptop you need
VNet connectivity — VPN, VNet peering, Azure Bastion SSH forwarding, or running the management
plane inside Azure. The cluster itself comes up healthy without it (auto-bootstrap + inline CCM
Secret), but the Kommodity TalosControlPlane reconciler will log `bootstrap failed, retrying` until
it can reach the Talos API. The Talos API (50000) is never exposed on the load balancer; Kommodity
reaches it over the private network via the talos-cluster-proxy tunnel (port-forwarded through the
API server LB on 6443 to the in-cluster proxy pod, which dials the node's private IP). This resolves
automatically once VNet connectivity to the API server LB exists.

Application workloads expose ingress via `type=LoadBalancer` Services (which CCM provisions as
public Azure LBs by default). To get an internal LB for a Service, add:

```yaml
metadata:
  annotations:
    service.beta.kubernetes.io/azure-load-balancer-internal: "true"
```

### 6.4 Public cluster

For non-compliant / development use, set `public: true`:

```yaml
kommodity:
  network:
    ipv4:
      enabled: true
      public: true
```

The chart emits `apiServerLB.type: Public`; CAPZ creates a public IP for the API server LB, so the
**Kubernetes API (6443) is internet-reachable**. Node VMs still have no public IPs — the `public`
flag controls the apiServerLB type only. The **Talos API (50000) is never exposed on the LB**;
Kommodity reaches it over the private network via the talos-cluster-proxy tunnel (which rides the
public API server LB on 6443). For BYO-VNet public clusters the chart's `allow-apiserver` NSG rule
permits 6443 from the internet (private clusters scope it to `VirtualNetwork`). `nodeCIDR` is
required in both modes on Azure so the proxy knows which node IPs to tunnel to.

### 6.5 BYO-VNet (public or private)

To land the cluster in an existing VNet, set:

```yaml
kommodity:
  network:
    ipv4:
      public: false   # or true — BYO-VNet works in both modes
      vnet:
        name: my-existing-vnet
        resourceGroup: my-vnet-resource-group
```

In BYO-VNet mode CAPZ skips creating NSGs and route tables for **all** clusters (public and
private — it marks them as "custom VNet mode" and expects the resources to pre-exist). The chart
handles this automatically: it emits `NetworkSecurityGroup`, `NetworkSecurityGroupsSecurityRule`,
and `RouteTable` ASO CRs so no manual pre-creation is required. The `allow-apiserver` rule's source
is `Internet` in public mode (the public LB needs internet access to 6443) and `VirtualNetwork` in
private mode. These CRs are created in the VNet's resource group and reconciled to Azure by
Kommodity's embedded ARM reconciler. `helm uninstall` deletes them, and the underlying Azure
resources are removed.

---

## 7. What the chart does automatically

The following are handled by the chart — you do **not** need to do these by hand:

| Concern | Mechanism |
|---|---|
| Anonymous auth on kube-apiserver | Talos configPatch applied for Azure (required for CAPZ's HTTPS /readyz LB health probe — see §8) |
| Internal LB for private clusters | `apiServerLB.type: Internal` in `AzureCluster` |
| NAT gateway on CP + node subnets | Both subnets reference the same `<release>-node-natgw-1` |
| NSG + route table for BYO-VNet | ASO `NetworkSecurityGroup` / `RouteTable` CRs (all BYO-VNet clusters; `allow-apiserver` source is `Internet` in public mode, `VirtualNetwork` in private) |
| Talos API access (port 50000) | Reached via the talos-cluster-proxy tunnel (never exposed on the LB); requires the `kommodity.io/node-cidr` annotation, set whenever node VMs are private (always on Azure) |
| CCM credential materialized | Credential materializer derives the cloud-config Secret from the `AzureClusterIdentity` (§5.1) — no `cloudConfig` written by hand |
| CCM delivery | `ClusterResourceSet` propagates the CCM manifests + the `azure-cloud-provider` Secret into the workload `kube-system` (CCM `initContainer` waits for the Secret) |
| kubelet `--cloud-provider=external` | Auto-injected by the chart whenever `cloudControllerManager.enabled` is true |
| VM extension suppression | `disableExtensionOperations: true` on all AzureMachineTemplates (Talos has no Azure Linux agent) |
| Standalone VMs (no VMSS) | Nodes are individual `Microsoft.Compute/virtualMachines` via `AzureMachineTemplate`; the chart never templates `MachinePool`/`AzureMachinePool` (see §12) |
| Auto-bootstrap | Talos auto-bootstrap extension self-initializes the CP on private clusters without management→workload connectivity |

---

## 8. kube-apiserver anonymous auth (Azure LB health probe)

CAPZ v1.21.0 hardcodes an **HTTPS GET /readyz** health probe on the API server load balancer.
Talos defaults `--anonymous-auth=false` on kube-apiserver, so `/readyz` returns HTTP 401 to
anonymous probes, causing Azure to mark all backends unhealthy and silently drop API server traffic.

The chart automatically adds a Talos configPatch setting `--anonymous-auth=true` whenever
`kommodity.provider.name` is `Azure` (see
[`templates/talos/apiserver-anonymous-auth.yaml`](../charts/kommodity-cluster/templates/talos/apiserver-anonymous-auth.yaml)).
Kubernetes' built-in `system:public-info-viewer` ClusterRoleBinding restricts
`system:unauthenticated` access to `/livez`, `/readyz`, `/healthz`, and `/version` only — no
resource data is exposed.

---

## 9. Deploying a private cluster (end-to-end example)

Assuming §1–§5 are done and you have a file `values.azure.private.yaml` (gitignored):

```bash
# Deploy
helm template my-cluster oci://ghcr.io/kommodity-io/charts/kommodity-cluster \
  --version 0.20.3 \
  -f values.azure.private.yaml \
  | kubectl --kubeconfig kommodity.yaml apply -f -

# Watch the cluster come up (against the Kommodity API server)
kubectl --kubeconfig kommodity.yaml get cluster,azurecluster,taloscontrolplane,machine -w

# Once AzureCluster is Ready and nodes are Running, access from inside the VNet:
kubectl --kubeconfig workload-kubeconfig.yaml get nodes
```

The cluster self-bootstraps. The TalosControlPlane reconciler may show `bootstrap failed, retrying`
until VNet connectivity is available, but the nodes come up healthy regardless (Talos auto-bootstrap
via customData). Once you establish VNet access (VPN, sshuttle, Azure Bastion, or by running
Kommodity itself inside the VNet — e.g. on an Azure VM or Container App), all controller conditions
clear.

### Retrieve the workload kubeconfig

```bash
kubectl --kubeconfig kommodity.yaml get secret my-cluster-kubeconfig \
  -o jsonpath='{.data.value}' | base64 -d > workload-kubeconfig.yaml
```

---

## 10. Talos API access (port 50000) via the proxy tunnel

The Talos API is **never exposed on the Azure load balancer** — matching the Scaleway model, only
the Kubernetes API (6443) is reachable through the LB, and node VMs have no public IPs in any mode.
The chart therefore emits **no** `additionalAPIServerLBPorts` entry.

Instead, Kommodity reaches the Talos API over the private network via the **talos-cluster-proxy**:

1. The chart sets the `kommodity.io/node-cidr` annotation on the `Cluster` whenever node VMs are
   private (always on Azure — see §6.4). Kommodity's in-process proxy registers this CIDR.
2. When the TalosControlPlane reconciler dials a node IP that falls within a registered CIDR, the
   proxy routes the connection through a tunnel instead of direct-dialing: it port-forwards through
   the **K8s API LB (6443)** to the in-cluster `talos-cluster-proxy` pod, which dials the node's
   private `IP:50000`.

Putting the Talos management API on a public LB would expose it to the internet — not how Scaleway
does it and a needless widening of the audited surface.

**Bootstrapping**: the Talos auto-bootstrap extension self-initializes the control plane (etcd peers
over the private network intra-VNet), so no `talosctl bootstrap` call through the management plane is
needed. The proxy tunnel becomes usable once the workload K8s API is up and its LB health probe
passes — which requires the anonymous-auth patch (§8) so `/readyz` returns 200. The
TalosControlPlane reconciler logs `bootstrap failed, retrying` until then, but the nodes come up
healthy regardless.

---

## 11. Teardown

```bash
# Delete the Helm release (removes CAPI/CAPZ/ASO CRs; CAPI deletes Azure resources via CAPZ)
helm uninstall my-cluster --kubeconfig kommodity.yaml

# Delete the workload resource group (CAPI usually does this; belt-and-suspenders)
az group delete --name <workload-rg> --yes --no-wait
```

You do **not** delete the per-cluster credential Secrets by hand. The materialized
`<release>-aso-secret` and cloud-config Secret carry an `ownerReference` to the `AzureCluster`, so
they are garbage-collected automatically when the cluster is torn down. (The only Secret you ever
created — the `AzureClusterIdentity`'s `clientSecret` — is shared across clusters and is yours to
keep or remove.)

The `AzureCluster` CR carries `helm.sh/resource-policy: keep`, so Helm does not delete it directly.
CAPI's deletion flow drives the sequence: `Cluster` → `AzureCluster` → Azure resources. The ASO
NSG/RouteTable CRs (BYO-VNet only) do not carry this annotation and are deleted by Helm directly,
after which Kommodity's embedded ARM reconciler deletes the corresponding Azure resources.

---

## 12. Node compute: standalone VMs, not scale sets

Cluster nodes (control plane and workers) are provisioned as **individual
`Microsoft.Compute/virtualMachines`** — **not** as a Virtual Machine Scale Set (VMSS). This is by
design and is a property of which Cluster API types the chart templates, not a tunable.

**How the layers split.** Two distinct controllers provision Azure resources, and the VM-vs-VMSS
choice belongs entirely to the compute layer — the embedded ARM reconciler has nothing to do with it:

| Layer | Owns | Mechanism |
|---|---|---|
| **Network** | ResourceGroup, VirtualNetwork, Subnet, NetworkSecurityGroup, RouteTable, NatGateway | Kommodity's **embedded Azure ARM reconciler** materializes ASO CRs into Azure (replaces the ASO sidecar) |
| **Compute** | the node VMs themselves | **CAPZ's `AzureMachine` controller** calls the Azure SDK directly — node compute never goes through an ASO CR |

**Why it's standalone VMs.** CAPZ binds compute kind to the CAPI machine abstraction:

- `MachineDeployment` / `TalosControlPlane` + **`AzureMachineTemplate`** → standalone
  `Microsoft.Compute/virtualMachines`. **This is what the chart templates** (see
  [`templates/provider/azure/machinetemplate.yaml`](../charts/kommodity-cluster/templates/provider/azure/machinetemplate.yaml)
  and [`templates/provider/capi/machinedeployment.yaml`](../charts/kommodity-cluster/templates/provider/capi/machinedeployment.yaml)).
- `MachinePool` + `AzureMachinePool` → a VMSS. **The chart never templates these**, so no VMSS is
  ever created.

**How to verify on a running cluster.** Standalone-VM nodes have a providerID of the form:

```
azure:///subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/virtualMachines/<name>
```

A VMSS-backed node would instead read `.../Microsoft.Compute/virtualMachineScaleSets/<vmss>/virtualMachines/<n>`.

**Switching to VMSS would be opting in, not out:** it would require adding `MachinePool` +
`AzureMachinePool` templates (and the matching compute wiring). There is no VMSS in the stack today
to remove.

# Azure cluster creation with Kommodity

This document is the complete end-to-end guide for deploying a Kubernetes cluster on Azure using
Kommodity. It covers every prerequisite and every step from a blank Azure subscription to a running
cluster. Cross-reference [`articles/000_baseline.md`](000_baseline.md) for the narrative overview
and [`charts/kommodity-cluster/values.azure.yaml`](../charts/kommodity-cluster/values.azure.yaml)
for the full annotated values reference.

> Status: written against chart v0.12.24 and the completed Azure MVP. Manual steps are documented
> honestly as such; planned automation is noted with a "Roadmap" callout.

## TL;DR

You need (in order):

1. An Azure **subscription** and a target **region**.
2. A **Talos OS Azure VM image** in that subscription (one-time per Talos version).
3. A **service principal** with Contributor on the subscription.
4. The **Kommodity management cluster** running with an **ASO sidecar** alongside it.
5. An **`AzureClusterIdentity`** + its backing Secret in the management cluster.
6. A **per-release ASO credential Secret** named `<release>-aso-secret` (one per cluster).
7. A **`values.azure.yaml`** with your subscription, resource group, image, and CCM credentials.
8. `helm install` (or `helm template | kubectl apply`).

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

## 4. Management cluster setup

You need a Kubernetes cluster running Kommodity with:

- **CAPI core controllers** — provided by Kommodity.
- **CAPZ infrastructure provider** — bundled into the Kommodity binary.
- **Talos bootstrap + control plane providers** — bundled into the Kommodity binary.
- **ASO (Azure Service Operator) v2.18.0** running as a **separate sidecar process** (see §4.2).
- Outbound network access from the management cluster host to `management.azure.com` and
  `login.microsoftonline.com`.

### 4.1 AzureClusterIdentity

CAPZ authenticates to Azure via an `AzureClusterIdentity` resource. Its backing Secret **must use
`.data` (base64-encoded values), not `.stringData`**: Kommodity's embedded API server does not
perform the standard `stringData`→`data` merge during admission, so `.stringData` keys are silently
discarded, leaving `.data.clientSecret` empty and CAPZ unable to authenticate.

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

### 4.2 ASO sidecar

CAPZ ≥ v1.10 delegates Resource Group, VNet, Subnet, NAT Gateway, and related Azure resource
creation to [Azure Service Operator (ASO)](https://azure.github.io/azure-service-operator/) custom
resources. Kommodity does not embed ASO controllers today (the ASO `internal/` packages cannot be
imported externally, and its entrypoint expects to own the process). Run ASO **v2.18.0** as a
separate Docker container pointed at the same Kommodity API server:

```bash
docker run -d \
  --name aso-kommodity \
  --restart=unless-stopped \
  -v /path/to/kommodity.kubeconfig:/kubeconfig \
  -e KUBECONFIG=/kubeconfig \
  -e AZURE_SUBSCRIPTION_ID=<subscription-id> \
  -e AZURE_TENANT_ID=<tenant-id> \
  -e AZURE_CLIENT_ID=<sp-app-id> \
  -e AZURE_CLIENT_SECRET=<sp-password> \
  -e POD_NAMESPACE=azureserviceoperator-system \
  -e AZURE_OPERATOR_MODE=watchers \
  mcr.microsoft.com/k8s/azureserviceoperator:v2.18.0 \
  --crd-management=none --health-addr=:8081 --metrics-addr=0
```

Two flags are required:
- `--crd-management=none`: Kommodity ships its own copy of the Azure CRDs; ASO must not try to
  replace them.
- `AZURE_OPERATOR_MODE=watchers`: disables webhook servers (Kommodity has no cert-manager).

The kubeconfig must point at Kommodity through a routable address. In Docker Desktop on macOS that
is typically `https://host.docker.internal:8443` or `https://192.168.5.2:8443`. If Kommodity's
serving cert only covers `127.0.0.1`, pass `insecure-skip-tls-verify: true` in the kubeconfig
(acceptable for local dev; use a properly-SANed cert in production).

**ASO version compatibility**: use v2.18.0. Newer ASO versions introduce CRD storage versions that
Kommodity's embedded scheme does not register, causing conversion errors at runtime. See
[`pkg/provider/crds/azure/`](../pkg/provider/crds/azure/) for the embedded CRD set.

> **Note**: Kommodity's embedded CRDs are generated by `make fetch-providers` and track CAPZ's
> pinned provider release, so they cannot carry hand-added stub versions. If a given ASO sidecar
> version refuses to start because a CRD (e.g. `extensions.kubernetesconfiguration.azure.com`) is
> missing a served version it expects, patch that CRD on the management cluster as a setup step
> (`kubectl ... patch crd ...` adding the served version) rather than editing the embedded CRD —
> the latter would fail the CAPI provider-consistency check.

> **Roadmap**: embedded ASO. Two unblockers must land first: (1) ASO upstream exposes a public
> controller setup API (or Kommodity accepts a fork), and (2) CAPZ catches up to an ASO version
> that contains the storage versions Kommodity wants to ship.

---

## 5. Per-cluster setup

### 5.1 Per-release ASO credential Secret

Every cluster Helm release needs an ASO credential Secret named **`<release-name>-aso-secret`** in
the management cluster's `default` namespace. CAPZ annotates every ASO CR it creates with
`serviceoperator.azure.com/credential-from: <release>-aso-secret`, so this Secret must exist
before or at the time of `helm install`.

This is currently a **manual step** — create it for each release:

```bash
kubectl --kubeconfig kommodity.yaml create secret generic my-cluster-aso-secret \
  --namespace default \
  --from-literal=AZURE_SUBSCRIPTION_ID=<subscription-id> \
  --from-literal=AZURE_TENANT_ID=<tenant-id> \
  --from-literal=AZURE_CLIENT_ID=<sp-app-id> \
  --from-literal=AZURE_CLIENT_SECRET=<sp-password>
```

Replace `my-cluster` with your Helm release name. A follow-up is planned to template this Secret
into the chart so it is created automatically.

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
`type=LoadBalancer` Services. Set `cloudControllerManager.cloudConfig` to deliver the CCM
credential as a Talos bootstrap `inlineManifest`:

```yaml
kommodity:
  provider:
    cloudControllerManager:
      enabled: true
      cloudConfig: |
        {
          "tenantId": "<tenant-id>",
          "subscriptionId": "<subscription-id>",
          "resourceGroup": "<workload-rg>",
          "location": "westeurope",
          "useManagedIdentityExtension": false,
          "aadClientId": "<sp-app-id>",
          "aadClientSecret": "<sp-password>",
          "loadBalancerSku": "standard",
          "vmType": "standard",
          "useInstanceMetadata": true,
          "securityGroupName": "<release>-node-nsg",
          "securityGroupResourceGroup": "<workload-rg>",
          "vnetName": "<release>-vnet",
          "vnetResourceGroup": "<workload-rg>",
          "subnetName": "<release>-node-subnet",
          "routeTableName": "<release>-node-routetable"
        }
```

When `cloudConfig` is set, the chart embeds the `azure-cloud-provider` Secret and the CCM
`Deployment` (with an `initContainer` that waits for the Secret) into the Talos machine config via
`cluster.inlineManifests`. The Secret is applied at first boot — no management→workload connectivity
is required for the CCM to start.

Also add the kubelet flag that tells Kubernetes a cloud provider is in use:

```yaml
kommodity:
  global:
    configPatches:
      - op: add
        path: /machine/kubelet/extraArgs
        value:
          cloud-provider: external
```

> **Security note:** `cloudConfig` is embedded verbatim into the Talos machine config (stored
> encrypted on the Talos STATE partition and in the management cluster's TalosConfig secret). Store
> the values file containing `cloudConfig` in a gitignored file — never commit SP credentials.

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
      nodeCIDR: 10.200.0.0/16   # required when public: false
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
| CCM Secret delivery at bootstrap | `cloudConfig` → Talos `inlineManifest` → `azure-cloud-provider` Secret in workload `kube-system` |
| VM extension suppression | `disableExtensionOperations: true` on all AzureMachineTemplates (Talos has no Azure Linux agent) |
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
  --version 0.12.24 \
  -f values.azure.private.yaml \
  | kubectl --kubeconfig kommodity.yaml apply -f -

# Watch the cluster come up (from management cluster)
kubectl --kubeconfig kommodity.yaml get cluster,azurecluster,taloscontrolplane,machine -w

# Once AzureCluster is Ready and nodes are Running, access from inside the VNet:
kubectl --kubeconfig workload-kubeconfig.yaml get nodes
```

The cluster self-bootstraps. The TalosControlPlane reconciler may show `bootstrap failed, retrying`
until VNet connectivity is available, but the nodes come up healthy regardless (Talos auto-bootstrap
via customData). Once you establish VNet access (VPN, sshuttle, Azure Bastion, or a management VM
inside Azure), all controller conditions clear.

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

# Delete the per-release ASO secret
kubectl --kubeconfig kommodity.yaml delete secret my-cluster-aso-secret
```

The `AzureCluster` CR carries `helm.sh/resource-policy: keep`, so Helm does not delete it directly.
CAPI's deletion flow drives the sequence: `Cluster` → `AzureCluster` → Azure resources. The ASO
NSG/RouteTable CRs (BYO-VNet only) do not carry this annotation and are deleted by Helm directly,
after which ASO deletes the corresponding Azure resources.

# Zero-Touch Cluster Lifecycle: From YAML to Production

*Part 3 of 3 in the Kommodity series*

*The best infrastructure is invisible. Apply a manifest, wait for machines to coordinate, and your HA cluster is ready - no manual intervention, no bootstrap scripts, no "SSH into the first node."*

---

In [Part 1](./001_why_sovereign_cloud.md), we introduced Kommodity and the case for sovereign cloud. In [Part 2](./002_hardware_rooted_trust.md), we covered the security foundations: TPM attestation and network-based key management.

Now let's put it all together. This article covers the operational side: how clusters are provisioned, how control planes bootstrap themselves, and what running Kommodity in production actually looks like.

---

## Cluster Provisioning with Cluster API

With the security foundation in place, let's look at how clusters are actually provisioned.

### A Sovereign Deployment in Practice

Imagine you're deploying a healthcare application that must comply with EU data residency requirements. You need:
- A production cluster in Frankfurt (Azure) for German customers
- A production cluster in Paris (Scaleway) for French customers
- A disaster recovery cluster in a private datacenter (KubeVirt)

With Kommodity, this becomes three YAML files and a single operational model. Each cluster gets:
- TPM-attested machines that prove their integrity before receiving secrets
- Disk encryption with keys you control (not Azure, not Scaleway - **you**)
- The same `kubectl` and Helm workflows across all environments
- Unified audit logging for compliance reporting

No cloud-specific tooling. No vendor-specific key management. No "works differently in production than in non-production."

That's what we mean by making sovereign cloud boring.

### The Resources

Kommodity uses standard Cluster API resources:

```yaml
# cluster.yaml - the orchestrating resource
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: production-paris
  namespace: clusters
spec:
  clusterNetwork:
    pods:
      cidrBlocks: ["10.244.0.0/16"]
    services:
      cidrBlocks: ["10.96.0.0/12"]
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1beta1
    kind: TalosControlPlane
    name: production-paris-control-plane
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: ScalewayCluster
    name: production-paris
```

The `Cluster` resource references:
- **TalosControlPlane**: Manages control plane nodes using Talos Linux
- **ScalewayCluster** (or AzureCluster, KubeVirtCluster): Provider-specific infrastructure

Apply with `kubectl`:

```bash
kubectl --kubeconfig kommodity.yaml apply -f cluster.yaml
```

Kommodity's controllers reconcile the desired state:
1. Create cloud infrastructure (VPC, subnets, load balancers)
2. Provision machines with Talos Linux
3. Wait for attestation to pass
4. Deliver machine configuration via metadata service
5. Bootstrap Kubernetes control plane
6. Join worker nodes

The same workflow applies regardless of provider. The YAML changes, but the operational model stays consistent.

### Multi-Cloud Reality Check

I want to be honest about what "multi-cloud" means in practice.

Different providers use different API versions:
- Scaleway: `infrastructure.cluster.x-k8s.io/v1alpha1`
- Azure: `infrastructure.cluster.x-k8s.io/v1beta1`
- Docker: `infrastructure.cluster.x-k8s.io/v1beta1`

Each provider has different capabilities. Scaleway has public gateways. Azure has managed identities. KubeVirt runs on existing virtualization platforms.

You can't just swap `ScalewayCluster` for `AzureCluster` and expect identical behavior. What you *can* do is use the same operational patterns:
- Same tooling (`kubectl`, Helm, GitOps)
- Same observability approach
- Same RBAC model
- Same team skills

The value isn't "write once, run anywhere" - it's "learn once, operate anywhere."

---

## Auto-Bootstrap: Eliminating Manual Cluster Initialization

Traditional Kubernetes bootstrap requires designating a first control plane node to initialize the cluster. In automated environments, this creates a sequencing problem: which node goes first?

The [auto-bootstrap extension](https://github.com/kommodity-io/kommodity-autobootstrap-extension) handles this automatically:

1. **Control plane detection**: Extension only activates on nodes with etcd secrets configured
2. **Peer discovery**: Scans local network CIDR for other Talos nodes using the Talos API
3. **Deterministic leader election**: Each node independently calculates who should be leader using the same algorithm - the node with the earliest boot time wins, with lowest IP address as tiebreaker. Since all nodes use identical logic on the same peer data, they all agree on the leader without any coordination protocol
4. **Quorum wait**: Leader waits until the configured number of peers are discovered (e.g., 3 for HA)
5. **Bootstrap execution**: Leader initializes etcd and the Kubernetes control plane; other nodes detect the initialized cluster and join automatically

```yaml
machine:
  install:
    extensions:
      - image: ghcr.io/kommodity-io/kommodity-autobootstrap-extension:vX.Y.Z # Use the latest release tag from GitHub
  env:
    KOMMODITY_AUTOBOOTSTRAP_NETWORK_CIDR: "10.0.0.0/24"
    KOMMODITY_AUTOBOOTSTRAP_QUORUM: "3"
```

This enables fully declarative cluster creation: apply a manifest, and three nodes coordinate to form an HA control plane without intervention.

### Private Networks Only

Auto-bootstrap is designed exclusively for private networks. Peer discovery scans the configured CIDR without TLS verification, which is appropriate for isolated infrastructure networks but not for shared or untrusted environments. In production, you should enforce this at configuration time by validating that `KOMMODITY_AUTOBOOTSTRAP_NETWORK_CIDR` is restricted to private address space (for example, RFC1918 ranges), and treating any non-private CIDR (such as `0.0.0.0/0`) as a misconfiguration that requires an explicit, security-reviewed override. Never run the auto-bootstrap extension on nodes that are directly reachable from the public Internet. The bootstrap process itself remains protected by Talos's certificate-based authentication once peers are discovered.

---

## Authentication: OIDC and ServiceAccounts

Kommodity supports two authentication mechanisms:

**OIDC** for human users:
```bash
export KOMMODITY_OIDC_ISSUER_URL=https://accounts.google.com
export KOMMODITY_OIDC_CLIENT_ID=your-client-id
export KOMMODITY_OIDC_USERNAME_CLAIM=email
export KOMMODITY_OIDC_GROUPS_CLAIM=groups
export KOMMODITY_ADMIN_GROUP=platform-team@yourcompany.com
```

Users authenticate with their corporate identity provider. Groups from the IdP map to authorization decisions.

**ServiceAccount tokens** for automation:

Kommodity generates tokens using a dedicated RSA signing key, stored separately from TLS certificates. This allows independent key rotation and survives certificate renewals.

The authorization model is straightforward:
- `system:masters` group → full access (Kubernetes convention)
- Configured admin group → full access
- `system:serviceaccounts` → access for authenticated service accounts
- Everyone else → denied

---

## Cluster Addons: Batteries Included, Customization Welcome

Kommodity doesn't just provision empty clusters - it includes an addon system that bootstraps essential components automatically.

### Supported Addons

**[Cilium CNI](https://cilium.io/)** (enabled by default):
- eBPF-based networking with kube-proxy replacement
- Hubble observability (UI + relay)
- BGP control plane support
- Pre-configured for Talos Linux security contexts

**[ArgoCD](https://argo-cd.readthedocs.io/)** (optional):
- GitOps-ready cluster management
- Deployed via `KubectlApply` mode for easy GitOps adoption

### Bring Your Own Addons

The addon system is extensible - any Helm chart can be deployed as a cluster addon:

```yaml
kommodity:
  addons:
    cnpg:  # CloudNative PostgreSQL - replace managed databases
      enabled: true
      namespace: cnpg-system
      lifecycle:
        install:
          mode: HelmInstall
        upgrade:
          disable: true  # Let GitOps manage after initial install
      chart:
        repository: https://cloudnative-pg.github.io/charts
        name: cloudnative-pg
        version: 0.23.0
```

This enables you to replace cloud-managed services with sovereign alternatives:
- **[CNPG](https://cloudnative-pg.io/)** instead of RDS/Cloud SQL - your data, your database - under your control
- **[Strimzi](https://strimzi.io/)** instead of managed Kafka
- **[Rook/Ceph](https://rook.io/)** instead of managed object storage (when data must never leave your infrastructure)

Addons deploy as inline manifests in the TalosControlPlane, available immediately after bootstrap. GitOps tools like ArgoCD or Flux can then "adopt" them for ongoing management (`upgrade.disable: true` prevents Kommodity from overwriting GitOps changes).

---

## Audit Logging

Kommodity implements Kubernetes-native audit logging:

```yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  - level: RequestResponse
    resources:
      - group: "cluster.x-k8s.io"
        resources: ["clusters", "machines"]
  - level: Metadata
    resources:
      - group: ""
        resources: ["secrets"]
```

Every API request records: authenticated user, source IP, timestamp, request body (at configured level), and response. This satisfies audit requirements for most compliance frameworks.

---

## Operational Realities

Let's talk about what running Kommodity actually looks like.

### Single Point of Failure?

Kommodity is a single binary, which might sound like a single point of failure. In practice, it's less critical than you'd think:

- **Existing clusters are independent**: Once bootstrapped, clusters don't depend on Kommodity for normal operation. Your workloads keep running.
- **Reconciliation is periodic, not constant**: Cluster API controllers reconcile state, but they're not in a tight loop. An occasional Kommodity outage during normal operations doesn't harm anything.
- **Critical only during changes**: Kommodity availability matters when you're provisioning new clusters, scaling nodes, or performing upgrades. Outside of those operations, it can be down without impact.

For production, we recommend:
- **One Kommodity instance per environment type** (dev, staging, production) for segregation. This limits blast radius and prevents a misconfiguration in dev from affecting production clusters.
- **Multiple replicas per instance** behind a load balancer - not because downtime is catastrophic, but because it's good practice and simplifies maintenance windows.
- **HA PostgreSQL** per instance (managed PostgreSQL services or CNPG work well).

### Upgrade Strategy

Kommodity uses standard Kubernetes API versioning. CRDs are embedded in the binary and applied on startup. Upgrades follow this pattern:

1. Update the Kommodity deployment (rolling update)
2. New version applies updated CRDs
3. Controllers reconcile with new logic

There's no separate migration step, but you should:
- Test upgrades in lower environment(s) first
- Review CRD changes between versions
- Plan for controller behavior changes

### Network Dependencies

When security features are enabled, machines depend on Kommodity for:
- Attestation verification
- Metadata delivery (Talos configuration)
- KMS key retrieval

Network partitions between machines and Kommodity prevent new machines from booting when these features are active.

**However, these features are optional.** If your compliance requirements don't mandate hardware attestation or network-based disk encryption, you can run Kommodity clusters without them. In this mode, Kommodity functions purely as a Cluster API management plane - machines boot independently using standard Talos configuration methods, and Kommodity's availability only affects cluster lifecycle operations (scaling, upgrades), not running workloads.

Choose based on your needs:
- **Strict compliance** (healthcare, finance, government): Enable attestation + KMS
- **Standard deployments**: Kommodity as management plane only, reduced network dependency

### What Kommodity Doesn't Do

To set appropriate expectations:

- **Not zero-ops**: You're running infrastructure. There will be incidents. Kommodity reduces complexity but doesn't eliminate operational responsibility.
- **Not a full PaaS**: Kommodity provisions and manages Kubernetes clusters. What you run on those clusters is up to you.
- **Requires Kubernetes expertise**: This is a tool for platform teams, not application developers. You should be comfortable with `kubectl`, Helm, and Kubernetes concepts.
- **Not a silver bullet for multi-cloud**: It abstracts cluster lifecycle management, but you still need to understand provider-specific features and limitations.

---

## Getting Started: Production Deployment

### Kommodity Terraform Module

For Azure deployments, use the Terraform module to provision Kommodity itself:

```hcl
module "kommodity_azure_deployment" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_azure_deployment?ref=<tag>"

  oidc_configuration = {
    issuer_url  = "https://login.microsoftonline.com/<tenant-id>/v2.0"
    client_id   = "<client-id>"
    admin_group = "platform-team@yourcompany.com"
  }
}
```

This provisions a complete Azure environment: VNet, PostgreSQL Flexible Server, Container App, and Log Analytics. See [`terraform/modules/kommodity_azure_deployment`](https://github.com/kommodity-io/kommodity/tree/main/terraform/modules/kommodity_azure_deployment) for all options.

### Kommodity Cluster Helm Chart

Once Kommodity is running, deploy clusters using the Helm chart:

```bash
# Add your cloud provider credentials (example for Scaleway here):
kubectl --kubeconfig kommodity.yaml create secret generic scaleway-secret \
  --from-literal=SCW_ACCESS_KEY=<key> \
  --from-literal=SCW_SECRET_KEY=<secret> \
  --from-literal=SCW_DEFAULT_REGION=fr-par \
  --from-literal=SCW_DEFAULT_PROJECT_ID=<project-id>

# Deploy a cluster
helm template my-cluster oci://ghcr.io/kommodity-io/charts/kommodity-cluster \
  -f values.scaleway.yaml | kubectl --kubeconfig kommodity.yaml apply -f -
```

Example `values.scaleway.yaml`:

```yaml
kommodity:
  provider:
    name: Scaleway
    secret:
      name: scaleway-secret
  region: fr-par
  controlplane:
    replicas: 3
    sku: PLAY2-NANO
  nodepools:
    default:
      replicas: 2
      sku: PLAY2-NANO
```

See [`charts/kommodity-cluster`](https://github.com/kommodity-io/kommodity/tree/main/charts/kommodity-cluster) for provider-specific examples (Scaleway, Azure, KubeVirt, Docker).

---

## Conclusion: Sovereignty Should Be Boring

Over this three-part series, we've covered:

1. **Why sovereign cloud matters** - and why existing solutions fall short
2. **Security foundations** - TPM attestation and network-based key management
3. **Operational reality** - cluster provisioning, auto-bootstrap, and production deployment

The best infrastructure is the kind you don't think about. It works. It's secure. It's auditable. It doesn't wake you up at night.

Sovereign cloud has a reputation for being complex, expensive, and requiring specialized expertise. That's because most sovereign solutions are bolt-ons - compliance features added after the fact to platforms designed for different priorities.

Kommodity takes the opposite approach. Sovereignty is built in from the start:

- **Verifiable machines**: TPM attestation means you *know* your infrastructure is what you deployed
- **Controlled encryption**: Your keys, your control, instant revocation
- **Portable operations**: The same `kubectl` workflows whether you're on a hyperscaler, a regional cloud, or bare metal
- **Auditable by design**: Every API call logged, every change tracked

It's not magic. It's a thoughtful combination of existing, proven tools - Talos Linux, Cluster API, Kine - packaged in a way that makes sovereign cloud as routine as any other deployment.

**One binary. Kubernetes APIs. Verifiable machines. Encrypted disks.**

That's sovereign cloud, made boring. And boring is exactly what infrastructure should be.

---

*Kommodity is developed by the platform team at [Corti](https://corti.ai) and released under Apache 2.0. Contributions welcome at [github.com/kommodity-io/kommodity](https://github.com/kommodity-io/kommodity).*

---

## References

- [Talos Linux Documentation](https://docs.siderolabs.com/talos/)
- [Talos Linux Security Overview](https://www.siderolabs.com/blog/security-in-kubernetes-infrastructure/)
- [Talos Disk Encryption](https://docs.siderolabs.com/talos/v1.9/configure-your-talos-cluster/storage-and-disk-management/disk-encryption)
- [Cluster API Documentation](https://cluster-api.sigs.k8s.io/)
- [Cluster API Bootstrap Provider for Talos](https://github.com/siderolabs/cluster-api-bootstrap-provider-talos)
- [Cluster API Control Plane Provider for Talos](https://github.com/siderolabs/cluster-api-control-plane-provider-talos)
- [Kine - etcd shim for SQL databases](https://github.com/k3s-io/kine)
- [TPM 2.0 and Platform Configuration Registers](https://www.systutorials.com/understanding-tpm-2-0-and-platform-configuration-registers-pcrs/)
- [Kommodity GitHub Repository](https://github.com/kommodity-io/kommodity)
- [Kommodity Attestation Extension](https://github.com/kommodity-io/kommodity-attestation-extension)
- [Kommodity Auto-Bootstrap Extension](https://github.com/kommodity-io/kommodity-autobootstrap-extension)

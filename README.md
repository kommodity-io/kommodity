<img src="public/kommodity-logo.jpeg" alt="Kommodity Logo" style="border-radius: 15px; max-width: 150px; width: 15%; float: right; margin-top: 30px; margin-left: 30px; margin-bottom: 30px;"/>

# Kommodity

[![Go Report Card](https://img.shields.io/badge/go%20report-A+-brightgreen?style=flat-square)](https://goreportcard.com/report/github.com/kommodity-io/kommodity)
[![Go Reference](https://img.shields.io/badge/godoc-reference-blue?style=flat-square)](https://pkg.go.dev/github.com/kommodity-io/kommodity)
[![CI](https://img.shields.io/github/actions/workflow/status/kommodity-io/kommodity/release.yml?branch=main&label=ci&style=flat-square)](https://github.com/kommodity-io/kommodity/actions)
[![Release](https://img.shields.io/github/v/release/kommodity-io/kommodity?include_prereleases&label=release&style=flat-square)](https://github.com/kommodity-io/kommodity/releases)
[![License](https://img.shields.io/github/license/kommodity-io/kommodity?style=flat-square)](https://github.com/kommodity-io/kommodity/blob/main/LICENSE)

Kommodity is an open-source infrastructure platform to commoditize compute, storage, and networking.

> 🚧 EXPERIMENTAL 🚧: This project is in an early stage of development and is not yet ready for production use. APIs may break between minor releases, and the project is not yet feature-complete. The project does however adhere to [semantic versioning][semver], so patch releases will never break the API.

## Architecture

![Kommodity Architecture](images/kommodity-architecture.excalidraw.png)

## Development

Make sure to have a recent version of Go installed. We recommend using [gvm][gvm] to install Go.

```bash
gvm install go1.24.2 -B
gvm use go1.24.2 --default
```

As a build system, we use `make`.

```bash
# Create a binary in the `bin/` directory.
make build
# Run code generation and start the local development setup (through docker compose)
make setup
# Run the application locally.
make run
# Teardown the local development setup
make teardown
# Run integration tests
make integration-test
# Run helm unit tests (requires helm unittest plugin)
make run-helm-unit-tests
```

### ⚠️ Dependencies

If you want to run Kommodity with authentication using OpenID Connect (OIDC), you need to have `kubectl` `oidc-login` plugin installed. We recommend that you install it via [krew](https://krew.sigs.k8s.io/docs/user-guide/setup/install/).

```bash
kubectl krew install oidc-login
```

Kommodity uses Caddy as a reverse proxy to handle TLS termination and routing, it is bootstrapped as part of Docker compose. Make sure to have Caddy installed on your system. You can find installation instructions on the [Caddy website](https://caddyserver.com/docs/install).

Make sure to override the `KOMMODITY_BASE_URL` environment variable in the `.env` file to match your Caddy setup, e.g., `https://localhost:5443`.

Example of `kommodity.yaml` kubeconfig file with OIDC authentication:

```yaml
apiVersion: v1
kind: Config
clusters:
  - name: kommodity
    cluster:
      server: https://localhost:5443
      insecure-skip-tls-verify: true
users:
  - name: oidc
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1
        command: kubectl
        args:
          - oidc-login
          - get-token
          - --oidc-issuer-url=ISSUER_URL
          - --oidc-client-id=YOUR_CLIENT_ID
          - --oidc-extra-scope=email
        interactiveMode: Always
contexts:
  - name: kommodity-context
    context:
      cluster: kommodity
      user: oidc
current-context: kommodity-context
preferences: {}
```

## Demo

```bash
# Test the application via `kubectl`.
kubectl --kubeconfig kommodity.yaml api-versions
kubectl --kubeconfig kommodity.yaml api-resources
kubectl --kubeconfig kommodity.yaml create -f examples/namespace.yaml
kubectl --kubeconfig kommodity.yaml create -f examples/secret.yaml
# Test gRPC reflection.
grpcurl -plaintext localhost:8000 list
```

### Setup Kubectl for Kommodity Talos Cluster

```bash
kubectl --kubeconfig <kommodity kubeconfig file> get secrets <cluster name>-talosconfig -ojson\
  | jq -r '.data.talosconfig'\
  | base64 -d > talosconfig
talosctl --talosconfig talosconfig kubeconfig -n <controlplane node ip>
```

### Kommodity UI

The Kommodity UI is a web-based interface for fetching kubeconfigs of your Kommodity managed clusters. URL is `http://localhost:8000/ui/<clusterName>`.

![Kommodity UI](images/kommodity-ui.png)

## Features

### 🔒 OIDC Authentication

Kommodity supports authentication using OpenID Connect (OIDC), allowing integration with modern identity providers such as Google, or Azure AD. By leveraging OIDC, Kommodity enables secure, standards-based authentication for API requests.

This feature ensures that only authorized users—those in the configured admin group or the Kubernetes `system:masters` group—can perform privileged operations. When authentication is disabled (`KOMMODITY_INSECURE_DISABLE_AUTHENTICATION=true`), all requests are allowed by default for easier local development and testing.

### 🗄️ Storage

Kommodity sorely relies on Kine as translation layer for storage of Kubernetes resource objects in database of your choice. Check [here](https://deepwiki.com/k3s-io/kine#backend-driver-architecture) for supported databases in Kine.

### 🧩 Providers Configuration

Kommodity is designed to be extensible and support multiple providers. The list of supported providers is managed in the [`providers.yaml`](pkg/provider/providers.yaml) file. Each entry specifies the provider name, repository, relevant Go module, and the YAML file containing the provider’s CustomResourceDefinitions (CRDs).

For each provider, you can:

- **Specify CRD filters:** Use the `filter` field to select only the CRDs you need for your deployment.
- **Exclude unwanted CRDs:** Add CRD kinds to the `deny_list` to prevent them from being installed.
- **Define API scheme locations:** The `scheme_locations` field lists the API versions and groups to include for each provider.

This flexible configuration allows you to streamline your setup and avoid installing unnecessary resources.

> **ℹ️ Note:** Providers need to be compatible with version `1.10.4` of Cluster API.

### 👀 Audit Logging

Kommodity supports audit logging to track and record API requests and responses. Audit logs can be configured to use a custom audit policy file, specified via the `KOMMODITY_AUDIT_POLICY_FILE_PATH` environment variable.
Kommodity natively supports Kubernetes audit policy format documented here: https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/

### Mock KMS Service

The `kms` package provides a mock implementation of the [Talos Linux Key Management Service (KMS)][talos-kms-api]. This implementation:

- Exposes SideroLabs KMS API via gRPC.
- Includes mock `Seal` and `Unseal` methods.

`Seal` prepends the string `sealed:` to the input data.

```bash
# Test sealing.
export SECRET="This is super secret"
grpcurl -plaintext -d "{\"data\": \"$(echo -n "$SECRET" | base64)\"}" \
  localhost:8000 sidero.kms.KMSService/Seal \
  | jq -r '.data' | base64 --decode
```

`Unseal` removes the `sealed:` prefix from the input data.

```bash
# Test unsealing.
export SEALED="sealed:This is super secret"
grpcurl -plaintext -d "{\"data\": \"$(echo -n "$SEALED" | base64)\"}" \
  localhost:8000 sidero.kms.KMSService/Unseal \
  | jq -r '.data' | base64 --decode
```

## 🔧 Configuration

Several environment variables can be set to configure Kommodity:

| Environment Variable                        | Description                                                | Default Value           |
| ------------------------------------------- | ---------------------------------------------------------- | ----------------------- |
| `KOMMODITY_PORT`                            | Port for the Kommodity server                              | `5000`                  |
| `KOMMODITY_BASE_URL`                        | Base URL for the Kommodity server                          | `http://localhost:5000` |
| `KOMMODITY_ADMIN_GROUP`                     | Name of the admin group for privileged access              | (none)                  |
| `KOMMODITY_INSECURE_DISABLE_AUTHENTICATION` | Disable authentication for local development               | `false`                 |
| `KOMMODITY_OIDC_ISSUER_URL`                 | OIDC issuer URL for authentication                         | (none)                  |
| `KOMMODITY_OIDC_CLIENT_ID`                  | OIDC client ID for authentication                          | (none)                  |
| `KOMMODITY_OIDC_USERNAME_CLAIM`             | OIDC claim used for username                               | `email`                 |
| `KOMMODITY_OIDC_GROUPS_CLAIM`               | OIDC claim used for groups                                 | `groups`                |
| `KOMMODITY_ATTESTATION_NONCE_TTL`           | TTL for attestation nonces (e.g., `5m`, `1h`)              | `5m`                    |
| `KOMMODITY_DB_URI`                          | URI of the PostgreSQL database                             | (none)                  |
| `KOMMODITY_DEVELOPMENT_MODE`                | Enable development mode                                    | `false`                 |
| `KOMMODITY_INFRASTRUCTURE_PROVIDERS`        | Comma-separated list of infrastructure providers to enable | All                     |
| `KOMMODITY_AUDIT_POLICY_FILE_PATH`          | File path to the audit policy file                         | (none)                  |

## 🚀 Deployment

As Kommodity is a single binary, it can easily be deployed on any infrastructure.

The Terraform modules in [terraform/modules](terraform/modules) can be used to deploy Kommodity on some of the major hyperscalers (Azure for now, more to come).

See examples in [terraform/examples](terraform/examples) for specific deployment examples.

## ⛔ Limitations

- Helm [`hooks`](https://helm.sh/docs/topics/charts_hooks/) are not supported.

## 📜 License

Kommodity is licensed under the [Apache License 2.0](LICENSE).

[gvm]: https://github.com/moovweb/gvm
[talos-kms-api]: https://github.com/siderolabs/kms-client/blob/main/api/kms/kms.proto
[semver]: https://semver.org

# Kommodity

## What It Is

Kommodity is a single binary that packages Cluster API, Talos Linux providers, and security services to deploy sovereign Kubernetes clusters. Components include:

- **Kubernetes API server** - all-in-one server combining kube-apiserver, extension API server, and aggregation layer (via `k8s.io/apiserver` and `k8s.io/apiextensions-apiserver`)
- **Cluster API controllers** for cluster lifecycle
- **Talos Linux providers** for immutable machine configuration
- **Kine** for PostgreSQL-backed storage (no etcd)
- **KMS service** for disk encryption key management
- **Attestation service** for TPM-based machine verification
- **Metadata service** for secure config delivery
- **Auto-bootstrap extension** for zero-touch HA initialization

## Repository Structure

```
kommodity/
├── cmd/kommodity/          # Main binary entrypoint
├── pkg/
│   ├── combinedserver/     # All-in-one API server orchestration
│   ├── server/             # Kubernetes API server setup
│   ├── controller/         # Cluster API and provider controllers
│   ├── provider/           # Cloud provider implementations
│   ├── kine/               # PostgreSQL storage backend
│   ├── kms/                # Disk encryption key management service
│   ├── attestation/        # TPM attestation service
│   ├── metadata/           # Machine configuration delivery service
│   ├── config/             # Configuration handling
│   └── ui/                 # Web UI backend
├── charts/kommodity-cluster/   # Helm chart for deploying clusters
├── terraform/modules/      # Terraform modules for Kommodity deployment
├── openapi/                # OpenAPI specs for attestation and metadata APIs
├── examples/               # Example cluster configurations
└── articles/               # Technical documentation and blog posts
```

## Why It Exists

Built by Corti's platform team for healthcare AI requiring:

- **Data sovereignty**: Patient data in specific jurisdictions, on-premise when required
- **Compliance**: GDPR, ISO 27001, SOC 2 - encryption at rest is legally required
- **Auditability**: Complete audit trail for regulatory review
- **Operational consistency**: Same `kubectl`/Helm workflows across all environments

The goal: make sovereign cloud as routine as any other Kubernetes deployment.

## Key Security Mechanisms

**TPM Attestation**: Machines prove integrity using hardware TPM before receiving secrets. The attestation extension collects measurements (AppArmor, SELinux, Secure Boot, kernel lockdown, extensions) and submits signed TPM quotes. Failed attestation = no secrets delivered.

**Network KMS**: Disk encryption keys are stored as Kubernetes Secrets in the API server (persisted in PostgreSQL via Kine) and retrieved via the network on boot. Keys are per-volume (STATE, EPHEMERAL), bound to node UUID and peer IP. Key revocation = delete the Kubernetes Secret.

**Trade-off**: Machines cannot boot without network access to Kommodity when security features are enabled. This is intentional for revocation capability but requires HA deployment planning.

## Architecture Decisions

- **Talos Linux**: No SSH, no shell, immutable root filesystem, API-driven config. Provides defensible answers for compliance audits.
- **Cluster API**: Declarative cluster lifecycle via Kubernetes resources. Same operational model regardless of cloud provider.
- **Provider abstraction**: Not "write once run anywhere" but "learn once, operate anywhere". Provider-specific YAML, consistent operational patterns.
- **Auto-bootstrap**: Deterministic leader election (earliest boot time, lowest IP tiebreaker) enables zero-touch HA without manual "first node" designation. Private networks only.

## Related Repositories

- `github.com/kommodity-io/kommodity` - main repository
- `github.com/kommodity-io/kommodity-attestation-extension` - TPM attestation extension
- `github.com/kommodity-io/kommodity-autobootstrap-extension` - auto-bootstrap extension

## Making changes
When proposing changes to Kommodity, consider the size of the change. If it's above a certain threshold, it may be best to break it into smaller, more manageable pieces. For example, if you're adding a new feature that requires changes to multiple components, consider submitting separate pull requests for each component rather than one large pull request.

For larger changes, you should first create a PRD (Product Requirements Document) that outlines the proposed change, its rationale, and its impact on the system. The PRD should be approved before any code changes can be made.

The PRD phase should use `Plan Mode`.

## During development
Prerequisites for development include:
- Run `make generate`
- Revert changes in `pkg/openapi/intstr/zz_generated.openapi.go` as its a known bug in the code generator

For every change that compiles using `make build`, you should also run `make lint` to ensure that your code adheres to the project's coding standards, if it does not, you should fix the linting errors before proceeding.

Furthermore, make sure that:
- Use constants in the top of the file, instead of hardcoding values in your code.
- Use errors.go file to define custom error types and error messages, the errors.go should be located in each sub package.
- Use the `pkg/config` package to manage configuration settings for your changes.
- Use wrapped errors to provide more context when an error occurs. This can be done using the `fmt.Errorf` function with the `%w` verb to wrap the original error with additional context.
- Use `pkg/logging` for logging instead of printing directly to the console. Pass structured fields (for example, `zap.String`, `zap.Error`, and other `zap.Field` helpers) to `logger.Info`/`logger.Warn`/etc. to make logs easier to search and analyze; `zap.Fields(...)` is available when you need to group multiple fields.
- Divide the code into smaller functions and make sure to reuse code where possible and its meaningful.
- You don't need to write documentation for every function, you must for public functions. For private functions, do it where it makes sense and complexity is high.
- When it makes sense and domain specific, please create a struct for the function parameters instead of passing multiple parameters.
- For error handling, never inline the `err != nil` in the if statement. Always assign the error to a variable and then check if it's not nil on a separate line.
- When a function has enough arguments or a certain length to the name, so that you need to break it due to lint `lll`, have one argument per line.
- If two arguments has same type, add type to each argument. Instead of `arg1, arg2 string`, use `arg1 string, arg2 string`.
- All returned errors from functions need to be checked and handled properly and if needed wrapped.
- Do not touch files that begins with `Code generated by *.sh file name*; DO NOT EDIT.` These are autogenerated and should stay like that.

This is applicable for the `Implement Mode`.

## Validating changes
When a task is considering done with development, you should run `make test` to ensure that all tests are passing. If any tests are failing, you should fix the issues before considering the task complete.

When tests are passing, you should also run `make build-image` to ensure that the Docker image can be built successfully. If the image fails to build, you should fix the issues before considering the task complete.

If changes to Helm charts are made, you should run `make run-helm-unit-tests` to ensure that the Helm charts are valid and can be rendered successfully. If the Helm charts fail to render, you should fix the issues before considering the task complete.

This is applicable for the `Implement Mode`.

## Final review
When the changes builds, lints and tests successfully, please read through the PRD or the initial ask and compare it with the code changes to ensure that the implementation matches the original intent. If there are discrepancies, please address them before the task is considered complete.

This is applicable for the `Implement Mode`.

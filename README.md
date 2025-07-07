<img src="public/kommodity-logo.jpeg" alt="Kommodity Logo" style="border-radius: 15px; max-width: 150px; width: 15%; float: right; margin-top: 30px; margin-left: 30px; margin-bottom: 30px;"/>

# Kommodity

[![Go Report Card](https://img.shields.io/badge/go%20report-A+-brightgreen?style=flat-square)](https://goreportcard.com/report/github.com/kommodity-io/kommodity)
[![Go Reference](https://img.shields.io/badge/godoc-reference-blue?style=flat-square)](https://pkg.go.dev/github.com/kommodity-io/kommodity)
[![CI](https://img.shields.io/github/actions/workflow/status/kommodity-io/kommodity/release.yml?branch=main&label=ci&style=flat-square)](https://github.com/kommodity-io/kommodity/actions)
[![Release](https://img.shields.io/github/v/release/kommodity-io/kommodity?include_prereleases&label=release&style=flat-square)](https://github.com/kommodity-io/kommodity/releases)
[![License](https://img.shields.io/github/license/kommodity-io/kommodity?style=flat-square)](https://github.com/kommodity-io/kommodity/blob/main/LICENSE)

Kommodity is an open-source infrastructure platform to commoditize compute, storage, and networking.

> 🚧 EXPERIMENTAL 🚧: This project is in an early stage of development and is not yet ready for production use. APIs may break between minor releases, and the project is not yet feature-complete. The project does however adhere to [semantic versioning][semver], so patch releases will never break the API.

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
# Run the application locally.
make run
```

## Demo

```bash
# Test the application via `kubectl`.
kubectl --kubeconfig kommodity.yaml api-versions
# Test gRPC reflection.
grpcurl -plaintext localhost:8080 list
```

### Mock KMS Service

The `kms` package provides a mock implementation of the [Talos Linux Key Management Service (KMS)][talos-kms-api]. This implementation:

- Exposes SideroLabs KMS API via gRPC.
- Includes mock `Seal` and `Unseal` methods.

`Seal` prepends the string `sealed:` to the input data.

```bash
# Test sealing.
export SECRET="This is super secret"
grpcurl -plaintext -d "{\"data\": \"$(echo -n "$SECRET" | base64)\"}" \
  localhost:8080 sidero.kms.KMSService/Seal \
  | jq -r '.data' | base64 --decode
```

`Unseal` removes the `sealed:` prefix from the input data.

```bash
# Test unsealing.
export SEALED="sealed:This is super secret"
grpcurl -plaintext -d "{\"data\": \"$(echo -n "$SEALED" | base64)\"}" \
  localhost:8080 sidero.kms.KMSService/Unseal \
  | jq -r '.data' | base64 --decode
```

## License

Kommodity is licensed under the [Apache License 2.0](LICENSE).

[gvm]: https://github.com/moovweb/gvm
[talos-kms-api]: https://github.com/siderolabs/kms-client/blob/main/api/kms/kms.proto
[semver]: https://semver.org

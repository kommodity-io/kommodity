<img src="public/kommodity-logo.jpeg" alt="Kommodity Logo" style="border-radius: 15px; max-width: 150px; width: 15%; float: right; margin-top: 30px; margin-left: 30px; margin-bottom: 30px;"/>

# Kommodity

[![Go Report Card](https://img.shields.io/badge/go%20report-A+-brightgreen?style=flat-square)](https://goreportcard.com/report/github.com/kommodity-io/kommodity)
[![Go Reference](https://img.shields.io/badge/godoc-reference-blue?style=flat-square)](https://pkg.go.dev/github.com/kommodity-io/kommodity)
[![CI](https://img.shields.io/github/actions/workflow/status/kommodity-io/kommodity/release.yml?branch=main&label=ci&style=flat-square)](https://github.com/kommodity-io/kommodity/actions)
[![Release](https://img.shields.io/github/v/release/kommodity-io/kommodity?include_prereleases&label=release&style=flat-square)](https://github.com/kommodity-io/kommodity/releases)
[![License](https://img.shields.io/github/license/kommodity-io/kommodity?style=flat-square)](https://github.com/kommodity-io/kommodity/blob/main/LICENSE)

Kommodity is an open-source infrastructure platform to commoditize compute, storage, and networking.

## Development

Make sure to have a recent version of Go installed. We recommend using [gvm](https://github.com/moovweb/gvm) to install Go.

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
# Test the application via `kubectl`.
kubectl --kubeconfig kommodity.yaml api-versions
```

## License

Kommodity is licensed under the [Apache License 2.0](LICENSE).

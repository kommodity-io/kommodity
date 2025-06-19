<img src="public/kommodity-logo.jpeg" alt="Kommodity Logo" style="border-radius: 15px; max-width: 150px; width: 15%; float: right; margin-top: 30px; margin-left: 30px; margin-bottom: 30px;"/>

# Kommodity

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

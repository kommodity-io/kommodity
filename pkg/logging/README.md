## Logging

This package provides a simple logging library based on [zap][github-zap], which may be configured at runtime using the environment variables shown below. The logger is optimized for structured logging using [Loki][github-loki] as a log aggregation system. By default the logger will use a production-ready configuration, but it can be configured to output logs in a more human-readable format for development purposes.

| Environment variable | Description                      | Default | Allowed values                            |
| -------------------- | -------------------------------- | ------- | ----------------------------------------- |
| `LOG_LEVEL`          | The minimum log level to output. | `info`  | `debug`, `info`, `warn`, `error`, `fatal` |
| `LOG_FORMAT`         | The log format to use.           | `json`  | `json`, `console`                         |

### Example

You may run the following code simply by executing `go run pkg/logging/example/main.go` in the root of the repository. You can also run the example with the `LOG_FORMAT` environment variable set to `console` to see the human-readable output.

```go
package main

import (
	"context"
	"fmt"

	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
)

func main() {
	logger := logging.NewLogger()

	port := 8080

	// Don't do this.
	logger.Info(fmt.Sprintf("Starting HTTP server on port %d", port))
	logger.Sugar().Infof("Starting HTTP server on port %d", port)

	// Do this instead.
	logger.Info("Starting HTTP server", zap.Int("port", port))

	printLog(logger)

	// This is how you can use a context to pass a logger around.
	ctx := logging.WithLogger(context.Background(), logger)
	printLogWithContext(ctx)
}

// This is how you can pass the logger around.
func printLog(logger *zap.Logger) {
	logger.Info("This is a log message", zap.String("key", "value"))
}

// This is how you can use a context to pass the logger around.
func printLogWithContext(ctx context.Context) {
	logger := logging.FromContext(ctx).With(zap.String("context", "example"))
	logger.Info("This is a log message", zap.String("key", "value"))
}
```

[github-zap]: https://github.com/uber-go/zap
[github-loki]: https://github.com/grafana/loki

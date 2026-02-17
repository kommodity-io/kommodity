// Package combinedserver provides a combined gRPC and HTTP server with reverse proxy capabilities.
package combinedserver

import "errors"

// ErrServerNotRunning is returned when the server is not in the running state.
var ErrServerNotRunning = errors.New("server is not running")
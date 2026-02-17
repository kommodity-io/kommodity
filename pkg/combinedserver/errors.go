package combinedserver

import "errors"

var (
	// ErrServerNotRunning is returned when the server is not in the running state.
	ErrServerNotRunning = errors.New("server is not running")
	// ErrAPIServerNotReady is returned when the internal Kubernetes API server is not ready.
	ErrAPIServerNotReady = errors.New("apiserver is not ready")
)

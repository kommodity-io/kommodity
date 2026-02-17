package combinedserver

import "errors"

// ErrServerNotRunning is returned when the server is not in the running state.
var ErrServerNotRunning = errors.New("server is not running")

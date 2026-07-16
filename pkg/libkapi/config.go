package libkapi

import (
	"log/slog"
	"os"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// portEnvVar is the environment variable consulted for the default listen port.
	portEnvVar = "PORT"
	// defaultPort is used when portEnvVar is unset or invalid.
	defaultPort = 8080
)

// TLSConfig is reserved for future external TLS support.
//
// It is not implemented in this version: setting it on Config causes New to
// return ErrNotImplemented. It exists now so the public API will not need a
// breaking change once TLS support is added.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// Config configures a libkapi Server.
type Config struct {
	// Addr is the listener address, e.g. ":8080". If empty, it defaults to
	// ":"+$PORT, falling back to ":8080" if PORT is unset or invalid.
	Addr string

	// Storage is the polymorphic connection string used to reach or start the
	// storage backend, e.g. "postgres://...", "mysql://...", "sqlite://path.db",
	// "etcd://host:2379", or "unix:///path/to/kine.sock".
	Storage string

	// Logger receives libkapi's internal log output. If nil, slog.Default() is used.
	Logger *slog.Logger

	// TLS is reserved for future use; it must be nil in this version.
	TLS *TLSConfig

	// Handlers mount additional routes onto the server's shared mux, alongside
	// the built API server.
	Handlers []HTTPHandlerFactory

	// Scheme lets the caller register additional types beyond the standard API
	// groups libkapi wires by default.
	Scheme *runtime.Scheme
}

// resolvedAddr returns cfg.Addr, or a PORT-env-var/default-derived fallback.
func (cfg Config) resolvedAddr() string {
	if cfg.Addr != "" {
		return cfg.Addr
	}

	port := os.Getenv(portEnvVar)
	if port != "" {
		_, err := strconv.Atoi(port)
		if err == nil {
			return ":" + port
		}
	}

	return ":" + strconv.Itoa(defaultPort)
}

// resolvedLogger returns cfg.Logger, or slog.Default() if unset.
func (cfg Config) resolvedLogger() *slog.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}

	return slog.Default()
}

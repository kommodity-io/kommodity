package libkapi

import (
	"log/slog"
	"os"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

const (
	// portEnvVar is the environment variable consulted for the default listen port.
	portEnvVar = "PORT"
	// defaultPort is used when portEnvVar is unset or invalid.
	defaultPort = 8080
)

// TLSConfig is reserved for future external TLS support.
//
// It is not implemented in this version: passing it to WithTLS causes New to
// return ErrNotImplemented. It exists now so the public API will not need a
// breaking change once TLS support is added.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// config holds resolved state from all applied Options. Unexported: callers
// build it up via New's variadic Option list, never directly.
type config struct {
	addr             string
	storage          string
	logger           *slog.Logger
	tls              *TLSConfig
	handlers         []HTTPHandlerFactory
	grpcFactories    []GRPCServerFactory
	scheme           *runtime.Scheme
	authOpts         []auth.Option
	controllers      []Controller
	leaderElection   *LeaderElectionConfig
	webhook          *WebhookConfig
	postStartHooks   []PostStartHookFunc
	preShutdownHooks []PreShutdownHookFunc
}

// resolvedAddr returns cfg.addr, or a PORT-env-var/default-derived fallback.
func (cfg config) resolvedAddr() string {
	if cfg.addr != "" {
		return cfg.addr
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

// resolvedLogger returns cfg.logger, or slog.Default() if unset.
func (cfg config) resolvedLogger() *slog.Logger {
	if cfg.logger != nil {
		return cfg.logger
	}

	return slog.Default()
}

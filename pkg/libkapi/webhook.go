package libkapi

import "context"

// defaultWebhookDNSName is used as WebhookConfig.DNSNames's default (and as
// the webhook server's bind address, see webhookHost in manager.go): the
// webhook server only ever needs to answer admission/conversion calls made
// by the API server running in this same process, on this same host — never
// a Service or any other host's DNS name — so "localhost" is always
// correct, not just a placeholder.
const defaultWebhookDNSName = "localhost"

// WebhookConfig configures the manager's webhook server. The server is
// bound to 127.0.0.1 only (see webhookHost in manager.go) — it exists to
// answer admission/conversion calls from the API server built into this
// same process, not to be reachable from anywhere else, so there's no
// Service or cluster networking to configure.
type WebhookConfig struct {
	// Port the webhook server listens on. Defaults to 9443
	// (controller-runtime's own default) if zero.
	Port int

	// DNSNames are the Subject Alternative Names embedded in the
	// self-signed serving certificate ListenAndServe generates on startup
	// (see Server.syncWebhookCert) if the shared Secret backing it doesn't
	// already exist. Defaults to []string{"localhost"} if empty — the only
	// hostname a caller dialing 127.0.0.1 from this same host would ever
	// use.
	DNSNames []string
}

// WithWebhookServer enables the manager's webhook server. Any registered
// Controller can call mgr.GetWebhookServer().Register(path, handler) in its
// own SetupWithManager to serve admission or conversion webhooks — no
// change to the Controller interface. ListenAndServe adopts or creates the
// shared serving certificate Secret, and writes the certificate to disk,
// before the manager starts; its PEM bytes are available to callers via
// Server.WebhookCABundle. Without this option,
// GetWebhookServer still works (controller-runtime's own lazy default), but
// with no certificate provisioned, so a Controller trying to actually serve
// traffic through it would fail.
func WithWebhookServer(cfg WebhookConfig) Option {
	return func(_ context.Context, c *config) error {
		if len(cfg.DNSNames) == 0 {
			cfg.DNSNames = []string{defaultWebhookDNSName}
		}

		c.webhook = &cfg

		return nil
	}
}

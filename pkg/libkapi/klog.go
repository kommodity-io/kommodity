package libkapi

import (
	"log/slog"

	"k8s.io/klog/v2"
)

// InstallKlogAdapter bridges klog output to the consumer's slog logger so
// that the Kubernetes packages libkapi embeds (apiserver,
// apiextensions-apiserver, kube-aggregator, client-go, and their
// transitive dependencies) route their log output through logger instead
// of klog's default stderr writer.
//
// After this call, every klog.Info, klog.Warning, klog.Error, and
// contextual klog.FromContext / klog.Background call is forwarded to
// the slog handler backing logger. klog's own file/stderr output is
// suppressed (klog.SetSlogLogger installs a backing logr.Logger and
// redirects all traditional klog calls to it). Output is tagged with
// component=apiserver, matching the embedded Kubernetes packages
// (apiserver, apiextensions-apiserver, kube-aggregator, client-go)
// that produce it.
//
// The bridge installs a process-wide global: klog has a single backing
// logger, so the last call wins. New calls InstallKlogAdapter during
// server construction, before any goroutine starts logging; callers that
// need a different klog configuration can call this function again after
// New returns (the swap is safe while no other goroutine is logging).
//
// If logger is nil, slog.Default() is used, matching WithLogger's fallback
// behavior.
func InstallKlogAdapter(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}

	klog.SetSlogLogger(logger.With("component", "apiserver"))
}

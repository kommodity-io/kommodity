// Package logging bridges klog, gRPC, logrus, and controller-runtime's own
// logging into a caller-supplied slog.Logger.
//
// Kubernetes packages libkapi embeds use klog internally; gRPC (used by the
// etcd3 client and kine) uses grpclog; kine itself uses logrus; and
// sigs.k8s.io/controller-runtime (used by WithController's Manager, and by
// any consumer calling it directly) uses its own global logr sink. Without
// these bridges, each of those logs to its own default writer instead of
// the consumer's logger.
package logging

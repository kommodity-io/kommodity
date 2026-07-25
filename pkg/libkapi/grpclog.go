package libkapi

import (
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/grpc/grpclog"
)

// InstallGRPCLogAdapter bridges gRPC's logger to the consumer's slog
// logger, demoting INFO- and WARNING-level connection lifecycle
// messages to slog.Debug so they don't clutter normal output but
// remain available when troubleshooting with a debug-level handler.
//
// During storage initialization, the apiserver creates multiple etcd3
// clients (CRD server, generic server, aggregator server, one per API
// group). Each client creates gRPC subchannels that dial the kine
// socket. Some connections are torn down mid-handshake as the
// apiserver reconfigures its storage layer, and gRPC logs each failure
// via channelz.Warningf ("addrConn.createTransport failed to connect").
// These messages are cosmetic — gRPC retries automatically — but
// produce dozens of alarming warnings during startup.
//
// Error messages are forwarded at slog.Error level. Fatal messages
// are logged at Error level and do NOT call os.Exit (though the
// package-level grpclog.Fatal does call os.Exit after the adapter
// logs — that's gRPC's hardcoded behavior and cannot be prevented
// through LoggerV2; Fatal is never called during normal operation).
//
// Like InstallKlogAdapter, this installs a process-wide global: gRPC's
// logger is a single backing instance, so the last call wins. New
// calls this before any gRPC connection is made (before
// storage.Resolve and buildServer), so all gRPC logs from kine, the
// etcd3 client, and the apiserver's storage layer are captured.
//
// If logger is nil, slog.Default() is used, matching WithLogger's fallback
// behavior.
func InstallGRPCLogAdapter(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}

	grpclog.SetLoggerV2(&grpcLogAdapter{logger: logger.With("component", "grpc")})
}

// grpcLogAdapter implements grpclog.LoggerV2 by routing Error messages
// to slog.Error and demoting INFO and WARNING to slog.Debug. It does
// not implement grpclog.DepthLoggerV2: gRPC falls back to Infoln/
// Warningln/Errorln when DepthLoggerV2Impl is nil, which is exactly
// what we want — the fallback calls hit our demoted or routed
// methods.
//
// INFO and WARNING are demoted to Debug because gRPC's channelz logs
// connection lifecycle events (e.g., "addrConn.createTransport failed
// to connect") at these levels. These events are cosmetic — gRPC
// retries automatically — and produce dozens of alarming messages
// during storage initialization when the apiserver creates and tears
// down etcd3 clients. Demoting to Debug keeps them available for
// troubleshooting with a debug-level handler without cluttering normal
// output. Error-level messages are still routed at Error because they
// indicate genuine failures.
type grpcLogAdapter struct {
	logger *slog.Logger
}

// trimMsg formats args like fmt.Sprintln and strips the trailing
// newline that slog handlers add themselves.
func trimMsg(args []any) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}

// trimMsgf formats args like fmt.Sprintf.
func trimMsgf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func (a *grpcLogAdapter) Info(args ...any) {
	a.logger.Debug(fmt.Sprint(args...))
}

func (a *grpcLogAdapter) Infoln(args ...any) {
	a.logger.Debug(trimMsg(args))
}

func (a *grpcLogAdapter) Infof(format string, args ...any) {
	a.logger.Debug(trimMsgf(format, args...))
}

func (a *grpcLogAdapter) Warning(args ...any) {
	a.logger.Debug(fmt.Sprint(args...))
}

func (a *grpcLogAdapter) Warningln(args ...any) {
	a.logger.Debug(trimMsg(args))
}

func (a *grpcLogAdapter) Warningf(format string, args ...any) {
	a.logger.Debug(trimMsgf(format, args...))
}

func (a *grpcLogAdapter) Error(args ...any) {
	a.logger.Error(fmt.Sprint(args...))
}

func (a *grpcLogAdapter) Errorln(args ...any) {
	a.logger.Error(trimMsg(args))
}

func (a *grpcLogAdapter) Errorf(format string, args ...any) {
	a.logger.Error(trimMsgf(format, args...))
}

func (a *grpcLogAdapter) Fatal(args ...any) {
	a.logger.Error(fmt.Sprint(args...))
}

func (a *grpcLogAdapter) Fatalln(args ...any) {
	a.logger.Error(trimMsg(args))
}

func (a *grpcLogAdapter) Fatalf(format string, args ...any) {
	a.logger.Error(trimMsgf(format, args...))
}

// V reports whether verbosity level l is enabled. Returning true for
// all levels ensures gRPC callers who check V before logging (e.g.,
// "if logger.V(2) { ... }") always reach the adapter, which then
// demotes Info/Warning to Debug. This prevents gRPC from skipping
// the adapter entirely at higher verbosity levels.
func (a *grpcLogAdapter) V(_ int) bool {
	return true
}

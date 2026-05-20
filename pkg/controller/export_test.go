package controller

import (
	"context"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

// This file exposes internal symbols of the controller package to the
// controller_test package. It is compiled only during `go test`.

// ConfigForGC is an exported wrapper around the unexported configForGC helper.
func ConfigForGC(restConfig *rest.Config) *rest.Config {
	return configForGC(restConfig)
}

// GCUserAgent re-exports the User-Agent constant set by configForGC.
const GCUserAgent = gcUserAgent

// GCQPSMultiplier re-exports the QPS/Burst multiplier applied by configForGC.
const GCQPSMultiplier = gcQPSMultiplier

// SetupGarbageCollectorWithDeps invokes setupGarbageCollector with the full
// dependency set so external tests can exercise both the disabled-path
// early return and the enabled-path validation branches without constructing
// a real controller-runtime Manager.
func SetupGarbageCollectorWithDeps(
	ctx context.Context,
	gcCfg *config.GarbageCollectorConfig,
	mgr ctrl.Manager,
	restConfig *rest.Config,
) error {
	return setupGarbageCollector(ctx, gcDeps{
		manager:    mgr,
		restConfig: restConfig,
		gcConfig:   gcCfg,
	})
}

// RunnerForTest wraps a partially-initialized garbageCollectorRunner so
// external tests can exercise the REST mapper reset goroutine.
type RunnerForTest struct {
	inner *garbageCollectorRunner
}

// NewRunnerForTest returns a runner whose only populated field is the REST
// mapper. The returned value is only suitable for calling RunRESTMapperReset.
func NewRunnerForTest(mapper meta.ResettableRESTMapper) *RunnerForTest {
	return &RunnerForTest{inner: &garbageCollectorRunner{restMapper: mapper}}
}

// RunRESTMapperReset proxies to the unexported runRESTMapperReset method.
func (r *RunnerForTest) RunRESTMapperReset(ctx context.Context, period time.Duration) {
	r.inner.runRESTMapperReset(ctx, period)
}

package controller_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/controller"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// stubResettableMapper is a minimal meta.ResettableRESTMapper that records
// Reset() invocations. Only Reset is exercised by runRESTMapperReset, so the
// embedded RESTMapper is intentionally left as a nil interface.
type stubResettableMapper struct {
	meta.RESTMapper

	resetCalls atomic.Int32
}

func (s *stubResettableMapper) Reset() {
	s.resetCalls.Add(1)
}

func TestConfigForGC_DoublesQPSAndBurstWhenSet(t *testing.T) {
	t.Parallel()

	src := &rest.Config{QPS: 7, Burst: 14, UserAgent: "caller-ua"}

	got := controller.ConfigForGC(src)

	if got.QPS != 7*controller.GCQPSMultiplier {
		t.Errorf("got QPS = %v, want %v", got.QPS, 7*controller.GCQPSMultiplier)
	}

	if got.Burst != 14*controller.GCQPSMultiplier {
		t.Errorf("got Burst = %v, want %v", got.Burst, 14*controller.GCQPSMultiplier)
	}

	if got.UserAgent != controller.GCUserAgent {
		t.Errorf("got UserAgent = %q, want %q", got.UserAgent, controller.GCUserAgent)
	}

	if got.RateLimiter != nil {
		t.Error("RateLimiter must not be installed when QPS is configured")
	}
}

func TestConfigForGC_InstallsDefaultRateLimiterWhenUnconfigured(t *testing.T) {
	t.Parallel()

	src := &rest.Config{}

	got := controller.ConfigForGC(src)

	if got.RateLimiter == nil {
		t.Fatal("expected default rate limiter to be installed when both QPS and RateLimiter are unset")
	}

	if got.QPS != 0 {
		t.Errorf("QPS must not be mutated when installing default rate limiter, got %v", got.QPS)
	}

	if got.Burst != 0 {
		t.Errorf("Burst must not be mutated when installing default rate limiter, got %v", got.Burst)
	}

	if src.RateLimiter != nil {
		t.Error("source RateLimiter must not be mutated")
	}
}

func TestConfigForGC_PreservesExistingRateLimiter(t *testing.T) {
	t.Parallel()

	existing := flowcontrol.NewTokenBucketRateLimiter(1, 1)
	src := &rest.Config{RateLimiter: existing}

	got := controller.ConfigForGC(src)

	if got.RateLimiter != existing {
		t.Error("existing RateLimiter must be preserved, not replaced")
	}

	if got.QPS != 0 || got.Burst != 0 {
		t.Errorf("QPS/Burst must not be set when RateLimiter is preserved, got QPS=%v Burst=%v",
			got.QPS, got.Burst)
	}
}

func TestConfigForGC_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	src := &rest.Config{QPS: 10, Burst: 20, UserAgent: "caller-ua"}

	_ = controller.ConfigForGC(src)

	if src.QPS != 10 {
		t.Errorf("source QPS was mutated: got %v, want 10", src.QPS)
	}

	if src.Burst != 20 {
		t.Errorf("source Burst was mutated: got %v, want 20", src.Burst)
	}

	if src.UserAgent != "caller-ua" {
		t.Errorf("source UserAgent was mutated: got %q, want %q", src.UserAgent, "caller-ua")
	}
}

func TestSetupGarbageCollector_NilConfigIsNoop(t *testing.T) {
	t.Parallel()

	err := controller.SetupGarbageCollectorWithConfig(context.Background(), nil)
	if err != nil {
		t.Errorf("SetupGarbageCollectorWithConfig with nil config returned error: %v", err)
	}
}

func TestSetupGarbageCollector_DisabledIsNoop(t *testing.T) {
	t.Parallel()

	err := controller.SetupGarbageCollectorWithConfig(
		context.Background(),
		&config.GarbageCollectorConfig{Enabled: false},
	)
	if err != nil {
		t.Errorf("SetupGarbageCollectorWithConfig with disabled config returned error: %v", err)
	}
}

func TestRunRESTMapperReset_CallsResetOnTick(t *testing.T) {
	t.Parallel()

	const (
		tickPeriod = 10 * time.Millisecond
		runFor     = 60 * time.Millisecond
		minResets  = 3
	)

	stub := &stubResettableMapper{}
	runner := controller.NewRunnerForTest(stub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		runner.RunRESTMapperReset(ctx, tickPeriod)
		close(done)
	}()

	time.Sleep(runFor)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runRESTMapperReset did not exit within 1s after ctx cancel")
	}

	got := stub.resetCalls.Load()
	if got < minResets {
		t.Errorf("expected at least %d Reset calls in %v at tick period %v, got %d",
			minResets, runFor, tickPeriod, got)
	}
}

func TestRunRESTMapperReset_ExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	stub := &stubResettableMapper{}
	runner := controller.NewRunnerForTest(stub)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		// Long tick period — the only way to exit is via context cancel.
		runner.RunRESTMapperReset(ctx, time.Hour)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runRESTMapperReset did not exit within 1s after ctx cancel")
	}

	if stub.resetCalls.Load() != 0 {
		t.Errorf("Reset must not be called before the first tick, got %d calls",
			stub.resetCalls.Load())
	}
}

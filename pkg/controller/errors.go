package controller

import "errors"

var (
	// ErrGarbageCollectorClientBuild is returned when constructing the typed,
	// metadata, or discovery client for the garbage collector fails.
	ErrGarbageCollectorClientBuild = errors.New("failed to build garbage collector client")

	// ErrGarbageCollectorInit is returned when garbagecollector.NewGarbageCollector fails.
	ErrGarbageCollectorInit = errors.New("failed to initialize garbage collector")

	// ErrGarbageCollectorMissingDep is returned when the garbage collector is
	// enabled but a required dependency (controller-runtime Manager or
	// loopback rest.Config) is nil.
	ErrGarbageCollectorMissingDep = errors.New("garbage collector dependency missing")
)

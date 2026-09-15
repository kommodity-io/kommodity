package apiserver

import "errors"

var (
	// ErrGroupVersionNotRegistered is returned when a standard API group has no version registered in the scheme.
	ErrGroupVersionNotRegistered = errors.New("no scheme version registered for group")
	// ErrServiceResolutionUnsupported is returned by the noopServiceResolver when
	// a remote APIService (Spec.Service != nil) is created, which libkapi does
	// not support.
	ErrServiceResolutionUnsupported = errors.New(
		"service resolution is not supported in libkapi: no Service or Endpoints resources are wired")
)

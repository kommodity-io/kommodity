// Package apiserver builds the generic apiserver, apiextensions (CRD), and
// aggregation-layer machinery libkapi's Server assembles into a single
// delegation chain: standard-API delegate -> CRD server -> aggregator.
//
// Everything here is stateless bootstrapping logic parameterized by explicit
// scheme/codec/config values - it holds no reference to libkapi's own
// Server or its private configuration, so it can be exercised directly (see
// spike_test.go in the parent package) independently of the rest of
// libkapi's Option/Server wiring.
package apiserver

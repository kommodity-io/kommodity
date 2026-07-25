// Package libkapi builds an embeddable, Kubernetes-API-compatible server.
//
// A built server has no TLS and no authentication by default: every request is
// treated as an anonymous, always-allowed request. Anyone who can reach the
// configured listener address has full read/write access to every resource the
// server exposes. Callers deploying outside a trusted network must put a
// TLS-terminating, authenticating proxy in front of the server themselves.
package libkapi

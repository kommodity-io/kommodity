//go:generate go run github.com/swaggo/swag/v2/cmd/swag@latest init -g ./doc.go -o ../../openapi/attestation --outputTypes "json,yaml" --parseDependency --parseInternal

// Package attestation provides functionality for attestation services for establishing trust for the Talos machines.
//
// @title       Kommodity Attestation API
// @version     0.1.0
// @description Attestation endpoints for Talos machines.
// @schemes     http
// @BasePath    /
package attestation

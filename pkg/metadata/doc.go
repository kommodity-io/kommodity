//go:generate go run github.com/swaggo/swag/v2/cmd/swag@latest init -g ./doc.go -o ../../openapi/metadata --outputTypes "json,yaml" --parseDependency --parseInternal

// Package metadata provides functionality for metadata services for the Talos machines.
//
// @title       Kommodity Metadata API
// @version     0.1.0
// @description Metadata service endpoints for Kommodity.
// @schemes     http
// @BasePath    /
package metadata

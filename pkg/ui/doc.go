//go:generate go run github.com/swaggo/swag/v2/cmd/swag@latest init -g ./doc.go -o ../../openapi/ui --outputTypes "json,yaml" --parseDependency --parseInternal

// Package ui provides functionality for the Kommodity UI.
//
// @title       Kommodity UI API
// @version     0.1.0
// @description API endpoints for Kommodity UI.
// @schemes     https
// @BasePath    /api
package ui

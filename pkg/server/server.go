// Package server implements the Kommodity server,
// including the supported API resources.
package server

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/kommodity-io/kommodity/pkg/apis/core/v1alpha1"
	"github.com/kommodity-io/kommodity/pkg/apiserver"
	"github.com/kommodity-io/kommodity/pkg/database"
	"github.com/kommodity-io/kommodity/pkg/genericserver"
	"github.com/kommodity-io/kommodity/pkg/storage/sql"
)

// New create a new kommodity server instance.
func New(ctx context.Context) (*genericserver.GenericServer, error) {
	//nolint:varnamelen
	db, err := database.SetupDB()
	if err != nil {
		return nil, fmt.Errorf("failed to setup database connection: %w", err)
	}

	srv, err := apiserver.NewAPIServer().
		WithResourceAndHandler(&v1alpha1.Namespace{}, sql.NewJSONStorageProvider(&v1alpha1.Namespace{}, db)).
		WithHealthCheck(&HealthCheckProvider{db: db}).
		Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build API server: %w", err)
	}

	return srv, nil
}

// HealthCheckProvider implements the HealthCheck interface for the Kommodity server.
type HealthCheckProvider struct {
	db *sqlx.DB
}

var _ genericserver.HealthCheck = &HealthCheckProvider{}

// Readyz implements the readyz endpoint for health checks.
func (h *HealthCheckProvider) Readyz(ctx context.Context) error {
	err := h.db.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// Livez implements the livez endpoint for health checks.
func (h *HealthCheckProvider) Livez(_ context.Context) error {
	// Intentionally left empty.
	return nil
}

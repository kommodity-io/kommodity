// Package server implements the Kommodity server,
// including the supported API resources.
package server

import (
	"context"
	"fmt"

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
		Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build API server: %w", err)
	}

	return srv, nil
}

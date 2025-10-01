// Package database provides functions to set up and manage the database connection.
package database

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/kommodity-io/kommodity/pkg/config"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// SetupDB initializes the database connection using the KOMMODITY_DB_URI environment variable.
func SetupDB(cfg *config.KommodityConfig) (*sqlx.DB, error) {
	dbURI := *cfg.DBURI

	db, err := sqlx.Connect(dbURI.Scheme, dbURI.String())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

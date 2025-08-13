// Package database provides functions to set up and manage the database connection.
package database

import (
	"fmt"
	"net/url"
	"os"

	"github.com/jmoiron/sqlx"

	_ "github.com/lib/pq" // PostgreSQL driver
	// "github.com/joho/godotenv"
)

// SetupDB initializes the database connection using the KOMMODITY_DB_URI environment variable.
func SetupDB() (*sqlx.DB, error) {

	// TODO: fix -> Debug mode wont load .env file
	// err := godotenv.Load()
	// if err != nil {
	// 	return nil, ErrCantLoadDotEnv
	// }

	dbURI := os.Getenv("KOMMODITY_DB_URI")
	if dbURI == "" {
		return nil, ErrKommodityDBEnvVarNotSet
	}

	u, err := url.Parse(dbURI)
	if err != nil {
		return nil, fmt.Errorf("invalid KOMMODITY_DB_URI: %w", err)
	}

	db, err := sqlx.Connect(u.Scheme, dbURI)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

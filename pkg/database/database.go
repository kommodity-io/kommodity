package database

import (
	"fmt"
	"net/url"
	"os"

	"github.com/jmoiron/sqlx"

	_ "github.com/lib/pq" // PostgreSQL driver
)

func SetupDB() (*sqlx.DB, error) {
	dbURI := os.Getenv("KOMMODITY_DB_URI")
	if dbURI == "" {
		return nil, errors.New("KOMMODITY_DB_URI environment variable is not set")
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

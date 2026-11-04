package database

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ConnectDb opens a connection pool to the Postgres database at url, for example
// postgres://user:password@localhost:5432/app?sslmode=disable.
func ConnectDb(url string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	// Stay well below the server's max_connections (100 by default), leaving room for other clients
	// and for more than one instance of the app. Recycle connections so failovers and server-side
	// limits are picked up.
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(25)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return db, nil
}

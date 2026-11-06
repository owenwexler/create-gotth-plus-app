package database

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// ConnectDb opens a connection pool to the MySQL database at dsn, in go-sql-driver/mysql's format,
// for example user:password@tcp(localhost:3306)/app?parseTime=true&charset=utf8mb4&loc=UTC.
// parseTime=true is required: the models use time.Time.
func ConnectDb(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	// Stay well below the server's max_connections (151 by default), leaving room for other clients
	// and for more than one instance of the app. Close connections before MySQL's wait_timeout
	// (8 hours by default) or a proxy drops them.
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(25)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return db, nil
}

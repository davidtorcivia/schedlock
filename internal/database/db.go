// Package database handles SQLite connection setup and management.
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the sql.DB connection with additional functionality.
type DB struct {
	*sql.DB
	path string
}

// Open creates or opens a SQLite database with WAL mode enabled.
func Open(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database with foreign keys enabled
	dsn := fmt.Sprintf("%s?_foreign_keys=on&_busy_timeout=5000", path)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{
		DB:   sqlDB,
		path: path,
	}

	// Configure SQLite for optimal performance
	if err := db.configure(); err != nil {
		sqlDB.Close()
		return nil, err
	}

	// Run migrations
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// configure sets up SQLite pragmas for optimal performance and safety.
func (db *DB) configure() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA cache_size=-64000", // 64MB cache
		"PRAGMA foreign_keys=ON",
		"PRAGMA temp_store=MEMORY",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}

	return nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	// Checkpoint WAL before closing
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		// Log but don't fail
		fmt.Printf("Warning: WAL checkpoint failed: %v\n", err)
	}
	return db.DB.Close()
}

// Path returns the database file path.
func (db *DB) Path() string {
	return db.path
}

// Vacuum performs database maintenance.
func (db *DB) Vacuum() error {
	_, err := db.Exec("VACUUM")
	return err
}

// BeginTx starts a transaction with the given options.
func (db *DB) BeginTx() (*sql.Tx, error) {
	return db.DB.Begin()
}

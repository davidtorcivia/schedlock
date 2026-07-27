// Package database handles SQLite connection setup and management.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the sql.DB connection with additional functionality.
type DB struct {
	*sql.DB
	path string
}

// Open creates or opens a SQLite database with WAL mode enabled and runs migrations.
//
// The pragmas below are passed through the DSN rather than executed after
// connecting: database/sql maintains a pool, and a pragma executed via Exec
// applies only to whichever connection happened to serve it. DSN pragmas are
// replayed for every connection the pool opens.
func Open(path string) (*DB, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("failed to create database directory: %w", err)
			}
		}
	}

	sqlDB, err := sql.Open("sqlite", buildDSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite permits a single writer. Capping the pool at a small number of
	// connections keeps reads concurrent under WAL while the busy_timeout
	// pragma absorbs the brief contention writers see.
	sqlDB.SetMaxOpenConns(maxOpenConns(path))
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Hour)

	db := &DB{DB: sqlDB, path: path}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// buildDSN assembles the connection string, including the pragmas that must be
// applied to every pooled connection.
func buildDSN(path string) string {
	pragmas := []string{
		"foreign_keys(1)",
		"busy_timeout(5000)",
		"temp_store(MEMORY)",
	}

	// An in-memory database is private to a single connection unless opened
	// with a shared cache, which would defeat the pool. Such databases get a
	// one-connection pool (see maxOpenConns) and no on-disk journal tuning.
	if path == ":memory:" {
		pragmas = append(pragmas, "journal_mode(MEMORY)")
	} else {
		pragmas = append(pragmas,
			"journal_mode(WAL)",
			"synchronous(NORMAL)",
			"cache_size(-64000)", // 64 MiB page cache
		)
	}

	query := make(url.Values, len(pragmas))
	for _, p := range pragmas {
		query.Add("_pragma", p)
	}

	if strings.HasPrefix(path, "file:") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return path + sep + query.Encode()
	}

	return path + "?" + query.Encode()
}

func maxOpenConns(path string) int {
	if path == ":memory:" {
		return 1
	}
	return 8
}

// Close checkpoints the write-ahead log and closes the connection pool.
func (db *DB) Close() error {
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		// A failed checkpoint is not fatal: the WAL is replayed on next open.
		logf("warning: WAL checkpoint failed: %v", err)
	}
	return db.DB.Close()
}

// Path returns the database file path.
func (db *DB) Path() string {
	return db.path
}

// Vacuum reclaims free pages. It cannot run inside a transaction.
func (db *DB) Vacuum(ctx context.Context) error {
	_, err := db.ExecContext(ctx, "VACUUM")
	return err
}

// logf writes to stderr. The database package is initialized before the
// application logger, so it cannot depend on it.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

package database

import (
	"database/sql"
	"time"

	"github.com/dtorcivia/schedlock/internal/util"
)

// Scanner is satisfied by both *sql.Row and *sql.Rows, letting a single scan
// function serve single-row and multi-row queries.
type Scanner interface {
	Scan(dest ...any) error
}

// NullTimeText converts a nullable SQLite TEXT timestamp into a sql.NullTime.
//
// Timestamps are stored as TEXT so that values written by SQL (datetime('now'))
// and by Go share one representation. The driver hands them back as strings, so
// they are parsed explicitly here rather than relying on driver-specific
// column-type conversion.
func NullTimeText(v sql.NullString) sql.NullTime {
	if !v.Valid || v.String == "" {
		return sql.NullTime{}
	}
	t, err := util.ParseSQLiteTimestamp(v.String)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// TimeText converts a non-null SQLite TEXT timestamp, yielding the zero time
// if it cannot be parsed.
func TimeText(v string) time.Time {
	t, err := util.ParseSQLiteTimestamp(v)
	if err != nil {
		return time.Time{}
	}
	return t
}

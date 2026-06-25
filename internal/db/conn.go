package db

import (
	"context"

	"github.com/clarkeandrew/rian/internal/history"
)

// Conn is an open connection to a specific database. It hides the concrete
// driver (pgx, database/sql, ...) from the engine, which depends only on this
// interface and on Dialect.
//
// ApplyMigration encapsulates the transaction strategy so the engine does not
// have to branch on it:
//   - When the dialect supports transactional DDL, the statements and the
//     success history row are committed atomically; a failure rolls everything
//     back and leaves NO row (re-run after fixing).
//   - Otherwise (MySQL DDL implicitly commits), a failure records the row with
//     success=false and returns the error; the migration then requires repair.
type Conn interface {
	Dialect() Dialect

	// EnsureHistory creates the schema-history table if it does not exist.
	EnsureHistory(ctx context.Context, table string) error

	// ReadHistory returns all rows ordered by installed_rank.
	ReadHistory(ctx context.Context, table string) ([]history.Row, error)

	// ApplyMigration runs the migration's statements and records its history
	// row, honoring the dialect's transaction strategy. row.ExecutionTime and
	// row.Success are set by the implementation.
	ApplyMigration(ctx context.Context, table string, statements []string, row history.Row) error

	// InsertHistory inserts a single history row (used by baseline).
	InsertHistory(ctx context.Context, table string, row history.Row) error

	// DeleteFailed removes rows with success=false (used by repair) and returns
	// the number deleted.
	DeleteFailed(ctx context.Context, table string) (int, error)

	// Close releases the connection.
	Close(ctx context.Context) error
}

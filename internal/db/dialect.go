// Package db defines the database-specific surface Rian depends on. Everything
// outside this package (engine, history) talks to a Dialect, never to a concrete
// driver, so adding a database means implementing Dialect.
package db

// Dialect isolates the per-database behavior Rian needs. The SQL-building
// methods take the (already configured) schema-history table name and return
// driver-ready SQL; column order matches Flyway's schema-history format.
type Dialect interface {
	// Name is the dialect's identifier (e.g. "postgresql", "mysql").
	Name() string

	// QuoteIdentifier quotes a table or schema identifier for safe interpolation.
	QuoteIdentifier(name string) string

	// SupportsTransactionalDDL reports whether DDL can participate in a
	// transaction and roll back on failure. Postgres: true. MySQL: false
	// (implicit commit on DDL), which forces the mark-failed-then-repair path.
	SupportsTransactionalDDL() bool

	// CreateHistoryTableSQL returns idempotent DDL creating the schema-history
	// table if it does not already exist.
	CreateHistoryTableSQL(table string) string

	// SelectHistorySQL returns SQL selecting all history rows ordered by
	// installed_rank, with columns in Flyway order (excluding installed_on,
	// which the database defaults).
	SelectHistorySQL(table string) string

	// InsertHistorySQL returns a parameterized INSERT for one history row. The
	// nine bind parameters are, in order: installed_rank, version, description,
	// type, script, checksum, installed_by, execution_time, success.
	InsertHistorySQL(table string) string

	// UpdateChecksumSQL returns a parameterized UPDATE setting a row's checksum.
	// The two bind parameters are, in order: checksum, installed_rank.
	UpdateChecksumSQL(table string) string
}

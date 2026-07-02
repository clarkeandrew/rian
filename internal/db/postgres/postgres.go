// Package postgres implements the db.Dialect for PostgreSQL.
//
// Postgres supports transactional DDL, so the engine runs each migration in a
// transaction and rolls back on failure. The schema-history DDL mirrors the
// table Flyway creates for Postgres so the two tools interoperate.
package postgres

import (
	"fmt"
	"strings"

	"github.com/clarkeandrew/rian/internal/db"
)

// Dialect is the PostgreSQL dialect.
type Dialect struct{}

// compile-time check that Dialect satisfies db.Dialect.
var _ db.Dialect = Dialect{}

func (Dialect) Name() string { return "postgresql" }

// SupportsTransactionalDDL is true: Postgres DDL is transactional.
func (Dialect) SupportsTransactionalDDL() bool { return true }

// QuoteIdentifier double-quotes an identifier, escaping embedded double quotes.
func (Dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// CreateHistoryTableSQL returns DDL matching Flyway's Postgres schema-history
// table (column names, order, and types), created only if absent.
func (d Dialect) CreateHistoryTableSQL(table string) string {
	t := d.QuoteIdentifier(table)
	pk := d.QuoteIdentifier(table + "_pk")
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    "installed_rank" integer NOT NULL,
    "version" varchar(50),
    "description" varchar(200) NOT NULL,
    "type" varchar(20) NOT NULL,
    "script" varchar(1000) NOT NULL,
    "checksum" integer,
    "installed_by" varchar(100) NOT NULL,
    "installed_on" timestamp NOT NULL DEFAULT now(),
    "execution_time" integer NOT NULL,
    "success" boolean NOT NULL,
    CONSTRAINT %s PRIMARY KEY ("installed_rank")
)`, t, pk)
}

// SelectHistorySQL selects history rows in installed_rank order.
func (d Dialect) SelectHistorySQL(table string) string {
	return `SELECT "installed_rank", "version", "description", "type", "script", ` +
		`"checksum", "installed_by", "execution_time", "success" FROM ` +
		d.QuoteIdentifier(table) + ` ORDER BY "installed_rank"`
}

// InsertHistorySQL returns the parameterized INSERT ($1..$9). installed_on is
// left to the column default.
func (d Dialect) InsertHistorySQL(table string) string {
	return `INSERT INTO ` + d.QuoteIdentifier(table) +
		` ("installed_rank", "version", "description", "type", "script", ` +
		`"checksum", "installed_by", "execution_time", "success") ` +
		`VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
}

// UpdateChecksumSQL returns the parameterized UPDATE ($1 = checksum,
// $2 = installed_rank) used by repair.
func (d Dialect) UpdateChecksumSQL(table string) string {
	return `UPDATE ` + d.QuoteIdentifier(table) +
		` SET "checksum" = $1 WHERE "installed_rank" = $2`
}

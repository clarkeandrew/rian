// Package mysql implements the db.Dialect and db.Conn for MySQL.
//
// MySQL implicitly commits on DDL, so a failed multi-statement DDL migration
// cannot be rolled back. Following Flyway, Rian therefore reports
// SupportsTransactionalDDL=false and, on failure, records the migration with
// success=false and returns the error; the migration then requires `repair`.
package mysql

import (
	"fmt"
	"strings"

	"github.com/clarkeandrew/rian/internal/db"
)

// Dialect is the MySQL dialect.
type Dialect struct{}

var _ db.Dialect = Dialect{}

func (Dialect) Name() string { return "mysql" }

// SupportsTransactionalDDL is false: MySQL implicitly commits DDL.
func (Dialect) SupportsTransactionalDDL() bool { return false }

// QuoteIdentifier wraps an identifier in backticks, doubling any embedded ones.
func (Dialect) QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// CreateHistoryTableSQL returns DDL matching Flyway's MySQL schema-history table
// (columns, order, types, nullability, ENGINE, and installed_on default).
//
// Flyway also creates a secondary index `<table>_s_idx` on (success). Rian omits
// it: against an existing Flyway-managed database the table (and index) already
// exist, and CREATE TABLE IF NOT EXISTS is a no-op; the index only affects a
// brand-new table Rian initializes, where it is not required for correctness.
func (d Dialect) CreateHistoryTableSQL(table string) string {
	t := d.QuoteIdentifier(table)
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n"+
		"    `installed_rank` int NOT NULL,\n"+
		"    `version` varchar(50),\n"+
		"    `description` varchar(200) NOT NULL,\n"+
		"    `type` varchar(20) NOT NULL,\n"+
		"    `script` varchar(1000) NOT NULL,\n"+
		"    `checksum` int,\n"+
		"    `installed_by` varchar(100) NOT NULL,\n"+
		"    `installed_on` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,\n"+
		"    `execution_time` int NOT NULL,\n"+
		"    `success` bool NOT NULL,\n"+
		"    PRIMARY KEY (`installed_rank`)\n"+
		") ENGINE=InnoDB", t)
}

// SelectHistorySQL selects history rows in installed_rank order.
func (d Dialect) SelectHistorySQL(table string) string {
	return "SELECT `installed_rank`, `version`, `description`, `type`, `script`, " +
		"`checksum`, `installed_by`, `execution_time`, `success` FROM " +
		d.QuoteIdentifier(table) + " ORDER BY `installed_rank`"
}

// InsertHistorySQL returns the parameterized INSERT (MySQL uses ? placeholders).
// installed_on is left to the column default.
func (d Dialect) InsertHistorySQL(table string) string {
	return "INSERT INTO " + d.QuoteIdentifier(table) +
		" (`installed_rank`, `version`, `description`, `type`, `script`, " +
		"`checksum`, `installed_by`, `execution_time`, `success`) " +
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"
}

// UpdateChecksumSQL returns the parameterized UPDATE (checksum, installed_rank)
// used by repair.
func (d Dialect) UpdateChecksumSQL(table string) string {
	return "UPDATE " + d.QuoteIdentifier(table) +
		" SET `checksum` = ? WHERE `installed_rank` = ?"
}

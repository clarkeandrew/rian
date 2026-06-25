package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/history"
)

// Conn is a pgx-backed Postgres connection implementing db.Conn.
type Conn struct {
	conn    *pgx.Conn
	dialect Dialect
}

var _ db.Conn = (*Conn)(nil)

// DSN converts a Flyway-style JDBC URL ("jdbc:postgresql://host:port/db?...")
// into a DSN pgx understands by stripping the "jdbc:" prefix. A URL that is
// already in pgx form is returned unchanged.
func DSN(url string) string {
	return strings.TrimPrefix(url, "jdbc:")
}

// Connect opens a Postgres connection. user/password, when non-empty, override
// any credentials embedded in the URL.
func Connect(ctx context.Context, url, user, password string) (*Conn, error) {
	cfg, err := pgx.ParseConfig(DSN(url))
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	if user != "" {
		cfg.User = user
	}
	if password != "" {
		cfg.Password = password
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return &Conn{conn: c}, nil
}

func (c *Conn) Dialect() db.Dialect { return c.dialect }

func (c *Conn) Close(ctx context.Context) error { return c.conn.Close(ctx) }

func (c *Conn) EnsureHistory(ctx context.Context, table string) error {
	_, err := c.conn.Exec(ctx, c.dialect.CreateHistoryTableSQL(table))
	if err != nil {
		return fmt.Errorf("create history table: %w", err)
	}
	return nil
}

func (c *Conn) ReadHistory(ctx context.Context, table string) ([]history.Row, error) {
	rows, err := c.conn.Query(ctx, c.dialect.SelectHistorySQL(table))
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer rows.Close()

	var out []history.Row
	for rows.Next() {
		var r history.Row
		var version *string
		var checksum *int32
		if err := rows.Scan(&r.InstalledRank, &version, &r.Description, &r.Type,
			&r.Script, &checksum, &r.InstalledBy, &r.ExecutionTime, &r.Success); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}
		if version != nil {
			r.Version = *version
		}
		r.Checksum = checksum
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *Conn) ApplyMigration(ctx context.Context, table string, statements []string, row history.Row) error {
	// Postgres supports transactional DDL: run statements + record the row
	// atomically; on failure the whole transaction rolls back, leaving no row.
	start := time.Now()
	tx, err := c.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after a successful Commit

	for _, stmt := range statements {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply %s: %w", row.Script, err)
		}
	}

	row.ExecutionTime = int(time.Since(start).Milliseconds())
	row.Success = true
	if _, err := tx.Exec(ctx, c.dialect.InsertHistorySQL(table), insertArgs(row)...); err != nil {
		return fmt.Errorf("insert history row: %w", err)
	}
	return tx.Commit(ctx)
}

func (c *Conn) InsertHistory(ctx context.Context, table string, row history.Row) error {
	if _, err := c.conn.Exec(ctx, c.dialect.InsertHistorySQL(table), insertArgs(row)...); err != nil {
		return fmt.Errorf("insert history row: %w", err)
	}
	return nil
}

func (c *Conn) DeleteFailed(ctx context.Context, table string) (int, error) {
	tag, err := c.conn.Exec(ctx,
		`DELETE FROM `+c.dialect.QuoteIdentifier(table)+` WHERE "success" = false`)
	if err != nil {
		return 0, fmt.Errorf("delete failed rows: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// insertArgs maps a history.Row to the InsertHistorySQL bind parameters, sending
// SQL NULL (nil) for an empty version or absent checksum.
func insertArgs(row history.Row) []any {
	var version any
	if row.Version != "" {
		version = row.Version
	}
	var checksum any
	if row.Checksum != nil {
		checksum = *row.Checksum
	}
	return []any{
		row.InstalledRank, version, row.Description, row.Type, row.Script,
		checksum, row.InstalledBy, row.ExecutionTime, row.Success,
	}
}

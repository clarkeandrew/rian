package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/history"
)

// executor is the exec-only subset of *sql.DB used by ApplyMigration,
// InsertHistory, and DeleteFailed, so those paths are unit-testable with a fake.
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Conn is a database/sql-backed MySQL connection implementing db.Conn.
type Conn struct {
	db      *sql.DB
	exec    executor // defaults to db; overridable in tests
	dialect Dialect
}

var _ db.Conn = (*Conn)(nil)

// DSN converts a Flyway-style JDBC URL ("jdbc:mysql://host:port/db?params") into
// the go-sql-driver DSN. Explicit user/password override any credentials
// embedded in the URL. The DSN is assembled through the driver's structured
// mysql.Config (not string concatenation) so credentials with special
// characters and the password-without-username case are handled correctly.
func DSN(jdbcURL, user, password string) (string, error) {
	s := strings.TrimPrefix(jdbcURL, "jdbc:")
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse mysql url: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("mysql url %q is missing a host", jdbcURL)
	}
	if u.User != nil {
		if user == "" {
			user = u.User.Username()
		}
		if password == "" {
			if p, ok := u.User.Password(); ok {
				password = p
			}
		}
	}

	cfg := gomysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	cfg.User = user
	cfg.Passwd = password
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	if u.RawQuery != "" {
		params, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", fmt.Errorf("parse mysql url params: %w", err)
		}
		cfg.Params = make(map[string]string, len(params))
		for k, vs := range params {
			if len(vs) > 0 {
				cfg.Params[k] = vs[0]
			}
		}
	}
	return cfg.FormatDSN(), nil
}

// Connect opens a MySQL connection.
func Connect(ctx context.Context, jdbcURL, user, password string) (*Conn, error) {
	dsn, err := DSN(jdbcURL, user, password)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect mysql: %w", err)
	}
	return &Conn{db: sqlDB, exec: sqlDB}, nil
}

func (c *Conn) Dialect() db.Dialect { return c.dialect }

func (c *Conn) Close(context.Context) error { return c.db.Close() }

func (c *Conn) EnsureHistory(ctx context.Context, table string) error {
	if _, err := c.db.ExecContext(ctx, c.dialect.CreateHistoryTableSQL(table)); err != nil {
		return fmt.Errorf("create history table: %w", err)
	}
	return nil
}

func (c *Conn) ReadHistory(ctx context.Context, table string) ([]history.Row, error) {
	rows, err := c.db.QueryContext(ctx, c.dialect.SelectHistorySQL(table))
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer rows.Close()

	var out []history.Row
	for rows.Next() {
		var r history.Row
		var version sql.NullString
		var checksum sql.NullInt32
		if err := rows.Scan(&r.InstalledRank, &version, &r.Description, &r.Type,
			&r.Script, &checksum, &r.InstalledBy, &r.ExecutionTime, &r.Success); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}
		if version.Valid {
			r.Version = version.String
		}
		if checksum.Valid {
			v := checksum.Int32
			r.Checksum = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApplyMigration runs the statements sequentially. MySQL implicitly commits DDL,
// so there is no rollback: on the first failing statement the migration is
// recorded with success=false and the error is returned, requiring repair.
func (c *Conn) ApplyMigration(ctx context.Context, table string, statements []string, row history.Row) error {
	start := time.Now()
	var execErr error
	for _, stmt := range statements {
		if _, err := c.exec.ExecContext(ctx, stmt); err != nil {
			execErr = fmt.Errorf("apply %s: %w", row.Script, err)
			break
		}
	}
	row.ExecutionTime = int(time.Since(start).Milliseconds())
	row.Success = execErr == nil

	if err := c.InsertHistory(ctx, table, row); err != nil {
		if execErr != nil {
			return execErr // surface the original failure
		}
		return err
	}
	return execErr
}

func (c *Conn) InsertHistory(ctx context.Context, table string, row history.Row) error {
	if _, err := c.exec.ExecContext(ctx, c.dialect.InsertHistorySQL(table), insertArgs(row)...); err != nil {
		return fmt.Errorf("insert history row: %w", err)
	}
	return nil
}

func (c *Conn) UpdateChecksum(ctx context.Context, table string, installedRank int, checksum int32) error {
	if _, err := c.exec.ExecContext(ctx, c.dialect.UpdateChecksumSQL(table), checksum, installedRank); err != nil {
		return fmt.Errorf("update checksum: %w", err)
	}
	return nil
}

func (c *Conn) DeleteFailed(ctx context.Context, table string) (int, error) {
	res, err := c.exec.ExecContext(ctx,
		"DELETE FROM "+c.dialect.QuoteIdentifier(table)+" WHERE `success` = false")
	if err != nil {
		return 0, fmt.Errorf("delete failed rows: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(n), nil
}

// insertArgs maps a history.Row to the InsertHistorySQL bind parameters, sending
// SQL NULL for an empty version or absent checksum.
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

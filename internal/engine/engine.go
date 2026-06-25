// Package engine orchestrates Rian's commands (migrate, info, validate,
// baseline, repair) over the scan, checksum, sql, history, and db packages. It
// is driver-agnostic: it talks to a db.Conn and a db.Dialect, never to a
// concrete driver.
package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/clarkeandrew/rian/internal/checksum"
	"github.com/clarkeandrew/rian/internal/config"
	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/history"
	"github.com/clarkeandrew/rian/internal/scan"
	"github.com/clarkeandrew/rian/internal/sql"
)

// Engine couples a connection with the resolved configuration.
type Engine struct {
	Conn db.Conn
	Cfg  config.Config
}

// New builds an Engine.
func New(conn db.Conn, cfg config.Config) *Engine {
	return &Engine{Conn: conn, Cfg: cfg}
}

func (e *Engine) scanOptions() scan.Options {
	return scan.Options{
		SQLPrefix:        e.Cfg.SQLMigrationPrefix,
		RepeatablePrefix: e.Cfg.RepeatableSQLMigrationPrefix,
		Separator:        e.Cfg.SQLMigrationSeparator,
		Suffixes:         e.Cfg.SQLMigrationSuffixes,
	}
}

// resolve discovers migrations and computes their checksums (keyed by Script).
func (e *Engine) resolve() ([]scan.Migration, map[string]int32, error) {
	migs, err := scan.Scan(e.Cfg.Locations, e.scanOptions())
	if err != nil {
		return nil, nil, err
	}
	checksums := make(map[string]int32, len(migs))
	for _, m := range migs {
		data, err := os.ReadFile(m.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", m.Script, err)
		}
		checksums[m.Script] = checksum.CalculateBytes(data)
	}
	return migs, checksums, nil
}

// MigrateResult reports what Migrate applied.
type MigrateResult struct {
	Applied []scan.Migration
}

// Migrate applies all pending migrations in order. It stops at the first
// failure (like Flyway).
func (e *Engine) Migrate(ctx context.Context) (MigrateResult, error) {
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return MigrateResult{}, err
	}
	migs, checksums, err := e.resolve()
	if err != nil {
		return MigrateResult{}, err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return MigrateResult{}, err
	}

	pending := history.Pending(migs, checksums, rows)
	rank := history.NextRank(rows)

	var result MigrateResult
	for _, m := range pending {
		stmts, err := e.prepare(m, checksums)
		if err != nil {
			return result, err
		}
		row := e.historyRow(m, checksums[m.Script], rank)
		if err := e.Conn.ApplyMigration(ctx, e.Cfg.Table, stmts, row); err != nil {
			return result, fmt.Errorf("migration %s failed: %w", m.Script, err)
		}
		result.Applied = append(result.Applied, m)
		rank++
	}
	return result, nil
}

// prepare reads a migration file, substitutes placeholders, and splits it into
// statements.
func (e *Engine) prepare(m scan.Migration, _ map[string]int32) ([]string, error) {
	data, err := os.ReadFile(m.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.Script, err)
	}
	substituted, err := sql.Substitute(string(data), e.Cfg.Placeholders,
		e.Cfg.PlaceholderPrefix, e.Cfg.PlaceholderSuffix, e.Cfg.PlaceholderReplacement)
	if err != nil {
		return nil, fmt.Errorf("placeholders in %s: %w", m.Script, err)
	}
	return sql.Split(substituted), nil
}

func (e *Engine) historyRow(m scan.Migration, checksum int32, rank int) history.Row {
	row := history.Row{
		InstalledRank: rank,
		Description:   m.Description,
		Type:          "SQL",
		Script:        m.Script,
		Checksum:      &checksum,
		InstalledBy:   e.Cfg.User,
	}
	if m.Type == scan.Versioned {
		row.Version = m.Version.String()
	}
	return row
}

// InfoEntry is one row of `info` output.
type InfoEntry struct {
	Type        string
	Version     string
	Description string
	Script      string
	Status      string
}

// Info returns the state of every migration (applied or pending).
func (e *Engine) Info(ctx context.Context) ([]InfoEntry, error) {
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return nil, err
	}
	migs, checksums, err := e.resolve()
	if err != nil {
		return nil, err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	for _, m := range history.Pending(migs, checksums, rows) {
		pending[m.Script] = true
	}

	entries := make([]InfoEntry, 0, len(migs))
	for _, m := range migs {
		version := ""
		if m.Version != nil {
			version = m.Version.String()
		}
		status := "Applied"
		if pending[m.Script] {
			status = "Pending"
		}
		entries = append(entries, InfoEntry{
			Type:        string(typeName(m.Type)),
			Version:     version,
			Description: m.Description,
			Script:      m.Script,
			Status:      status,
		})
	}
	return entries, nil
}

// Validate returns the list of validation problems (empty means valid).
func (e *Engine) Validate(ctx context.Context) ([]history.Problem, error) {
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return nil, err
	}
	migs, checksums, err := e.resolve()
	if err != nil {
		return nil, err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return nil, err
	}
	return history.Validate(migs, checksums, rows), nil
}

// Baseline records a baseline row at the configured baseline version, marking
// all earlier migrations as already applied. It is a no-op error if history
// already contains rows.
func (e *Engine) Baseline(ctx context.Context) error {
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return fmt.Errorf("cannot baseline: schema history %q is not empty", e.Cfg.Table)
	}
	row := history.Row{
		InstalledRank: 1,
		Version:       e.Cfg.BaselineVersion,
		Description:   "<< Flyway Baseline >>",
		Type:          "BASELINE",
		Script:        "<< Flyway Baseline >>",
		InstalledBy:   e.Cfg.User,
		Success:       true,
	}
	return e.Conn.InsertHistory(ctx, e.Cfg.Table, row)
}

// Repair removes failed migration rows so a corrected migration can be re-run.
func (e *Engine) Repair(ctx context.Context) (int, error) {
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return 0, err
	}
	return e.Conn.DeleteFailed(ctx, e.Cfg.Table)
}

func typeName(t scan.Type) string {
	if t == scan.Repeatable {
		return "Repeatable"
	}
	return "Versioned"
}

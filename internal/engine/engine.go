// Package engine orchestrates Rian's commands (migrate, info, validate,
// baseline, repair) over the scan, checksum, sql, history, and db packages. It
// is driver-agnostic: it talks to a db.Conn and a db.Dialect, never to a
// concrete driver.
package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

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

// resolved is the discovered migration set with each file read exactly once:
// its checksum and raw contents, both keyed by Script.
type resolved struct {
	migrations []scan.Migration
	checksums  map[string]int32
	contents   map[string][]byte
}

// resolve discovers migrations and reads each file once, computing its checksum
// and retaining its bytes for later substitution/splitting.
func (e *Engine) resolve() (resolved, error) {
	migs, err := scan.Scan(e.Cfg.Locations, e.scanOptions())
	if err != nil {
		return resolved{}, err
	}
	r := resolved{
		migrations: migs,
		checksums:  make(map[string]int32, len(migs)),
		contents:   make(map[string][]byte, len(migs)),
	}
	for _, m := range migs {
		data, err := os.ReadFile(m.Path)
		if err != nil {
			return resolved{}, fmt.Errorf("read %s: %w", m.Script, err)
		}
		r.contents[m.Script] = data
		r.checksums[m.Script] = checksum.CalculateBytes(data)
	}
	return r, nil
}

// MigrateResult reports what Migrate applied.
type MigrateResult struct {
	Applied []scan.Migration
}

// Migrate applies all pending migrations in order, up to the configured target
// version (if any). It stops at the first failure (like Flyway). Matching
// Flyway's defaults, it validates the recorded history first (validateOnMigrate)
// and refuses out-of-order migrations — a pending version below the latest
// applied one — unless outOfOrder is enabled.
func (e *Engine) Migrate(ctx context.Context) (MigrateResult, error) {
	target, err := e.targetVersion()
	if err != nil {
		return MigrateResult{}, err
	}
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return MigrateResult{}, err
	}
	r, err := e.resolve()
	if err != nil {
		return MigrateResult{}, err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return MigrateResult{}, err
	}
	if e.Cfg.ValidateOnMigrate {
		if problems := history.Validate(r.migrations, r.checksums, rows); len(problems) > 0 {
			return MigrateResult{}, fmt.Errorf("validation failed before migrate: %s", joinProblems(problems))
		}
	}

	pending := aboveTargetDropped(history.Pending(r.migrations, r.checksums, rows), target)
	if maxV := history.MaxAppliedVersion(rows); maxV != nil && !e.Cfg.OutOfOrder {
		for _, m := range pending {
			if m.Type == scan.Versioned && m.Version.Compare(maxV) < 0 {
				return MigrateResult{}, fmt.Errorf(
					"migration %s (version %s) is below the already-applied version %s; set outOfOrder=true to allow it",
					m.Script, m.Version, maxV)
			}
		}
	}
	rank := history.NextRank(rows)

	var result MigrateResult
	for _, m := range pending {
		stmts, err := e.prepare(m, r.contents[m.Script])
		if err != nil {
			return result, err
		}
		row := e.historyRow(m, r.checksums[m.Script], rank)
		if err := e.Conn.ApplyMigration(ctx, e.Cfg.Table, stmts, row); err != nil {
			return result, fmt.Errorf("migration %s failed: %w", m.Script, err)
		}
		result.Applied = append(result.Applied, m)
		rank++
	}
	return result, nil
}

// installedBy returns the value recorded in installed_by: the configured
// override, or the connection user.
func (e *Engine) installedBy() string {
	if e.Cfg.InstalledBy != "" {
		return e.Cfg.InstalledBy
	}
	return e.Cfg.User
}

// targetVersion parses the configured target; empty or "latest" means no limit.
func (e *Engine) targetVersion() (*scan.Version, error) {
	t := e.Cfg.Target
	if t == "" || strings.EqualFold(t, "latest") {
		return nil, nil
	}
	v, err := scan.ParseVersion(t)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	return v, nil
}

// aboveTargetDropped filters out versioned migrations above the target version.
// A nil target keeps everything; repeatable migrations are never filtered.
func aboveTargetDropped(migs []scan.Migration, target *scan.Version) []scan.Migration {
	if target == nil {
		return migs
	}
	kept := migs[:0]
	for _, m := range migs {
		if m.Type == scan.Versioned && m.Version.Compare(target) > 0 {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// prepare substitutes placeholders in the migration's (already read) content and
// splits it into statements.
func (e *Engine) prepare(m scan.Migration, content []byte) ([]string, error) {
	substituted, err := sql.Substitute(string(content), e.Cfg.Placeholders,
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
		InstalledBy:   e.installedBy(),
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

// Info returns the state of every migration (applied, pending, below baseline,
// or above the configured target).
func (e *Engine) Info(ctx context.Context) ([]InfoEntry, error) {
	target, err := e.targetVersion()
	if err != nil {
		return nil, err
	}
	if err := e.Conn.EnsureHistory(ctx, e.Cfg.Table); err != nil {
		return nil, err
	}
	r, err := e.resolve()
	if err != nil {
		return nil, err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	for _, m := range history.Pending(r.migrations, r.checksums, rows) {
		pending[m.Script] = true
	}
	baseline := history.BaselineVersion(rows)

	entries := make([]InfoEntry, 0, len(r.migrations))
	for _, m := range r.migrations {
		version := ""
		if m.Version != nil {
			version = m.Version.String()
		}
		status := "Applied"
		switch {
		case pending[m.Script] && m.Type == scan.Versioned && target != nil && m.Version.Compare(target) > 0:
			status = "Above Target"
		case pending[m.Script]:
			status = "Pending"
		case m.Type == scan.Versioned && baseline != nil && m.Version.Compare(baseline) <= 0 &&
			!history.VersionApplied(m.Version, rows):
			status = "Below Baseline"
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
	r, err := e.resolve()
	if err != nil {
		return nil, err
	}
	rows, err := e.Conn.ReadHistory(ctx, e.Cfg.Table)
	if err != nil {
		return nil, err
	}
	return history.Validate(r.migrations, r.checksums, rows), nil
}

// Baseline records a baseline row at the configured baseline version, marking
// all earlier migrations as already applied. It is a no-op error if history
// already contains rows.
func (e *Engine) Baseline(ctx context.Context) error {
	if _, err := scan.ParseVersion(e.Cfg.BaselineVersion); err != nil {
		return fmt.Errorf("baseline version: %w", err)
	}
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
		Type:          history.TypeBaseline,
		Script:        "<< Flyway Baseline >>",
		InstalledBy:   e.installedBy(),
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

func joinProblems(ps []history.Problem) string {
	strs := make([]string, len(ps))
	for i, p := range ps {
		strs[i] = p.String()
	}
	return strings.Join(strs, "; ")
}

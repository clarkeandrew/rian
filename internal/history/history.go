// Package history models Flyway's schema-history table and derives migration
// state by joining discovered migrations against the recorded history.
//
// The table format is fixed by Flyway so the two tools can read each other's
// history; see Row for the columns. All logic here is pure (no database), so it
// is unit-testable; the actual table I/O lives in internal/db.
package history

import (
	"fmt"

	"github.com/clarkeandrew/rian/internal/scan"
)

// DefaultTable is Flyway's default schema-history table name.
const DefaultTable = "flyway_schema_history"

// TypeBaseline is the history `type` Flyway records for a baseline row.
const TypeBaseline = "BASELINE"

// Row is one schema-history record. Column order/types match Flyway:
// installed_rank, version, description, type, script, checksum, installed_by,
// installed_on, execution_time, success. Version is empty for repeatable
// migrations; Checksum is nil when the column is NULL.
type Row struct {
	InstalledRank int
	Version       string
	Description   string
	Type          string
	Script        string
	Checksum      *int32
	InstalledBy   string
	ExecutionTime int
	Success       bool
}

// NextRank returns the installed_rank to use for the next applied migration:
// one greater than the maximum present (1 when the history is empty).
func NextRank(rows []Row) int {
	max := 0
	for _, r := range rows {
		if r.InstalledRank > max {
			max = r.InstalledRank
		}
	}
	return max + 1
}

// Pending returns the migrations that still need to be applied, preserving the
// input order (scan already sorts versioned-then-repeatable). A versioned
// migration is pending when no successful history row records its version;
// versions at or below a recorded baseline count as applied (Flyway's baseline
// semantics). A repeatable migration is pending when it has never been applied
// successfully or its checksum has changed since the last successful
// application.
//
// checksums maps a migration's Script to its computed checksum and is consulted
// only for repeatable migrations.
func Pending(migs []scan.Migration, checksums map[string]int32, rows []Row) []scan.Migration {
	baseline := BaselineVersion(rows)
	var pending []scan.Migration
	for _, m := range migs {
		switch m.Type {
		case scan.Versioned:
			if baseline != nil && m.Version.Compare(baseline) <= 0 {
				continue
			}
			if !VersionApplied(m.Version, rows) {
				pending = append(pending, m)
			}
		case scan.Repeatable:
			if repeatableNeedsApply(m, checksums[m.Script], rows) {
				pending = append(pending, m)
			}
		}
	}
	return pending
}

// ProblemKind categorizes a validation problem.
type ProblemKind string

const (
	ChecksumMismatch ProblemKind = "checksum mismatch"
	MissingMigration ProblemKind = "applied migration not resolved locally"
	FailedMigration  ProblemKind = "failed migration present"
)

// Problem is a single validation failure.
type Problem struct {
	Kind   ProblemKind
	Script string
	Detail string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s: %s (%s)", p.Kind, p.Script, p.Detail)
}

// Validate checks the recorded history against the migrations on disk, the way
// Flyway's validate does: every successful versioned row must resolve to a local
// migration with a matching checksum, and any failed row must be repaired.
// checksums maps Script to the locally-computed checksum.
func Validate(migs []scan.Migration, checksums map[string]int32, rows []Row) []Problem {
	onDisk := map[string]scan.Migration{}
	repeatableDescs := map[string]bool{}
	for _, m := range migs {
		switch m.Type {
		case scan.Versioned:
			onDisk[m.Version.Canonical()] = m
		case scan.Repeatable:
			repeatableDescs[m.Description] = true
		}
	}

	var problems []Problem
	for _, r := range rows {
		if !r.Success {
			problems = append(problems, Problem{FailedMigration, r.Script, "run repair to remove the failed entry"})
			continue
		}
		if r.Type == TypeBaseline {
			// The baseline marker has no corresponding file on disk.
			continue
		}
		if r.Version == "" {
			// Repeatable: resolved by description. A successful row that no longer
			// maps to any local repeatable is unresolved (Flyway raises this). A
			// checksum DIFFERENCE is not an error — Flyway re-applies on change.
			if !repeatableDescs[r.Description] {
				problems = append(problems, Problem{MissingMigration, r.Script, "repeatable recorded in history but not found on disk"})
			}
			continue
		}
		rv, err := scan.ParseVersion(r.Version)
		if err != nil {
			// A version Rian cannot parse must surface, not silently skip the
			// checksum check — otherwise a parse divergence from Flyway would
			// validate "successfully" while comparing nothing.
			problems = append(problems, Problem{MissingMigration, r.Script,
				fmt.Sprintf("unparseable version %q in history: %v", r.Version, err)})
			continue
		}
		m, ok := onDisk[rv.Canonical()]
		if !ok {
			problems = append(problems, Problem{MissingMigration, r.Script, "recorded in history but not found on disk"})
			continue
		}
		if r.Checksum != nil && *r.Checksum != checksums[m.Script] {
			problems = append(problems, Problem{
				Kind:   ChecksumMismatch,
				Script: m.Script,
				Detail: fmt.Sprintf("history checksum %d != local %d", *r.Checksum, checksums[m.Script]),
			})
		}
	}
	return problems
}

// VersionApplied reports whether some successful history row records this
// version (comparing canonically, so "1" matches "1.0").
func VersionApplied(v *scan.Version, rows []Row) bool {
	for _, r := range rows {
		if r.Success && r.Version != "" && sameVersion(v, r.Version) {
			return true
		}
	}
	return false
}

// BaselineVersion returns the version of the latest successful baseline row, or
// nil when the history has none. Versioned migrations at or below this version
// are treated as already applied.
func BaselineVersion(rows []Row) *scan.Version {
	var base *scan.Version
	for _, r := range rows {
		if !r.Success || r.Type != TypeBaseline || r.Version == "" {
			continue
		}
		v, err := scan.ParseVersion(r.Version)
		if err != nil {
			continue
		}
		if base == nil || v.Compare(base) > 0 {
			base = v
		}
	}
	return base
}

// MaxAppliedVersion returns the highest version among successful versioned rows
// (a baseline row counts), or nil when there are none.
func MaxAppliedVersion(rows []Row) *scan.Version {
	var max *scan.Version
	for _, r := range rows {
		if !r.Success || r.Version == "" {
			continue
		}
		v, err := scan.ParseVersion(r.Version)
		if err != nil {
			continue
		}
		if max == nil || v.Compare(max) > 0 {
			max = v
		}
	}
	return max
}

// repeatableNeedsApply reports whether a repeatable migration (matched by
// description, as Flyway does) must be re-applied: never applied, or its
// checksum differs from the last successful application.
func repeatableNeedsApply(m scan.Migration, checksum int32, rows []Row) bool {
	var last *Row
	for i := range rows {
		r := &rows[i]
		if r.Success && r.Version == "" && r.Description == m.Description {
			last = r
		}
	}
	if last == nil {
		return true
	}
	return last.Checksum == nil || *last.Checksum != checksum
}

func sameVersion(a *scan.Version, b string) bool {
	bv, err := scan.ParseVersion(b)
	if err != nil {
		return false
	}
	return a.Compare(bv) == 0
}

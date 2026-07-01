package history

import (
	"strings"
	"testing"

	"github.com/clarkeandrew/rian/internal/scan"
)

func ptr(i int32) *int32 { return &i }

func ver(t *testing.T, s string) *scan.Version {
	t.Helper()
	v, err := scan.ParseVersion(s)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", s, err)
	}
	return v
}

func versioned(t *testing.T, v, desc, script string) scan.Migration {
	return scan.Migration{Type: scan.Versioned, Version: ver(t, v), Description: desc, Script: script}
}

func repeatable(desc, script string) scan.Migration {
	return scan.Migration{Type: scan.Repeatable, Description: desc, Script: script}
}

func TestNextRank(t *testing.T) {
	if got := NextRank(nil); got != 1 {
		t.Errorf("empty history NextRank = %d, want 1", got)
	}
	rows := []Row{{InstalledRank: 1}, {InstalledRank: 5}, {InstalledRank: 3}}
	if got := NextRank(rows); got != 6 {
		t.Errorf("NextRank = %d, want 6", got)
	}
}

func TestPendingVersioned(t *testing.T) {
	migs := []scan.Migration{
		versioned(t, "1", "init", "V1__init.sql"),
		versioned(t, "2", "next", "V2__next.sql"),
		versioned(t, "3", "third", "V3__third.sql"),
	}
	rows := []Row{
		{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Success: true},
		// V2 applied but FAILED -> still pending (must be retried/repaired).
		{InstalledRank: 2, Version: "2", Script: "V2__next.sql", Success: false},
	}
	got := Pending(migs, nil, rows)
	if len(got) != 2 || got[0].Script != "V2__next.sql" || got[1].Script != "V3__third.sql" {
		t.Errorf("pending = %v, want V2 and V3", scripts(got))
	}
}

func TestPendingVersionedMatchesAcrossVersionFormatting(t *testing.T) {
	// History stored "1.0"; on-disk version is "1". They are the same version,
	// so the migration is NOT pending (guards phantom re-runs).
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	rows := []Row{{InstalledRank: 1, Version: "1.0", Script: "V1.0__init.sql", Success: true}}
	if got := Pending(migs, nil, rows); len(got) != 0 {
		t.Errorf("expected no pending (1 == 1.0), got %v", scripts(got))
	}
}

func TestPendingSkipsBaselineAndBelow(t *testing.T) {
	migs := []scan.Migration{
		versioned(t, "1", "init", "V1__init.sql"),
		versioned(t, "2", "next", "V2__next.sql"),
		versioned(t, "3", "third", "V3__third.sql"),
	}
	rows := []Row{{InstalledRank: 1, Version: "2", Type: TypeBaseline, Script: "<< Flyway Baseline >>", Success: true}}
	got := Pending(migs, nil, rows)
	if len(got) != 1 || got[0].Script != "V3__third.sql" {
		t.Errorf("pending = %v, want only V3 (V1/V2 are at or below the baseline)", scripts(got))
	}
}

func TestBaselineVersionAndMaxApplied(t *testing.T) {
	rows := []Row{
		{InstalledRank: 1, Version: "2", Type: TypeBaseline, Success: true},
		{InstalledRank: 2, Version: "3", Type: "SQL", Success: true},
		{InstalledRank: 3, Version: "9", Type: "SQL", Success: false}, // failed: ignored
	}
	if b := BaselineVersion(rows); b == nil || b.String() != "2" {
		t.Errorf("BaselineVersion = %v, want 2", b)
	}
	if m := MaxAppliedVersion(rows); m == nil || m.String() != "3" {
		t.Errorf("MaxAppliedVersion = %v, want 3", m)
	}
	if BaselineVersion(nil) != nil || MaxAppliedVersion(nil) != nil {
		t.Error("empty history should have no baseline or max applied version")
	}
}

func TestPendingRepeatable(t *testing.T) {
	migs := []scan.Migration{repeatable("refresh views", "R__refresh_views.sql")}
	checksums := map[string]int32{"R__refresh_views.sql": 100}

	// Never applied -> pending.
	if got := Pending(migs, checksums, nil); len(got) != 1 {
		t.Errorf("never-applied repeatable should be pending, got %v", scripts(got))
	}
	// Applied with same checksum -> not pending.
	same := []Row{{InstalledRank: 1, Version: "", Description: "refresh views", Checksum: ptr(100), Success: true}}
	if got := Pending(migs, checksums, same); len(got) != 0 {
		t.Errorf("unchanged repeatable should not be pending, got %v", scripts(got))
	}
	// Applied with different checksum -> pending again.
	changed := []Row{{InstalledRank: 1, Version: "", Description: "refresh views", Checksum: ptr(99), Success: true}}
	if got := Pending(migs, checksums, changed); len(got) != 1 {
		t.Errorf("changed repeatable should be pending, got %v", scripts(got))
	}
	// Last successful application stored a NULL checksum -> re-apply (no nil deref).
	nullCk := []Row{{InstalledRank: 1, Version: "", Description: "refresh views", Checksum: nil, Success: true}}
	if got := Pending(migs, checksums, nullCk); len(got) != 1 {
		t.Errorf("repeatable with NULL stored checksum should be pending, got %v", scripts(got))
	}
}

func TestValidateChecksumMismatch(t *testing.T) {
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 555}
	rows := []Row{{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Checksum: ptr(444), Success: true}}

	problems := Validate(migs, checksums, rows)
	if len(problems) != 1 || problems[0].Kind != ChecksumMismatch {
		t.Fatalf("expected one ChecksumMismatch, got %v", problems)
	}
	if problems[0].Script != "V1__init.sql" {
		t.Errorf("mismatch should name the script, got %q", problems[0].Script)
	}
	// Detail reports history-vs-local in that order (444 history, 555 local).
	if !strings.Contains(problems[0].Detail, "444") || !strings.Contains(problems[0].Detail, "555") {
		t.Errorf("detail should report both checksums: %q", problems[0].Detail)
	}
}

func TestValidateMatchesAcrossVersionFormatting(t *testing.T) {
	// History stored "1.0"; on-disk version "1". The canonical join must resolve
	// them so no phantom MissingMigration is raised.
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 7}
	rows := []Row{{InstalledRank: 1, Version: "1.0", Script: "V1.0__init.sql", Checksum: ptr(7), Success: true}}
	if problems := Validate(migs, checksums, rows); len(problems) != 0 {
		t.Fatalf("expected clean validate across 1 == 1.0, got %v", problems)
	}
}

func TestValidateNullChecksum(t *testing.T) {
	// A successful versioned row with a NULL stored checksum is accepted (no
	// comparison, no panic) — locks in the documented nil-skip behavior.
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 7}
	rows := []Row{{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Checksum: nil, Success: true}}
	if problems := Validate(migs, checksums, rows); len(problems) != 0 {
		t.Fatalf("NULL history checksum should be accepted, got %v", problems)
	}
}

func TestValidateRepeatableUnresolved(t *testing.T) {
	// A successful repeatable row whose description has no local migration is an
	// unresolved-applied error; but a resolvable repeatable with a DIFFERENT
	// checksum is fine (Flyway re-applies on change, validate does not fail).
	local := []scan.Migration{repeatable("kept view", "R__kept_view.sql")}
	checksums := map[string]int32{"R__kept_view.sql": 1}
	rows := []Row{
		{InstalledRank: 1, Version: "", Description: "kept view", Script: "R__kept_view.sql", Checksum: ptr(999), Success: true},     // differing checksum, resolvable -> OK
		{InstalledRank: 2, Version: "", Description: "deleted view", Script: "R__deleted_view.sql", Checksum: ptr(5), Success: true}, // unresolved
	}
	problems := Validate(local, checksums, rows)
	if len(problems) != 1 || problems[0].Kind != MissingMigration || problems[0].Script != "R__deleted_view.sql" {
		t.Fatalf("expected one MissingMigration for the deleted repeatable, got %v", problems)
	}
}

func TestValidateSkipsBaselineRow(t *testing.T) {
	// The baseline marker has a version but no file on disk; it must not be
	// reported as a missing migration.
	rows := []Row{{InstalledRank: 1, Version: "1", Type: TypeBaseline, Script: "<< Flyway Baseline >>", Success: true}}
	if problems := Validate(nil, nil, rows); len(problems) != 0 {
		t.Fatalf("baseline row should be skipped by validate, got %v", problems)
	}
}

func TestValidateMissingMigration(t *testing.T) {
	// History has V2 applied, but no V2 file on disk.
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 1}
	rows := []Row{
		{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Checksum: ptr(1), Success: true},
		{InstalledRank: 2, Version: "2", Script: "V2__gone.sql", Checksum: ptr(2), Success: true},
	}
	problems := Validate(migs, checksums, rows)
	if len(problems) != 1 || problems[0].Kind != MissingMigration || problems[0].Script != "V2__gone.sql" {
		t.Fatalf("expected one MissingMigration for V2, got %v", problems)
	}
}

func TestValidateFailedMigration(t *testing.T) {
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 1}
	rows := []Row{{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Checksum: ptr(1), Success: false}}
	problems := Validate(migs, checksums, rows)
	if len(problems) != 1 || problems[0].Kind != FailedMigration || problems[0].Script != "V1__init.sql" {
		t.Fatalf("expected one FailedMigration for V1__init.sql, got %v", problems)
	}
}

func TestProblemString(t *testing.T) {
	p := Problem{Kind: ChecksumMismatch, Script: "V1__init.sql", Detail: "history 1 != local 2"}
	s := p.String()
	for _, frag := range []string{string(ChecksumMismatch), "V1__init.sql", "history 1 != local 2"} {
		if !strings.Contains(s, frag) {
			t.Errorf("Problem.String() %q missing %q", s, frag)
		}
	}
}

func TestValidateUnparseableVersion(t *testing.T) {
	// A successful row whose stored version Rian cannot parse must be reported,
	// not silently skipped (which would suppress the checksum comparison).
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 1}
	rows := []Row{{InstalledRank: 1, Version: "not.a.number", Script: "Vbad.sql", Checksum: ptr(1), Success: true}}
	problems := Validate(migs, checksums, rows)
	if len(problems) != 1 || problems[0].Kind != MissingMigration {
		t.Fatalf("expected one problem for unparseable version, got %v", problems)
	}
}

func TestValidateClean(t *testing.T) {
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 7}
	rows := []Row{{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Checksum: ptr(7), Success: true}}
	if problems := Validate(migs, checksums, rows); len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
}

func scripts(ms []scan.Migration) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Script
	}
	return out
}

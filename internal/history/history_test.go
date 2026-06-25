package history

import (
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
}

func TestValidateChecksumMismatch(t *testing.T) {
	migs := []scan.Migration{versioned(t, "1", "init", "V1__init.sql")}
	checksums := map[string]int32{"V1__init.sql": 555}
	rows := []Row{{InstalledRank: 1, Version: "1", Script: "V1__init.sql", Checksum: ptr(444), Success: true}}

	problems := Validate(migs, checksums, rows)
	if len(problems) != 1 || problems[0].Kind != ChecksumMismatch {
		t.Fatalf("expected one ChecksumMismatch, got %v", problems)
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
	if len(problems) != 1 || problems[0].Kind != FailedMigration {
		t.Fatalf("expected one FailedMigration, got %v", problems)
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

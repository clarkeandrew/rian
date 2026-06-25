package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/clarkeandrew/rian/internal/checksum"
	"github.com/clarkeandrew/rian/internal/config"
	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/history"
)

// fakeConn is an in-memory db.Conn that simulates a transactional database:
// ApplyMigration records a success row, unless the script is configured to fail
// (in which case no row persists, mirroring a rolled-back transaction).
type fakeConn struct {
	rows       []history.Row
	failScript string
	applied    []string
}

func (f *fakeConn) Dialect() db.Dialect                         { return nil }
func (f *fakeConn) EnsureHistory(context.Context, string) error { return nil }
func (f *fakeConn) ReadHistory(context.Context, string) ([]history.Row, error) {
	return append([]history.Row(nil), f.rows...), nil
}
func (f *fakeConn) ApplyMigration(_ context.Context, _ string, _ []string, row history.Row) error {
	if row.Script == f.failScript {
		return errors.New("simulated failure")
	}
	row.Success = true
	f.rows = append(f.rows, row)
	f.applied = append(f.applied, row.Script)
	return nil
}
func (f *fakeConn) InsertHistory(_ context.Context, _ string, row history.Row) error {
	f.rows = append(f.rows, row)
	return nil
}
func (f *fakeConn) DeleteFailed(context.Context, string) (int, error) {
	var kept []history.Row
	n := 0
	for _, r := range f.rows {
		if r.Success {
			kept = append(kept, r)
		} else {
			n++
		}
	}
	f.rows = kept
	return n, nil
}
func (f *fakeConn) Close(context.Context) error { return nil }

func migrationsDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func testConfig(dir string) config.Config {
	cfg := config.Default()
	cfg.Locations = []string{dir}
	cfg.User = "tester"
	return cfg
}

func TestMigrateAppliesPendingInOrder(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
	})
	conn := &fakeConn{}
	eng := New(conn, testConfig(dir))

	res, err := eng.Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 2 || res.Applied[0].Script != "V1__a.sql" || res.Applied[1].Script != "V2__b.sql" {
		t.Fatalf("applied = %v, want V1 then V2", res.Applied)
	}
	if conn.rows[0].InstalledRank != 1 || conn.rows[1].InstalledRank != 2 {
		t.Errorf("ranks = %d,%d want 1,2", conn.rows[0].InstalledRank, conn.rows[1].InstalledRank)
	}
	if conn.rows[0].Version != "1" || conn.rows[0].Type != "SQL" || !conn.rows[0].Success {
		t.Errorf("unexpected row[0]: %+v", conn.rows[0])
	}

	// Running again applies nothing (idempotent).
	res2, err := eng.Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Applied) != 0 {
		t.Errorf("second migrate applied %v, want none", res2.Applied)
	}
}

func TestMigrateStopsAtFirstFailure(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "BROKEN;",
		"V3__c.sql": "CREATE TABLE c (id int);",
	})
	conn := &fakeConn{failScript: "V2__b.sql"}
	eng := New(conn, testConfig(dir))

	res, err := eng.Migrate(context.Background())
	if err == nil {
		t.Fatal("expected error from failing migration")
	}
	if len(res.Applied) != 1 || res.Applied[0].Script != "V1__a.sql" {
		t.Errorf("applied = %v, want only V1 before failure", res.Applied)
	}
	// V3 must not have been attempted.
	for _, s := range conn.applied {
		if s == "V3__c.sql" {
			t.Error("V3 should not run after V2 failed")
		}
	}
}

func TestMigrateSubstitutesPlaceholders(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE ${prefix}_users (id int);",
	})
	cfg := testConfig(dir)
	cfg.Placeholders = map[string]string{"prefix": "app"}
	eng := New(&fakeConn{}, cfg)
	if _, err := eng.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate with placeholder failed: %v", err)
	}

	// An unresolved placeholder must fail the migration.
	dir2 := migrationsDir(t, map[string]string{"V1__a.sql": "SELECT ${missing};"})
	eng2 := New(&fakeConn{}, testConfig(dir2))
	if _, err := eng2.Migrate(context.Background()); err == nil {
		t.Error("expected error for unresolved placeholder")
	}
}

func TestInfoReportsStatuses(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
	})
	conn := &fakeConn{}
	eng := New(conn, testConfig(dir))

	// Apply V1 only by pre-seeding history with its checksum.
	ck := checksum.CalculateBytes([]byte("CREATE TABLE a (id int);"))
	conn.rows = []history.Row{{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Checksum: &ck, Success: true}}

	entries, err := eng.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Status != "Applied" || entries[1].Status != "Pending" {
		t.Errorf("statuses = %q,%q want Applied,Pending", entries[0].Status, entries[1].Status)
	}
}

func TestValidateDetectsChecksumMismatch(t *testing.T) {
	dir := migrationsDir(t, map[string]string{"V1__a.sql": "CREATE TABLE a (id int);"})
	wrong := int32(12345)
	conn := &fakeConn{rows: []history.Row{
		{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Checksum: &wrong, Success: true},
	}}
	eng := New(conn, testConfig(dir))

	problems, err := eng.Validate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Kind != history.ChecksumMismatch {
		t.Fatalf("expected one checksum mismatch, got %v", problems)
	}
}

func TestBaseline(t *testing.T) {
	dir := migrationsDir(t, map[string]string{})
	conn := &fakeConn{}
	cfg := testConfig(dir)
	cfg.BaselineVersion = "1"
	eng := New(conn, cfg)

	if err := eng.Baseline(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(conn.rows) != 1 || conn.rows[0].Version != "1" || conn.rows[0].Type != "BASELINE" {
		t.Fatalf("unexpected baseline row: %+v", conn.rows)
	}
	// Baselining a non-empty history is an error.
	if err := eng.Baseline(context.Background()); err == nil {
		t.Error("expected error baselining a non-empty history")
	}
}

func TestRepairRemovesFailedRows(t *testing.T) {
	dir := migrationsDir(t, map[string]string{})
	conn := &fakeConn{rows: []history.Row{
		{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Success: true},
		{InstalledRank: 2, Version: "2", Script: "V2__b.sql", Success: false},
	}}
	eng := New(conn, testConfig(dir))

	n, err := eng.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("repaired %d, want 1", n)
	}
	if len(conn.rows) != 1 || !conn.rows[0].Success {
		t.Errorf("failed row should be gone, rows = %+v", conn.rows)
	}
}

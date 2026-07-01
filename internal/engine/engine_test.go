package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	// nonTransactional models a MySQL-style dialect: a failed migration records a
	// success=false row (requires repair) instead of rolling back cleanly.
	nonTransactional bool
	// ensureErr/readErr force errors from EnsureHistory/ReadHistory.
	ensureErr error
	readErr   error

	applied    []string
	statements [][]string // statements passed to each successful ApplyMigration
}

func (f *fakeConn) Dialect() db.Dialect                         { return nil }
func (f *fakeConn) EnsureHistory(context.Context, string) error { return f.ensureErr }
func (f *fakeConn) ReadHistory(context.Context, string) ([]history.Row, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return append([]history.Row(nil), f.rows...), nil
}
func (f *fakeConn) ApplyMigration(_ context.Context, _ string, stmts []string, row history.Row) error {
	if row.Script == f.failScript {
		if f.nonTransactional {
			row.Success = false
			f.rows = append(f.rows, row)
		}
		return errors.New("simulated failure")
	}
	f.statements = append(f.statements, append([]string(nil), stmts...))
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
	conn := &fakeConn{}
	eng := New(conn, cfg)
	if _, err := eng.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate with placeholder failed: %v", err)
	}
	// The produced SQL must have the placeholder resolved (value-level check).
	want := []string{"CREATE TABLE app_users (id int)"}
	if len(conn.statements) != 1 || !reflect.DeepEqual(conn.statements[0], want) {
		t.Errorf("produced statements = %#v, want %#v", conn.statements, want)
	}

	// An unresolved placeholder must fail the migration.
	dir2 := migrationsDir(t, map[string]string{"V1__a.sql": "SELECT ${missing};"})
	eng2 := New(&fakeConn{}, testConfig(dir2))
	if _, err := eng2.Migrate(context.Background()); err == nil {
		t.Error("expected error for unresolved placeholder")
	}
}

func TestMigrateSplitsMultipleStatements(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);\nCREATE TABLE b (id int);",
	})
	conn := &fakeConn{}
	if _, err := New(conn, testConfig(dir)).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"CREATE TABLE a (id int)", "CREATE TABLE b (id int)"}
	if len(conn.statements) != 1 || !reflect.DeepEqual(conn.statements[0], want) {
		t.Errorf("statements = %#v, want one migration with %#v", conn.statements, want)
	}
}

func TestMigrateContinuesInstalledRank(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
	})
	// History already has V1 applied at a non-contiguous rank 5.
	ck := checksum.CalculateBytes([]byte("CREATE TABLE a (id int);"))
	conn := &fakeConn{rows: []history.Row{
		{InstalledRank: 5, Version: "1", Script: "V1__a.sql", Checksum: &ck, Success: true},
	}}
	res, err := New(conn, testConfig(dir)).Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Script != "V2__b.sql" {
		t.Fatalf("applied = %v, want only V2", res.Applied)
	}
	// New row's rank must continue from max(existing)+1 = 6, not 1 or len+1.
	last := conn.rows[len(conn.rows)-1]
	if last.InstalledRank != 6 {
		t.Errorf("new rank = %d, want 6 (continues from existing max)", last.InstalledRank)
	}
}

func TestMigrateRepeatable(t *testing.T) {
	body := "CREATE OR REPLACE VIEW v AS SELECT 1;"
	dir := migrationsDir(t, map[string]string{"R__v.sql": body})
	ck := checksum.CalculateBytes([]byte(body))

	// (a) From empty history: applied with empty Version and a non-nil checksum.
	conn := &fakeConn{}
	res, err := New(conn, testConfig(dir)).Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Script != "R__v.sql" {
		t.Fatalf("applied = %v, want R__v.sql", res.Applied)
	}
	row := conn.rows[0]
	if row.Version != "" || row.Checksum == nil || *row.Checksum != ck {
		t.Errorf("repeatable row = %+v, want empty version and checksum %d", row, ck)
	}

	// (b) Unchanged checksum -> not re-applied.
	seeded := &fakeConn{rows: []history.Row{
		{InstalledRank: 1, Version: "", Description: "v", Script: "R__v.sql", Checksum: &ck, Success: true},
	}}
	if res, _ := New(seeded, testConfig(dir)).Migrate(context.Background()); len(res.Applied) != 0 {
		t.Errorf("unchanged repeatable re-applied: %v", res.Applied)
	}

	// (c) Changed checksum -> re-applied at the next rank.
	old := int32(999)
	changed := &fakeConn{rows: []history.Row{
		{InstalledRank: 3, Version: "", Description: "v", Script: "R__v.sql", Checksum: &old, Success: true},
	}}
	res, err = New(changed, testConfig(dir)).Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 {
		t.Fatalf("changed repeatable should re-apply, got %v", res.Applied)
	}
	if last := changed.rows[len(changed.rows)-1]; last.InstalledRank != 4 {
		t.Errorf("re-applied rank = %d, want 4", last.InstalledRank)
	}
}

func TestMigrateMixedVersionedAndRepeatableOrder(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
		"R__z.sql":  "CREATE OR REPLACE VIEW z AS SELECT 1;",
	})
	conn := &fakeConn{}
	res, err := New(conn, testConfig(dir)).Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := []string{res.Applied[0].Script, res.Applied[1].Script, res.Applied[2].Script}
	want := []string{"V1__a.sql", "V2__b.sql", "R__z.sql"}
	if !reflect.DeepEqual(gotOrder, want) {
		t.Errorf("apply order = %v, want %v (repeatable last)", gotOrder, want)
	}
	for i, wantRank := range []int{1, 2, 3} {
		if conn.rows[i].InstalledRank != wantRank {
			t.Errorf("row %d rank = %d, want %d", i, conn.rows[i].InstalledRank, wantRank)
		}
	}
}

func TestMigrateNonTransactionalFailureThenRepair(t *testing.T) {
	// MySQL-style: a failed migration leaves a success=false row that validate
	// reports and repair clears, after which a fixed migration can be re-run.
	dir := migrationsDir(t, map[string]string{"V1__a.sql": "BROKEN;"})
	conn := &fakeConn{nonTransactional: true, failScript: "V1__a.sql"}
	eng := New(conn, testConfig(dir))

	if _, err := eng.Migrate(context.Background()); err == nil {
		t.Fatal("expected migration failure")
	}
	if len(conn.rows) != 1 || conn.rows[0].Success {
		t.Fatalf("expected one success=false row, got %+v", conn.rows)
	}
	problems, _ := eng.Validate(context.Background())
	if len(problems) != 1 || problems[0].Kind != history.FailedMigration {
		t.Fatalf("validate should report the failed migration, got %v", problems)
	}
	// While the failed row is present, migrate refuses (validate-first) — repair
	// is genuinely required.
	conn.failScript = ""
	if _, err := eng.Migrate(context.Background()); err == nil {
		t.Fatal("migrate should refuse while a failed row is present")
	}
	if n, _ := eng.Repair(context.Background()); n != 1 {
		t.Fatalf("repair should remove 1 failed row")
	}
	// After repair the (now non-failing) migration applies cleanly.
	res, err := eng.Migrate(context.Background())
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("re-migrate after repair: applied=%v err=%v", res.Applied, err)
	}
}

func TestMigrateHonorsTarget(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
		"V3__c.sql": "CREATE TABLE c (id int);",
		"R__v.sql":  "CREATE OR REPLACE VIEW v AS SELECT 1;",
	})
	conn := &fakeConn{}
	cfg := testConfig(dir)
	cfg.Target = "2"
	eng := New(conn, cfg)

	res, err := eng.Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// V1 and V2 apply; V3 is above the target; the repeatable is unaffected.
	got := []string{}
	for _, m := range res.Applied {
		got = append(got, m.Script)
	}
	want := []string{"V1__a.sql", "V2__b.sql", "R__v.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}

	entries, err := eng.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Script == "V3__c.sql" && e.Status != "Above Target" {
			t.Errorf("V3 status = %q, want Above Target", e.Status)
		}
	}

	// An unparseable target is an error, not a silent no-limit.
	cfg.Target = "not-a-version"
	if _, err := New(conn, cfg).Migrate(context.Background()); err == nil {
		t.Error("expected error for unparseable target")
	}
}

func TestMigrateValidatesFirst(t *testing.T) {
	// A drifted applied migration (checksum mismatch) must block migrate before
	// anything is applied — Flyway's validateOnMigrate default.
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
	})
	wrong := int32(12345)
	conn := &fakeConn{rows: []history.Row{
		{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Checksum: &wrong, Success: true},
	}}
	_, err := New(conn, testConfig(dir)).Migrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation failure before migrate, got %v", err)
	}
	if len(conn.applied) != 0 {
		t.Errorf("nothing should be applied on a drifted history, applied %v", conn.applied)
	}

	// With validateOnMigrate disabled, the drifted history is tolerated and the
	// pending migration applies.
	cfg := testConfig(dir)
	cfg.ValidateOnMigrate = false
	res, err := New(conn, cfg).Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Script != "V2__b.sql" {
		t.Fatalf("applied = %v, want V2", res.Applied)
	}
}

func TestMigrateRefusesOutOfOrder(t *testing.T) {
	// V2 is applied; a newly-added V1 below it must be refused, matching
	// Flyway's outOfOrder=false default.
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
	})
	ck := checksum.CalculateBytes([]byte("CREATE TABLE b (id int);"))
	conn := &fakeConn{rows: []history.Row{
		{InstalledRank: 1, Version: "2", Script: "V2__b.sql", Checksum: &ck, Success: true},
	}}
	_, err := New(conn, testConfig(dir)).Migrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "outOfOrder") {
		t.Fatalf("expected out-of-order refusal, got %v", err)
	}
	if len(conn.applied) != 0 {
		t.Errorf("nothing should be applied, applied %v", conn.applied)
	}

	// With outOfOrder enabled, the older migration applies.
	cfg := testConfig(dir)
	cfg.OutOfOrder = true
	res, err := New(conn, cfg).Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Script != "V1__a.sql" {
		t.Fatalf("applied = %v, want V1 (out of order)", res.Applied)
	}
}

func TestBaselineThenMigrateSkipsBelowBaseline(t *testing.T) {
	dir := migrationsDir(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id int);",
		"V2__b.sql": "CREATE TABLE b (id int);",
		"V3__c.sql": "CREATE TABLE c (id int);",
	})
	conn := &fakeConn{}
	cfg := testConfig(dir)
	cfg.BaselineVersion = "2"
	eng := New(conn, cfg)

	if err := eng.Baseline(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := eng.Migrate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Script != "V3__c.sql" {
		t.Fatalf("applied = %v, want only V3 (V1/V2 are at or below the baseline)", res.Applied)
	}
	entries, err := eng.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantStatus := map[string]string{
		"V1__a.sql": "Below Baseline",
		"V2__b.sql": "Applied", // at the baseline version: recorded by the baseline row
		"V3__c.sql": "Applied",
	}
	for _, e := range entries {
		if e.Status != wantStatus[e.Script] {
			t.Errorf("info %s status = %q, want %q", e.Script, e.Status, wantStatus[e.Script])
		}
	}
}

func TestInstalledByOverride(t *testing.T) {
	dir := migrationsDir(t, map[string]string{"V1__a.sql": "CREATE TABLE a (id int);"})
	conn := &fakeConn{}
	cfg := testConfig(dir) // User = "tester"
	cfg.InstalledBy = "deploy-bot"
	if _, err := New(conn, cfg).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := conn.rows[0].InstalledBy; got != "deploy-bot" {
		t.Errorf("installed_by = %q, want configured override deploy-bot", got)
	}
}

func TestBaselineRejectsInvalidVersion(t *testing.T) {
	cfg := testConfig(migrationsDir(t, nil))
	cfg.BaselineVersion = "not-a-version"
	if err := New(&fakeConn{}, cfg).Baseline(context.Background()); err == nil {
		t.Fatal("expected error for invalid baseline version")
	}
}

func TestValidateCleanAndKinds(t *testing.T) {
	dir := migrationsDir(t, map[string]string{"V1__a.sql": "CREATE TABLE a (id int);"})
	ck := checksum.CalculateBytes([]byte("CREATE TABLE a (id int);"))

	// Clean: matching checksum -> no problems.
	clean := &fakeConn{rows: []history.Row{{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Checksum: &ck, Success: true}}}
	if p, _ := New(clean, testConfig(dir)).Validate(context.Background()); len(p) != 0 {
		t.Errorf("expected clean validate, got %v", p)
	}

	// Missing: applied V9 with no file -> MissingMigration.
	other := int32(1)
	missing := &fakeConn{rows: []history.Row{
		{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Checksum: &ck, Success: true},
		{InstalledRank: 2, Version: "9", Script: "V9__gone.sql", Checksum: &other, Success: true},
	}}
	if p, _ := New(missing, testConfig(dir)).Validate(context.Background()); len(p) != 1 || p[0].Kind != history.MissingMigration {
		t.Errorf("expected MissingMigration, got %v", p)
	}
}

func TestCommandsPropagateEnsureHistoryError(t *testing.T) {
	dir := migrationsDir(t, map[string]string{})
	boom := errors.New("ensure boom")
	mk := func() *Engine { return New(&fakeConn{ensureErr: boom}, testConfig(dir)) }
	ctx := context.Background()

	if _, err := mk().Migrate(ctx); err == nil {
		t.Error("Migrate should surface EnsureHistory error")
	}
	if _, err := mk().Info(ctx); err == nil {
		t.Error("Info should surface EnsureHistory error")
	}
	if _, err := mk().Validate(ctx); err == nil {
		t.Error("Validate should surface EnsureHistory error")
	}
	if err := mk().Baseline(ctx); err == nil {
		t.Error("Baseline should surface EnsureHistory error")
	}
	if _, err := mk().Repair(ctx); err == nil {
		t.Error("Repair should surface EnsureHistory error")
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

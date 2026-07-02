//go:build e2e

// Package e2e contains end-to-end tests that run Rian against real Postgres and
// MySQL databases. They are gated behind the `e2e` build tag and skipped unless
// the corresponding URL env vars are set:
//
//	RIAN_E2E_POSTGRES_URL  e.g. jdbc:postgresql://localhost:5432/app
//	RIAN_E2E_MYSQL_URL     e.g. jdbc:mysql://localhost:3306/app
//
// Bring databases up with `docker compose up -d`, then:
//
//	RIAN_E2E_POSTGRES_URL=... RIAN_E2E_MYSQL_URL=... go test -tags e2e ./test/e2e/...
//
// The suite must run against FRESH databases (the e2e `go-roundtrip` CI job
// uses dedicated service containers): each scenario creates its own schema
// objects and history table and would collide with leftovers from a prior run.
//
// The real-Flyway -> Rian drop-in handoff is exercised by the e2e GitHub
// Actions workflow; this Go suite covers every engine command (migrate, info,
// validate, baseline, repair), repeatable re-application, per-dialect failure
// semantics, and the migration lock against the real drivers.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clarkeandrew/rian/internal/config"
	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/db/mysql"
	"github.com/clarkeandrew/rian/internal/db/postgres"
	"github.com/clarkeandrew/rian/internal/engine"
	"github.com/clarkeandrew/rian/internal/history"
)

func TestPostgres(t *testing.T) {
	url := os.Getenv("RIAN_E2E_POSTGRES_URL")
	if url == "" {
		t.Skip("RIAN_E2E_POSTGRES_URL not set")
	}
	suite(t, "postgres", url)
}

func TestMySQL(t *testing.T) {
	url := os.Getenv("RIAN_E2E_MYSQL_URL")
	if url == "" {
		t.Skip("RIAN_E2E_MYSQL_URL not set")
	}
	suite(t, "mysql", url)
}

// suite runs every scenario against one database kind. Each scenario uses its
// own migrations directory, uniquely named schema objects, and its own history
// table, so the scenarios are independent on a shared (fresh) database.
func suite(t *testing.T, kind, url string) {
	t.Run("RoundTrip", func(t *testing.T) { roundTrip(t, kind, url) })
	t.Run("RepeatableReapply", func(t *testing.T) { repeatableReapply(t, kind, url) })
	t.Run("Baseline", func(t *testing.T) { baselineFlow(t, kind, url) })
	t.Run("RepairChecksum", func(t *testing.T) { repairChecksum(t, kind, url) })
	t.Run("FailedMigration", func(t *testing.T) { failedMigration(t, kind, url) })
	t.Run("LockExcludesConcurrentRuns", func(t *testing.T) { lockExcludes(t, kind, url) })
}

func connect(t *testing.T, ctx context.Context, kind, url string) db.Conn {
	t.Helper()
	var (
		conn db.Conn
		err  error
	)
	switch kind {
	case "postgres":
		conn, err = postgres.Connect(ctx, url, "rian", "rian")
	case "mysql":
		conn, err = mysql.Connect(ctx, url, "rian", "rian")
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	if err != nil {
		t.Fatalf("connect %s: %v", kind, err)
	}
	return conn
}

// newEngine writes the given migrations to a temp dir and returns an engine
// using that dir and the given history table. The dir is returned so scenarios
// can edit migrations between runs.
func newEngine(t *testing.T, conn db.Conn, table string, files map[string]string) (*engine.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		writeFile(t, dir, name, body)
	}
	cfg := config.Default()
	cfg.Locations = []string{dir}
	cfg.Table = table
	cfg.User = "rian"
	return engine.New(conn, cfg), dir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func migrateN(t *testing.T, eng *engine.Engine, want int) {
	t.Helper()
	res, err := eng.Migrate(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Applied) != want {
		t.Fatalf("migrate applied %d, want %d (%v)", len(res.Applied), want, res.Applied)
	}
}

func validateClean(t *testing.T, eng *engine.Engine) {
	t.Helper()
	problems, err := eng.Validate(context.Background())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected clean validate, got %v", problems)
	}
}

// roundTrip covers the primary flow against the shared test/e2e/sql migrations
// (three versioned + one repeatable): migrate everything, validate, no-op
// re-migrate, and info reporting all applied.
func roundTrip(t *testing.T, kind, url string) {
	ctx := context.Background()
	conn := connect(t, ctx, kind, url)
	defer conn.Close(ctx)

	cfg := config.Default()
	cfg.Locations = []string{"filesystem:sql"}
	cfg.User = "rian"
	cfg.Table = "rian_e2e_history"
	eng := engine.New(conn, cfg)

	res, err := eng.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Applied) != 4 {
		t.Fatalf("expected 4 migrations applied (V1..V3 + repeatable), got %d", len(res.Applied))
	}
	validateClean(t, eng)

	// Migrate again is a no-op (repeatable checksum unchanged).
	migrateN(t, eng, 0)

	entries, err := eng.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	applied := 0
	for _, e := range entries {
		if e.Status == "Applied" {
			applied++
		}
	}
	if applied != 4 {
		t.Fatalf("info: %d applied, want 4", applied)
	}
}

// repeatableReapply proves the repeatable lifecycle on a real database: applied
// once, skipped while unchanged, re-applied (at the next rank) when its content
// — and therefore checksum — changes.
func repeatableReapply(t *testing.T, kind, url string) {
	ctx := context.Background()
	conn := connect(t, ctx, kind, url)
	defer conn.Close(ctx)

	eng, dir := newEngine(t, conn, "rian_e2e_repeat", map[string]string{
		"V1__items.sql":     "CREATE TABLE rpt_items (id INTEGER PRIMARY KEY, label VARCHAR(50));",
		"R__items_view.sql": "CREATE OR REPLACE VIEW rpt_items_view AS SELECT id FROM rpt_items;",
	})
	migrateN(t, eng, 2)
	migrateN(t, eng, 0)

	// Changing the repeatable's content re-applies it.
	writeFile(t, dir, "R__items_view.sql",
		"CREATE OR REPLACE VIEW rpt_items_view AS SELECT id, label FROM rpt_items;")
	migrateN(t, eng, 1)
	validateClean(t, eng)

	rows, err := conn.ReadHistory(ctx, "rian_e2e_repeat")
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	last := rows[len(rows)-1]
	if last.Version != "" || last.InstalledRank != 3 || !last.Success {
		t.Fatalf("re-applied repeatable row = %+v, want empty version at rank 3", last)
	}
}

// baselineFlow covers baseline against a real database: the baseline row is
// recorded, migrations at or below it are skipped (their DDL never runs), and
// validate accepts the file-less baseline marker.
func baselineFlow(t *testing.T, kind, url string) {
	ctx := context.Background()
	conn := connect(t, ctx, kind, url)
	defer conn.Close(ctx)

	eng, _ := newEngine(t, conn, "rian_e2e_baseline", map[string]string{
		"V1__one.sql":   "CREATE TABLE bl_one (id INTEGER);",
		"V2__two.sql":   "CREATE TABLE bl_two (id INTEGER);",
		"V3__three.sql": "CREATE TABLE bl_three (id INTEGER);",
	})
	eng.Cfg.BaselineVersion = "2"

	if err := eng.Baseline(ctx); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Only V3 applies: V1 is below the baseline, V2 is at it.
	migrateN(t, eng, 1)
	validateClean(t, eng)

	entries, err := eng.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	wantStatus := map[string]string{
		"V1__one.sql":   "Below Baseline",
		"V2__two.sql":   "Applied", // at the baseline version: recorded by the baseline row
		"V3__three.sql": "Applied",
	}
	for _, e := range entries {
		if e.Status != wantStatus[e.Script] {
			t.Errorf("info %s status = %q, want %q", e.Script, e.Status, wantStatus[e.Script])
		}
	}

	// Baselining a non-empty history must fail.
	if err := eng.Baseline(ctx); err == nil {
		t.Fatal("expected error baselining a non-empty history")
	}
}

// repairChecksum covers repair's checksum realignment (the UpdateChecksum SQL)
// against a real database: an edited applied migration blocks migrate and fails
// validate until repair realigns the stored checksum.
func repairChecksum(t *testing.T, kind, url string) {
	ctx := context.Background()
	conn := connect(t, ctx, kind, url)
	defer conn.Close(ctx)

	eng, dir := newEngine(t, conn, "rian_e2e_repair", map[string]string{
		"V1__one.sql": "CREATE TABLE rp_one (id INTEGER);",
	})
	migrateN(t, eng, 1)

	// Edit the applied migration: validate must flag the drift and migrate must
	// refuse (validateOnMigrate).
	writeFile(t, dir, "V1__one.sql", "CREATE TABLE rp_one (id INTEGER); -- edited")
	problems, err := eng.Validate(ctx)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(problems) != 1 || problems[0].Kind != history.ChecksumMismatch {
		t.Fatalf("expected one checksum mismatch, got %v", problems)
	}
	if _, err := eng.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("migrate should refuse a drifted history, got %v", err)
	}

	res, err := eng.Repair(ctx)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.AlignedChecksums != 1 || res.RemovedFailed != 0 {
		t.Fatalf("repair = %+v, want 1 aligned / 0 removed", res)
	}
	validateClean(t, eng)
}

// failedMigration covers the per-dialect failure semantics: Postgres rolls the
// whole migration back (no history row), MySQL records success=false and
// requires repair before migrate works again.
func failedMigration(t *testing.T, kind, url string) {
	ctx := context.Background()
	conn := connect(t, ctx, kind, url)
	defer conn.Close(ctx)

	const table = "rian_e2e_failed"
	eng, dir := newEngine(t, conn, table, map[string]string{
		"V1__broken.sql": "CREATE TABLE rf_one (id INTEGER); THIS IS NOT SQL;",
	})
	if _, err := eng.Migrate(ctx); err == nil {
		t.Fatal("expected migrate to fail on broken SQL")
	}

	if conn.Dialect().SupportsTransactionalDDL() {
		// Postgres: the transaction rolled back — no history row, validate clean.
		rows, err := conn.ReadHistory(ctx, table)
		if err != nil {
			t.Fatalf("read history: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("expected no history rows after rollback, got %+v", rows)
		}
		// Fix the migration and re-run — nothing to repair.
		writeFile(t, dir, "V1__broken.sql", "CREATE TABLE rf_one (id INTEGER);")
	} else {
		// MySQL: a success=false row is recorded; migrate refuses until repair.
		problems, err := eng.Validate(ctx)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if len(problems) != 1 || problems[0].Kind != history.FailedMigration {
			t.Fatalf("expected one failed-migration problem, got %v", problems)
		}
		if _, err := eng.Migrate(ctx); err == nil {
			t.Fatal("migrate should refuse while a failed row is present")
		}
		res, err := eng.Repair(ctx)
		if err != nil {
			t.Fatalf("repair: %v", err)
		}
		if res.RemovedFailed != 1 {
			t.Fatalf("repair removed %d failed rows, want 1", res.RemovedFailed)
		}
		// MySQL implicitly committed the first statement; clean it up in the
		// fixed migration (exactly what an operator would do), then re-run.
		writeFile(t, dir, "V1__broken.sql", "DROP TABLE IF EXISTS rf_one;\nCREATE TABLE rf_one (id INTEGER);")
	}
	migrateN(t, eng, 1)
	validateClean(t, eng)
}

// lockExcludes proves the migration lock excludes a concurrent run across two
// separate connections on both engines.
func lockExcludes(t *testing.T, kind, url string) {
	ctx := context.Background()
	a := connect(t, ctx, kind, url)
	defer a.Close(ctx)
	b := connect(t, ctx, kind, url)
	defer b.Close(ctx)

	const table = "rian_e2e_lock"
	if err := a.Lock(ctx, table); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		if err := b.Lock(ctx, table); err != nil {
			acquired <- err
			return
		}
		acquired <- b.Unlock(ctx, table)
	}()

	// While A holds the lock, B must not get it.
	select {
	case err := <-acquired:
		t.Fatalf("second connection acquired the lock while held (err=%v)", err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := a.Unlock(ctx, table); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second lock/unlock after release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second connection never acquired the lock after release")
	}
}

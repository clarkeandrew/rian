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
// The real-Flyway -> Rian drop-in handoff is exercised by the e2e GitHub
// Actions workflow; this Go suite covers Rian's own round-trip against the
// real drivers.
package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/clarkeandrew/rian/internal/config"
	"github.com/clarkeandrew/rian/internal/db"
	"github.com/clarkeandrew/rian/internal/db/mysql"
	"github.com/clarkeandrew/rian/internal/db/postgres"
	"github.com/clarkeandrew/rian/internal/engine"
)

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

func runRoundTrip(t *testing.T, kind, url string) {
	ctx := context.Background()
	conn := connect(t, ctx, kind, url)
	defer conn.Close(ctx)

	cfg := config.Default()
	cfg.Locations = []string{"filesystem:sql"}
	cfg.User = "rian"
	// Use a per-kind table to keep parallel runs independent.
	cfg.Table = "rian_e2e_history"

	eng := engine.New(conn, cfg)

	// This test expects a fresh database (CI uses throwaway service containers).
	res, err := eng.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Applied) != 3 {
		t.Fatalf("expected 3 migrations applied, got %d", len(res.Applied))
	}

	// Validate must be clean immediately after migrating.
	problems, err := eng.Validate(ctx)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected clean validate, got %v", problems)
	}

	// Migrate again is a no-op.
	res2, err := eng.Migrate(ctx)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(res2.Applied) != 0 {
		t.Fatalf("second migrate applied %d, want 0", len(res2.Applied))
	}

	// Info reports all three as applied.
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
	if applied != 3 {
		t.Fatalf("info: %d applied, want 3", applied)
	}
}

func TestPostgresRoundTrip(t *testing.T) {
	url := os.Getenv("RIAN_E2E_POSTGRES_URL")
	if url == "" {
		t.Skip("RIAN_E2E_POSTGRES_URL not set")
	}
	runRoundTrip(t, "postgres", url)
}

func TestMySQLRoundTrip(t *testing.T) {
	url := os.Getenv("RIAN_E2E_MYSQL_URL")
	if url == "" {
		t.Skip("RIAN_E2E_MYSQL_URL not set")
	}
	runRoundTrip(t, "mysql", url)
}

package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/clarkeandrew/rian/internal/history"
)

// fakeResult is a minimal sql.Result.
type fakeResult struct{ affected int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.affected, nil }

// fakeExec records executed statements and can be configured to fail.
type fakeExec struct {
	failOnStmt string // fail when the query contains this substring
	failAll    bool
	queries    []string
	args       [][]any
}

func (f *fakeExec) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.failAll || (f.failOnStmt != "" && strings.Contains(query, f.failOnStmt)) {
		return nil, errors.New("exec boom")
	}
	return fakeResult{affected: 1}, nil
}

func newTestConn(e executor) *Conn { return &Conn{exec: e, dialect: Dialect{}} }

// insertArgsFromLast returns the args of the last INSERT recorded by f.
func insertArgsFromLast(t *testing.T, f *fakeExec) []any {
	t.Helper()
	for i := len(f.queries) - 1; i >= 0; i-- {
		if strings.HasPrefix(f.queries[i], "INSERT INTO") {
			return f.args[i]
		}
	}
	t.Fatal("no INSERT recorded")
	return nil
}

func TestApplyMigrationSuccess(t *testing.T) {
	f := &fakeExec{}
	row := history.Row{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Type: "SQL"}
	if err := newTestConn(f).ApplyMigration(context.Background(), "h", []string{"CREATE TABLE a", "INSERT VALUES"}, row); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Two statements + one history INSERT were executed.
	if len(f.queries) != 3 {
		t.Fatalf("expected 3 execs, got %d: %v", len(f.queries), f.queries)
	}
	args := insertArgsFromLast(t, f)
	if got := args[8]; got != true { // success column
		t.Errorf("recorded success = %v, want true", got)
	}
}

func TestApplyMigrationFailureRecordsFailedRow(t *testing.T) {
	// MySQL invariant: a failing statement records success=false and returns the
	// original apply error (no rollback).
	f := &fakeExec{failOnStmt: "BADSTMT"}
	row := history.Row{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Type: "SQL"}
	err := newTestConn(f).ApplyMigration(context.Background(), "h", []string{"GOOD", "BADSTMT", "AFTER"}, row)
	if err == nil || !strings.Contains(err.Error(), "apply V1__a.sql") {
		t.Fatalf("expected apply error, got %v", err)
	}
	// "AFTER" must not have run; an INSERT with success=false must have.
	for _, q := range f.queries {
		if q == "AFTER" {
			t.Error("statement after the failing one should not run")
		}
	}
	args := insertArgsFromLast(t, f)
	if got := args[8]; got != false {
		t.Errorf("recorded success = %v, want false", got)
	}
}

func TestApplyMigrationFailurePrefersExecErr(t *testing.T) {
	// When both the statement and the failure-row INSERT error, the original
	// migration error is surfaced (not the insert error).
	f := &fakeExec{failAll: true}
	row := history.Row{InstalledRank: 1, Version: "1", Script: "V1__a.sql", Type: "SQL"}
	err := newTestConn(f).ApplyMigration(context.Background(), "h", []string{"BADSTMT"}, row)
	if err == nil || !strings.Contains(err.Error(), "apply V1__a.sql") {
		t.Fatalf("expected the apply error to win, got %v", err)
	}
}

func TestDeleteFailedPropagatesError(t *testing.T) {
	f := &fakeExec{failAll: true}
	if _, err := newTestConn(f).DeleteFailed(context.Background(), "h"); err == nil {
		t.Error("expected DeleteFailed to surface exec error")
	}
}

func TestInsertArgs(t *testing.T) {
	ck := int32(42)
	// Versioned row with checksum.
	args := insertArgs(history.Row{InstalledRank: 2, Version: "1.2", Description: "d", Type: "SQL", Script: "s", Checksum: &ck, InstalledBy: "u", ExecutionTime: 7, Success: true})
	if len(args) != 9 {
		t.Fatalf("len(args) = %d, want 9", len(args))
	}
	if args[0] != 2 || args[1] != "1.2" || args[2] != "d" || args[3] != "SQL" || args[4] != "s" || args[5] != ck || args[6] != "u" || args[7] != 7 || args[8] != true {
		t.Errorf("positional args wrong: %#v", args)
	}
	// Repeatable row: empty version and nil checksum map to SQL NULL (nil).
	args = insertArgs(history.Row{InstalledRank: 1, Version: "", Checksum: nil})
	if args[1] != nil {
		t.Errorf("empty version should be nil (NULL), got %#v", args[1])
	}
	if args[5] != nil {
		t.Errorf("nil checksum should be nil (NULL), got %#v", args[5])
	}
}

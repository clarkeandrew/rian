package sql

import (
	"reflect"
	"testing"
)

func TestSplitSimple(t *testing.T) {
	got := Split("SELECT 1;\nSELECT 2;\n")
	want := []string{"SELECT 1", "SELECT 2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitTrailingStatementNoSemicolon(t *testing.T) {
	got := Split("SELECT 1;\nSELECT 2")
	want := []string{"SELECT 1", "SELECT 2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitSemicolonInStringLiteral(t *testing.T) {
	got := Split("INSERT INTO t VALUES ('a;b');\nSELECT 1;")
	want := []string{"INSERT INTO t VALUES ('a;b')", "SELECT 1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitDoubledQuoteEscape(t *testing.T) {
	// '' is an escaped quote, so the ; stays inside the literal.
	got := Split("SELECT 'it''s; fine';")
	want := []string{"SELECT 'it''s; fine'"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitSemicolonInComments(t *testing.T) {
	got := Split("SELECT 1; -- trailing; comment\n/* block; comment */ SELECT 2;")
	want := []string{
		"SELECT 1",
		"-- trailing; comment\n/* block; comment */ SELECT 2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitDollarQuotedFunction(t *testing.T) {
	in := `CREATE FUNCTION f() RETURNS int AS $$
BEGIN
  RETURN 1;
END;
$$ LANGUAGE plpgsql;
SELECT f();`
	got := Split(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 statements, got %d: %#v", len(got), got)
	}
	if !contains(got[0], "BEGIN") || !contains(got[0], "$$") {
		t.Errorf("function body not kept intact: %q", got[0])
	}
	if got[1] != "SELECT f()" {
		t.Errorf("second statement = %q", got[1])
	}
}

func TestSplitDollarQuotedWithTag(t *testing.T) {
	in := `SELECT $body$ has ; and $$ inside $body$;
SELECT 2;`
	got := Split(in)
	want := []string{"SELECT $body$ has ; and $$ inside $body$", "SELECT 2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSplitMySQLDelimiter(t *testing.T) {
	in := `DELIMITER $$
CREATE PROCEDURE p()
BEGIN
  SELECT 1;
  SELECT 2;
END$$
DELIMITER ;
SELECT 3;`
	got := Split(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 statements, got %d: %#v", len(got), got)
	}
	if !contains(got[0], "CREATE PROCEDURE") || !contains(got[0], "SELECT 2") {
		t.Errorf("procedure body should be one statement: %q", got[0])
	}
	if got[1] != "SELECT 3" {
		t.Errorf("statement after DELIMITER reset = %q", got[1])
	}
}

func TestSplitEmptyAndWhitespace(t *testing.T) {
	got := Split(";;\n   \n;")
	if len(got) != 0 {
		t.Errorf("expected no statements from empty input, got %#v", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

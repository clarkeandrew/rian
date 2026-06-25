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

func TestSplitBackslashNotEscapeInPlainString(t *testing.T) {
	// Standard SQL (Postgres standard_conforming_strings=on, MySQL default):
	// backslash is NOT an escape in a plain '...' literal, so 'a\' closes at the
	// quote and the following statement is separate.
	got := Split(`INSERT INTO t VALUES ('a\');SELECT 2;`)
	want := []string{`INSERT INTO t VALUES ('a\')`, "SELECT 2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSplitBackslashEscapeInEString(t *testing.T) {
	// In a Postgres E'...' string, backslash DOES escape, so \' does not close
	// the literal and the ; stays inside.
	got := Split(`SELECT E'a\';b';`)
	want := []string{`SELECT E'a\';b'`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSplitCommentIntroducerInsideString(t *testing.T) {
	// '--' and '/*' inside a string literal must not start a comment.
	for _, in := range []string{"SELECT 'a -- b'; SELECT 2;", "SELECT 'a /* b'; SELECT 2;"} {
		got := Split(in)
		if len(got) != 2 {
			t.Errorf("Split(%q): expected 2 statements, got %#v", in, got)
		}
	}
}

func TestSplitBacktickIdentifierWithSemicolon(t *testing.T) {
	got := Split("SELECT `a;b`; SELECT 2;")
	want := []string{"SELECT `a;b`", "SELECT 2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSplitUnterminatedDollarAbsorbsRest(t *testing.T) {
	// An unterminated dollar body absorbs the rest, so ';' is not a boundary.
	got := Split("SELECT $$ x ; y")
	want := []string{"SELECT $$ x ; y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSplitMultiCharDelimiterAndReset(t *testing.T) {
	in := "DELIMITER //\nSELECT 1;\nSELECT 2//\nDELIMITER ;\nSELECT 3;"
	got := Split(in)
	want := []string{"SELECT 1;\nSELECT 2", "SELECT 3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
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
	want := []string{
		"CREATE FUNCTION f() RETURNS int AS $$\nBEGIN\n  RETURN 1;\nEND;\n$$ LANGUAGE plpgsql",
		"SELECT f()",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
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
	want := []string{
		"CREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT 2;\nEND",
		"SELECT 3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestSplitEmptyAndWhitespace(t *testing.T) {
	got := Split(";;\n   \n;")
	if len(got) != 0 {
		t.Errorf("expected no statements from empty input, got %#v", got)
	}
}

package mysql

import (
	"strings"
	"testing"
)

func TestNameAndCapabilities(t *testing.T) {
	d := Dialect{}
	if d.Name() != "mysql" {
		t.Errorf("Name = %q", d.Name())
	}
	// The defining MySQL property: DDL is NOT transactional.
	if d.SupportsTransactionalDDL() {
		t.Error("MySQL must report non-transactional DDL (implicit commit)")
	}
}

func TestQuoteIdentifier(t *testing.T) {
	d := Dialect{}
	if got := d.QuoteIdentifier("flyway_schema_history"); got != "`flyway_schema_history`" {
		t.Errorf("quote = %q", got)
	}
	// Embedded backticks are doubled.
	if got := d.QuoteIdentifier("we`ird"); got != "`we``ird`" {
		t.Errorf("quote with backtick = %q", got)
	}
}

func TestCreateHistoryTableSQL(t *testing.T) {
	sql := Dialect{}.CreateHistoryTableSQL("flyway_schema_history")
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS `flyway_schema_history`") {
		t.Errorf("missing idempotent create: %s", sql)
	}
	// Columns present, in Flyway order, with the right nullability.
	cols := []string{
		"`installed_rank` int NOT NULL",
		"`version` varchar(50),", // nullable
		"`description` varchar(200) NOT NULL",
		"`type` varchar(20) NOT NULL",
		"`script` varchar(1000) NOT NULL",
		"`checksum` int,", // nullable
		"`installed_by` varchar(100) NOT NULL",
		"`installed_on` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP",
		"`execution_time` int NOT NULL",
		"`success` bool NOT NULL",
	}
	last := -1
	for _, frag := range cols {
		idx := strings.Index(sql, frag)
		if idx < 0 {
			t.Errorf("DDL missing %q", frag)
			continue
		}
		if idx < last {
			t.Errorf("column %q out of Flyway order", frag)
		}
		last = idx
	}
	if !strings.Contains(sql, "PRIMARY KEY (`installed_rank`)") {
		t.Errorf("installed_rank must be the primary key: %s", sql)
	}
}

func TestInsertHistorySQL(t *testing.T) {
	sql := Dialect{}.InsertHistorySQL("h")
	wantCols := "(`installed_rank`, `version`, `description`, `type`, `script`, `checksum`, `installed_by`, `execution_time`, `success`)"
	if !strings.Contains(sql, wantCols) {
		t.Errorf("insert column list/order wrong:\n%s", sql)
	}
	if strings.Count(sql, "?") != 9 {
		t.Errorf("expected 9 ? placeholders, got %d in %q", strings.Count(sql, "?"), sql)
	}
	if strings.Contains(sql, "installed_on") {
		t.Errorf("installed_on must use the column default, not appear in INSERT: %q", sql)
	}
}

func TestSelectHistorySQL(t *testing.T) {
	sql := Dialect{}.SelectHistorySQL("h")
	if !strings.Contains(sql, "ORDER BY `installed_rank`") || !strings.Contains(sql, "FROM `h`") {
		t.Errorf("unexpected select: %q", sql)
	}
}

func TestDSN(t *testing.T) {
	tests := []struct {
		name           string
		url, user, pwd string
		want           string
	}{
		{"basic with creds", "jdbc:mysql://localhost:3306/app", "root", "secret", "root:secret@tcp(localhost:3306)/app"},
		{"user only", "jdbc:mysql://db:3306/app", "root", "", "root@tcp(db:3306)/app"},
		{"with query params", "jdbc:mysql://h:3306/app?parseTime=true&tls=skip-verify", "u", "p", "u:p@tcp(h:3306)/app?parseTime=true&tls=skip-verify"},
		{"creds embedded in url", "jdbc:mysql://u:p@h:3306/app", "", "", "u:p@tcp(h:3306)/app"},
		{"explicit overrides embedded", "jdbc:mysql://u:p@h:3306/app", "admin", "x", "admin:x@tcp(h:3306)/app"},
		{"no jdbc prefix", "mysql://h:3306/app", "u", "p", "u:p@tcp(h:3306)/app"},
	}
	for _, tt := range tests {
		got, err := DSN(tt.url, tt.user, tt.pwd)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: DSN = %q, want %q", tt.name, got, tt.want)
		}
	}
}

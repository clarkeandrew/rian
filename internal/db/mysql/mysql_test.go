package mysql

import (
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
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

func TestUpdateChecksumSQL(t *testing.T) {
	sql := Dialect{}.UpdateChecksumSQL("h")
	// Bind order is the Dialect contract: checksum first, installed_rank second.
	if sql != "UPDATE `h` SET `checksum` = ? WHERE `installed_rank` = ?" {
		t.Errorf("unexpected update: %q", sql)
	}
}

// TestDSN round-trips the produced DSN back through the driver's ParseDSN and
// asserts the decoded fields, which is more robust than string equality and
// guarantees the output is actually parseable by go-sql-driver.
func TestDSN(t *testing.T) {
	tests := []struct {
		name           string
		url, user, pwd string
		wantUser       string
		wantPasswd     string
		wantAddr       string
		wantDB         string
		wantParams     map[string]string
	}{
		{"basic with creds", "jdbc:mysql://localhost:3306/app", "root", "secret", "root", "secret", "localhost:3306", "app", nil},
		{"user only", "jdbc:mysql://db:3306/app", "root", "", "root", "", "db:3306", "app", nil},
		// An unknown param is preserved in Params through the round-trip.
		{"custom param", "jdbc:mysql://h:3306/app?appName=rian", "u", "p", "u", "p", "h:3306", "app", map[string]string{"appName": "rian"}},
		{"creds embedded", "jdbc:mysql://u:p@h:3306/app", "", "", "u", "p", "h:3306", "app", nil},
		{"explicit overrides embedded", "jdbc:mysql://u:p@h:3306/app", "admin", "x", "admin", "x", "h:3306", "app", nil},
		{"no jdbc prefix", "mysql://h:3306/app", "u", "p", "u", "p", "h:3306", "app", nil},
		// Port-less host (a common real Flyway form) -> driver appends default :3306.
		{"portless host", "jdbc:mysql://localhost/app", "u", "p", "u", "p", "localhost:3306", "app", nil},
		// Special characters in the password must survive the round-trip.
		{"special-char password", "jdbc:mysql://h:3306/app", "u", "p@:ss", "u", "p@:ss", "h:3306", "app", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn, err := DSN(tt.url, tt.user, tt.pwd)
			if err != nil {
				t.Fatalf("DSN error: %v", err)
			}
			cfg, err := gomysql.ParseDSN(dsn)
			if err != nil {
				t.Fatalf("produced DSN %q not parseable: %v", dsn, err)
			}
			if cfg.User != tt.wantUser || cfg.Passwd != tt.wantPasswd {
				t.Errorf("creds = %q/%q, want %q/%q", cfg.User, cfg.Passwd, tt.wantUser, tt.wantPasswd)
			}
			if cfg.Addr != tt.wantAddr {
				t.Errorf("addr = %q, want %q", cfg.Addr, tt.wantAddr)
			}
			if cfg.DBName != tt.wantDB {
				t.Errorf("db = %q, want %q", cfg.DBName, tt.wantDB)
			}
			for k, v := range tt.wantParams {
				if cfg.Params[k] != v {
					t.Errorf("param %q = %q, want %q", k, cfg.Params[k], v)
				}
			}
		})
	}
}

func TestDSNMissingHostErrors(t *testing.T) {
	if _, err := DSN("jdbc:mysql:///app", "u", "p"); err == nil {
		t.Error("expected error for missing host, got nil")
	}
}

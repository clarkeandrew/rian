package postgres

import (
	"strings"
	"testing"
)

func TestNameAndCapabilities(t *testing.T) {
	d := Dialect{}
	if d.Name() != "postgresql" {
		t.Errorf("Name = %q", d.Name())
	}
	if !d.SupportsTransactionalDDL() {
		t.Error("Postgres must report transactional DDL")
	}
}

func TestQuoteIdentifier(t *testing.T) {
	d := Dialect{}
	if got := d.QuoteIdentifier("flyway_schema_history"); got != `"flyway_schema_history"` {
		t.Errorf("quote = %q", got)
	}
	// Embedded double quotes are doubled to prevent injection/breakage.
	if got := d.QuoteIdentifier(`we"ird`); got != `"we""ird"` {
		t.Errorf("quote with embedded quote = %q", got)
	}
}

func TestCreateHistoryTableSQL(t *testing.T) {
	sql := Dialect{}.CreateHistoryTableSQL("flyway_schema_history")
	// Must be idempotent and contain every Flyway column in order.
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS \"flyway_schema_history\"") {
		t.Errorf("missing idempotent create: %s", sql)
	}
	cols := []string{
		"installed_rank", "version", "description", "type", "script",
		"checksum", "installed_by", "installed_on", "execution_time", "success",
	}
	last := -1
	for _, c := range cols {
		idx := strings.Index(sql, `"`+c+`"`)
		if idx < 0 {
			t.Errorf("column %q missing from DDL", c)
			continue
		}
		if idx < last {
			t.Errorf("column %q out of Flyway order", c)
		}
		last = idx
	}
	if !strings.Contains(sql, `PRIMARY KEY ("installed_rank")`) {
		t.Errorf("installed_rank must be the primary key: %s", sql)
	}
}

func TestInsertHistorySQL(t *testing.T) {
	sql := Dialect{}.InsertHistorySQL("h")
	for _, p := range []string{"$1", "$2", "$3", "$4", "$5", "$6", "$7", "$8", "$9"} {
		if !strings.Contains(sql, p) {
			t.Errorf("missing bind param %s in %q", p, sql)
		}
	}
	if strings.Contains(sql, "$10") {
		t.Errorf("installed_on should use the column default, not a 10th param: %q", sql)
	}
	if !strings.HasPrefix(sql, `INSERT INTO "h"`) {
		t.Errorf("insert target = %q", sql)
	}
}

func TestSelectHistorySQL(t *testing.T) {
	sql := Dialect{}.SelectHistorySQL("h")
	if !strings.Contains(sql, `ORDER BY "installed_rank"`) {
		t.Errorf("history must be ordered by installed_rank: %q", sql)
	}
	if !strings.HasPrefix(sql, "SELECT ") || !strings.Contains(sql, `FROM "h"`) {
		t.Errorf("unexpected select: %q", sql)
	}
}

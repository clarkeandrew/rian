package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeConf(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "flyway.conf")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsLoadWithNoSources(t *testing.T) {
	// An empty config file with no env/flags must yield Flyway's defaults.
	conf := writeConf(t, "")
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Table != "flyway_schema_history" || cfg.SQLMigrationPrefix != "V" ||
		cfg.SQLMigrationSeparator != "__" || cfg.PlaceholderPrefix != "${" ||
		cfg.PlaceholderSuffix != "}" || !cfg.PlaceholderReplacement {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestConfFileParsing(t *testing.T) {
	conf := writeConf(t, `
# a comment
flyway.url=jdbc:postgresql://localhost/db
flyway.user = sa
flyway.locations = filesystem:a, filesystem:b
flyway.table=history
flyway.placeholders.env=prod
flyway.placeholders.region=eu
flyway.sqlMigrationSuffixes=.sql,.ddl
flyway.placeholderReplacement=false
notflyway.key=ignored
flyway.unknownKey=whatever
malformedline
`)
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "jdbc:postgresql://localhost/db" {
		t.Errorf("url = %q", cfg.URL)
	}
	if cfg.User != "sa" {
		t.Errorf("user = %q (whitespace around '=' should be trimmed)", cfg.User)
	}
	if !reflect.DeepEqual(cfg.Locations, []string{"filesystem:a", "filesystem:b"}) {
		t.Errorf("locations = %v", cfg.Locations)
	}
	if !reflect.DeepEqual(cfg.SQLMigrationSuffixes, []string{".sql", ".ddl"}) {
		t.Errorf("suffixes = %v", cfg.SQLMigrationSuffixes)
	}
	if cfg.Placeholders["env"] != "prod" || cfg.Placeholders["region"] != "eu" {
		t.Errorf("placeholders = %v", cfg.Placeholders)
	}
	if cfg.PlaceholderReplacement {
		t.Error("placeholderReplacement should be false")
	}
	// Unknown / malformed / non-flyway lines must produce warnings, not failure.
	if len(cfg.Warnings) < 3 {
		t.Errorf("expected >=3 warnings (unknownKey, notflyway, malformed), got %v", cfg.Warnings)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	conf := writeConf(t, "flyway.url=from-file\nflyway.user=file-user\n")
	env := []string{
		"FLYWAY_URL=from-env",
		"FLYWAY_SQL_MIGRATION_PREFIX=M",
		"FLYWAY_PLACEHOLDERS_FOO=envfoo",
		"FLYWAY_HOME=/opt/flyway", // not a config key; must be ignored silently
		"UNRELATED=x",
	}
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "from-env" {
		t.Errorf("env should override file: url = %q", cfg.URL)
	}
	if cfg.User != "file-user" {
		t.Errorf("user from file should remain: %q", cfg.User)
	}
	if cfg.SQLMigrationPrefix != "M" {
		t.Errorf("env camel mapping failed: prefix = %q", cfg.SQLMigrationPrefix)
	}
	if cfg.Placeholders["foo"] != "envfoo" {
		t.Errorf("env placeholder = %v", cfg.Placeholders)
	}
}

func TestFlagsOverrideEnvAndFile(t *testing.T) {
	conf := writeConf(t, "flyway.url=from-file\nflyway.table=file-table\n")
	env := []string{"FLYWAY_URL=from-env", "FLYWAY_TABLE=env-table"}
	url := "from-flag"
	cfg, err := Load(Flags{
		ConfigFiles:  []string{conf},
		URL:          &url,
		Placeholders: map[string]string{"k": "flagval"},
	}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "from-flag" {
		t.Errorf("flag should win: url = %q", cfg.URL)
	}
	if cfg.Table != "env-table" {
		t.Errorf("env should beat file when no flag: table = %q", cfg.Table)
	}
	if cfg.Placeholders["k"] != "flagval" {
		t.Errorf("flag placeholder = %v", cfg.Placeholders)
	}
}

func TestEmptyFlagStringStillOverrides(t *testing.T) {
	conf := writeConf(t, "flyway.user=file-user\n")
	empty := ""
	cfg, err := Load(Flags{ConfigFiles: []string{conf}, User: &empty}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "" {
		t.Errorf("explicit empty flag should override file: user = %q", cfg.User)
	}
}

func TestEnvToCamel(t *testing.T) {
	cases := map[string]string{
		"URL":                             "url",
		"SQL_MIGRATION_PREFIX":            "sqlMigrationPrefix",
		"REPEATABLE_SQL_MIGRATION_PREFIX": "repeatableSqlMigrationPrefix",
		"DEFAULT_SCHEMA":                  "defaultSchema",
	}
	for in, want := range cases {
		if got := envToCamel(in); got != want {
			t.Errorf("envToCamel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMissingConfigFileErrors(t *testing.T) {
	_, err := Load(Flags{ConfigFiles: []string{"/no/such/flyway.conf"}}, nil)
	if err == nil {
		t.Error("expected error for explicitly-specified missing config file")
	}
}

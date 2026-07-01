package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
flyway.url=jdbc:mysql://host/db?useSSL=false&a=1
flyway.user = sa
flyway.locations = filesystem:a, filesystem:b
flyway.table=history
flyway.placeholders.env=prod
flyway.placeholders.region=eu
flyway.placeholders.kv=x=y
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
	// strings.Cut splits on the FIRST '=', so '=' in JDBC URLs / values is kept.
	if cfg.URL != "jdbc:mysql://host/db?useSSL=false&a=1" {
		t.Errorf("url with embedded '=' truncated: %q", cfg.URL)
	}
	if cfg.Placeholders["kv"] != "x=y" {
		t.Errorf("placeholder value with '=' truncated: %q", cfg.Placeholders["kv"])
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
	// Unknown / malformed / non-flyway lines must each produce a categorized
	// warning, not a failure.
	for _, frag := range []string{"flyway.unknownKey", "non-flyway key", "without '='"} {
		if !anyContains(cfg.Warnings, frag) {
			t.Errorf("expected a warning containing %q, got %v", frag, cfg.Warnings)
		}
	}
}

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestTargetKey(t *testing.T) {
	conf := writeConf(t, "flyway.target=5\n")
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, []string{"FLYWAY_TARGET=7"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Target != "7" {
		t.Errorf("target = %q, want env override 7", cfg.Target)
	}
	tgt := "9"
	cfg, err = Load(Flags{ConfigFiles: []string{conf}, Target: &tgt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Target != "9" {
		t.Errorf("target = %q, want flag override 9", cfg.Target)
	}
}

func TestSchemaKeysWarnUnsupported(t *testing.T) {
	// schemas/defaultSchema are recognized (an existing flyway.conf still loads)
	// but unsupported, so each must produce a warning rather than a silent no-op.
	conf := writeConf(t, "flyway.schemas=a,b\nflyway.defaultSchema=a\n")
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{"flyway.schemas is not supported", "flyway.defaultSchema is not supported"} {
		if !anyContains(cfg.Warnings, frag) {
			t.Errorf("expected a warning containing %q, got %v", frag, cfg.Warnings)
		}
	}
}

func TestDuplicateKeyLastWins(t *testing.T) {
	conf := writeConf(t, "flyway.user=first\nflyway.user=second\n")
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "second" {
		t.Errorf("duplicate key should be last-wins: user = %q", cfg.User)
	}
}

func TestPlaceholderCaseAsymmetry(t *testing.T) {
	// Conf placeholder names keep their case; env placeholder names are lowercased.
	// A conf 'Foo' and an env 'FOO' are therefore distinct keys.
	conf := writeConf(t, "flyway.placeholders.Foo=conf\n")
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, []string{"FLYWAY_PLACEHOLDERS_FOO=env"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Placeholders["foo"] != "env" {
		t.Errorf("env placeholder should lowercase to 'foo': %v", cfg.Placeholders)
	}
	if cfg.Placeholders["Foo"] != "conf" {
		t.Errorf("conf placeholder should keep case 'Foo': %v", cfg.Placeholders)
	}
}

func TestCRLFConfFile(t *testing.T) {
	conf := writeConf(t, "flyway.user=sa\r\nflyway.table=h\r\n")
	cfg, err := Load(Flags{ConfigFiles: []string{conf}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "sa" {
		t.Errorf("CRLF should be trimmed: user = %q", cfg.User)
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
		t.Fatal("expected error for explicitly-specified missing config file")
	}
	if !strings.Contains(err.Error(), "/no/such/flyway.conf") {
		t.Errorf("error should name the missing file: %v", err)
	}
}

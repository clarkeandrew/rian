// Package config resolves Rian's runtime configuration from the same sources
// Flyway reads — `flyway.conf` files, `FLYWAY_*` environment variables, and CLI
// flags — and merges them with Flyway's precedence: flags > env > file.
//
// Keys mirror Flyway's: file keys are `flyway.<camelCaseName>` (and
// `flyway.placeholders.<name>`); env vars are `FLYWAY_<UPPER_SNAKE_NAME>` (and
// `FLYWAY_PLACEHOLDERS_<NAME>`). Unknown `flyway.*` file keys are recorded as
// warnings rather than failing, so an existing Flyway config still loads.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Config is the fully-resolved configuration.
type Config struct {
	URL      string
	User     string
	Password string

	Locations []string
	Table     string

	SQLMigrationPrefix           string
	RepeatableSQLMigrationPrefix string
	SQLMigrationSeparator        string
	SQLMigrationSuffixes         []string

	PlaceholderPrefix      string
	PlaceholderSuffix      string
	PlaceholderReplacement bool
	Placeholders           map[string]string

	BaselineVersion   string
	Target            string // highest version migrate applies; "" or "latest" = no limit
	OutOfOrder        bool   // allow applying a pending version below the latest applied one
	ValidateOnMigrate bool   // validate the recorded history before migrating

	// Warnings collects non-fatal issues (e.g. unsupported config keys) for the
	// caller to surface to the user.
	Warnings []string
}

// Default returns a Config populated with Flyway's default values.
func Default() Config {
	return Config{
		Locations:                    []string{"filesystem:sql"},
		Table:                        "flyway_schema_history",
		SQLMigrationPrefix:           "V",
		RepeatableSQLMigrationPrefix: "R",
		SQLMigrationSeparator:        "__",
		SQLMigrationSuffixes:         []string{".sql"},
		PlaceholderPrefix:            "${",
		PlaceholderSuffix:            "}",
		PlaceholderReplacement:       true,
		Placeholders:                 map[string]string{},
		BaselineVersion:              "1",
		ValidateOnMigrate:            true,
	}
}

// Flags holds CLI overrides. Pointer fields are nil when the flag was not set,
// so an explicit empty value still overrides lower-precedence sources.
type Flags struct {
	ConfigFiles       []string
	URL               *string
	User              *string
	Password          *string
	Locations         *[]string
	Table             *string
	Target            *string
	OutOfOrder        *bool
	ValidateOnMigrate *bool
	Placeholders      map[string]string
}

// Load builds a Config by starting from Default, then applying (in increasing
// precedence) config files, environment variables, and flags. environ is a list
// of "KEY=VALUE" strings (e.g. from os.Environ()). If no config file is given
// via flags and ./flyway.conf exists, it is loaded.
func Load(flags Flags, environ []string) (Config, error) {
	cfg := Default()

	files := flags.ConfigFiles
	if len(files) == 0 {
		if _, err := os.Stat("flyway.conf"); err == nil {
			files = []string{"flyway.conf"}
		}
	}
	for _, f := range files {
		if err := applyConfFile(&cfg, f); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg, environ)
	applyFlags(&cfg, flags)
	return cfg, nil
}

// set applies a single canonical key (the part after "flyway.") and returns
// whether the key is recognized.
func (cfg *Config) set(subkey, value string) bool {
	if name, ok := strings.CutPrefix(subkey, "placeholders."); ok {
		cfg.Placeholders[name] = value
		return true
	}
	switch subkey {
	case "url":
		cfg.URL = value
	case "user":
		cfg.User = value
	case "password":
		cfg.Password = value
	case "locations":
		cfg.Locations = splitList(value)
	case "schemas", "defaultSchema":
		// Recognized so existing Flyway configs load, but Rian always uses the
		// connection's default schema — silently honoring these would lie.
		cfg.Warnings = append(cfg.Warnings,
			fmt.Sprintf("flyway.%s is not supported: rian uses the connection's default schema", subkey))
	case "table":
		cfg.Table = value
	case "baselineVersion":
		cfg.BaselineVersion = value
	case "target":
		cfg.Target = value
	case "outOfOrder":
		cfg.OutOfOrder = parseBool(value, cfg.OutOfOrder)
	case "validateOnMigrate":
		cfg.ValidateOnMigrate = parseBool(value, cfg.ValidateOnMigrate)
	case "sqlMigrationPrefix":
		cfg.SQLMigrationPrefix = value
	case "repeatableSqlMigrationPrefix":
		cfg.RepeatableSQLMigrationPrefix = value
	case "sqlMigrationSeparator":
		cfg.SQLMigrationSeparator = value
	case "sqlMigrationSuffixes":
		cfg.SQLMigrationSuffixes = splitList(value)
	case "placeholderPrefix":
		cfg.PlaceholderPrefix = value
	case "placeholderSuffix":
		cfg.PlaceholderSuffix = value
	case "placeholderReplacement":
		cfg.PlaceholderReplacement = parseBool(value, cfg.PlaceholderReplacement)
	default:
		return false
	}
	return true
}

// applyConfFile parses a flyway.conf-style file (key=value, '#' comments) and
// applies each `flyway.*` key. Unknown keys become warnings.
func applyConfFile(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read config file %q: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s:%d: ignoring line without '='", path, lineNo))
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		sub, ok := strings.CutPrefix(key, "flyway.")
		if !ok {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s:%d: ignoring non-flyway key %q", path, lineNo, key))
			continue
		}
		if !cfg.set(sub, value) {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s:%d: unsupported config key flyway.%s", path, lineNo, sub))
		}
	}
	return sc.Err()
}

// applyEnv applies FLYWAY_* environment variables. Unknown FLYWAY_* names are
// ignored silently (many, like FLYWAY_HOME, are not config keys).
func applyEnv(cfg *Config, environ []string) {
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(name, "FLYWAY_")
		if !ok || rest == "" {
			continue
		}
		if ph, ok := strings.CutPrefix(rest, "PLACEHOLDERS_"); ok {
			cfg.set("placeholders."+strings.ToLower(ph), value)
			continue
		}
		cfg.set(envToCamel(rest), value)
	}
}

func applyFlags(cfg *Config, f Flags) {
	if f.URL != nil {
		cfg.URL = *f.URL
	}
	if f.User != nil {
		cfg.User = *f.User
	}
	if f.Password != nil {
		cfg.Password = *f.Password
	}
	if f.Locations != nil {
		cfg.Locations = *f.Locations
	}
	if f.Table != nil {
		cfg.Table = *f.Table
	}
	if f.Target != nil {
		cfg.Target = *f.Target
	}
	if f.OutOfOrder != nil {
		cfg.OutOfOrder = *f.OutOfOrder
	}
	if f.ValidateOnMigrate != nil {
		cfg.ValidateOnMigrate = *f.ValidateOnMigrate
	}
	for k, v := range f.Placeholders {
		cfg.Placeholders[k] = v
	}
}

// envToCamel converts an UPPER_SNAKE env suffix to the camelCase config key
// Flyway uses, e.g. "SQL_MIGRATION_PREFIX" -> "sqlMigrationPrefix".
func envToCamel(s string) string {
	parts := strings.Split(strings.ToLower(s), "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// splitList splits a comma-separated value, trimming spaces and dropping empties.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	raw := strings.Split(v, ",")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseBool(v string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true":
		return true
	case "false":
		return false
	default:
		return fallback
	}
}

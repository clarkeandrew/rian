// Package scan discovers migration files in configured locations and parses
// their filenames into structured migrations, ordering versioned migrations
// with Flyway's numeric-segment semantics.
//
// Filename grammar (Flyway defaults, all configurable via Options):
//
//	Versioned:  V<version>__<description>.sql   e.g. V1.2__add_users.sql
//	Repeatable: R__<description>.sql            e.g. R__refresh_views.sql
//
// The version is everything between the prefix and the first separator ("__");
// the description follows the separator. Underscores in the description are
// converted to spaces for the stored description, matching Flyway.
package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Type distinguishes versioned migrations (run once, in version order) from
// repeatable migrations (re-applied whenever their checksum changes).
type Type int

const (
	Versioned Type = iota
	Repeatable
)

func (t Type) String() string {
	switch t {
	case Versioned:
		return "Versioned"
	case Repeatable:
		return "Repeatable"
	default:
		return "Unknown"
	}
}

// Migration is a single discovered migration file.
type Migration struct {
	Type        Type
	Version     *Version // nil for repeatable migrations
	Description string   // underscores converted to spaces, as Flyway stores it
	Script      string   // base filename
	Path        string   // full filesystem path (empty until set by Scan)
}

// Options controls filename parsing. Defaults match Flyway's defaults.
type Options struct {
	SQLPrefix        string   // versioned migration prefix; default "V"
	RepeatablePrefix string   // repeatable migration prefix; default "R"
	Separator        string   // version/description separator; default "__"
	Suffixes         []string // accepted file suffixes; default [".sql"]
}

// DefaultOptions returns the Flyway-compatible default parsing options.
func DefaultOptions() Options {
	return Options{
		SQLPrefix:        "V",
		RepeatablePrefix: "R",
		Separator:        "__",
		Suffixes:         []string{".sql"},
	}
}

// descriptionFromRaw converts the filename description into the stored form:
// Flyway replaces underscores with spaces.
func descriptionFromRaw(raw string) string {
	return strings.ReplaceAll(raw, "_", " ")
}

// ParseFilename parses a base filename into a Migration.
//
// The boolean return is false (with nil error) when the name does not look like
// a migration at all — wrong suffix, or no matching prefix — so the caller can
// silently skip it. It returns an error when the name DOES match a migration
// prefix+suffix but is malformed (bad version, missing separator, empty
// description); this mirrors Flyway, which reports such files as invalid rather
// than ignoring them.
func ParseFilename(name string, opts Options) (*Migration, bool, error) {
	stem, ok := trimSuffix(name, opts.Suffixes)
	if !ok {
		return nil, false, nil
	}

	switch {
	case opts.RepeatablePrefix != "" && strings.HasPrefix(stem, opts.RepeatablePrefix):
		rest := stem[len(opts.RepeatablePrefix):]
		if !strings.HasPrefix(rest, opts.Separator) {
			return nil, false, fmt.Errorf("migration %q: missing separator %q after repeatable prefix", name, opts.Separator)
		}
		desc := rest[len(opts.Separator):]
		if desc == "" {
			return nil, false, fmt.Errorf("migration %q: empty description", name)
		}
		return &Migration{
			Type:        Repeatable,
			Description: descriptionFromRaw(desc),
			Script:      name,
		}, true, nil

	case opts.SQLPrefix != "" && strings.HasPrefix(stem, opts.SQLPrefix):
		rest := stem[len(opts.SQLPrefix):]
		idx := strings.Index(rest, opts.Separator)
		if idx < 0 {
			return nil, false, fmt.Errorf("migration %q: missing version/description separator %q", name, opts.Separator)
		}
		versionPart, desc := rest[:idx], rest[idx+len(opts.Separator):]
		if versionPart == "" {
			return nil, false, fmt.Errorf("migration %q: missing version", name)
		}
		if desc == "" {
			return nil, false, fmt.Errorf("migration %q: empty description", name)
		}
		ver, err := ParseVersion(versionPart)
		if err != nil {
			return nil, false, fmt.Errorf("migration %q: %w", name, err)
		}
		return &Migration{
			Type:        Versioned,
			Version:     ver,
			Description: descriptionFromRaw(desc),
			Script:      name,
		}, true, nil

	default:
		return nil, false, nil
	}
}

// trimSuffix returns the stem (name minus the first matching suffix) and true,
// or "" and false if no suffix matches.
func trimSuffix(name string, suffixes []string) (string, bool) {
	for _, suf := range suffixes {
		if suf != "" && strings.HasSuffix(name, suf) {
			return strings.TrimSuffix(name, suf), true
		}
	}
	return "", false
}

// Scan walks each location (recursively), parses migration filenames, and
// returns the migrations sorted into apply order: versioned migrations first by
// ascending version, then repeatable migrations by description.
//
// Locations may carry a leading "filesystem:" scheme, which is stripped.
// Missing locations are skipped (Flyway warns and continues). Two versioned
// migrations that resolve to the same version are a duplicate error.
func Scan(locations []string, opts Options) ([]Migration, error) {
	var migrations []Migration
	seen := map[string]string{} // canonical version -> script, for duplicate detection

	for _, loc := range locations {
		root := strings.TrimPrefix(loc, "filesystem:")
		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan location %q: %w", root, err)
		}
		if !info.IsDir() {
			continue
		}

		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			m, ok, perr := ParseFilename(d.Name(), opts)
			if perr != nil {
				return perr
			}
			if !ok {
				return nil
			}
			m.Path = path
			if m.Type == Versioned {
				key := m.Version.canonical()
				if prev, dup := seen[key]; dup {
					return fmt.Errorf("duplicate migration version %s: %q and %q", m.Version, prev, m.Script)
				}
				seen[key] = m.Script
			}
			migrations = append(migrations, *m)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	sortMigrations(migrations)
	return migrations, nil
}

// sortMigrations orders versioned migrations first (ascending version), then
// repeatable migrations alphabetically by description.
func sortMigrations(ms []Migration) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if a.Type != b.Type {
			return a.Type == Versioned
		}
		if a.Type == Versioned {
			return a.Version.Compare(b.Version) < 0
		}
		return a.Description < b.Description
	})
}

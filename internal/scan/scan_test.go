package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFilenameVersioned(t *testing.T) {
	opts := DefaultOptions()
	tests := []struct {
		name        string
		wantVersion string
		wantDesc    string
	}{
		{"V1__init.sql", "1", "init"},
		{"V1.2__add_users.sql", "1.2", "add users"},
		{"V1_2__add_users.sql", "1_2", "add users"}, // version uses '_', desc '_'->space
		{"V2.10.1__multi_word_desc.sql", "2.10.1", "multi word desc"},
		{"V20230101120000__snapshot.sql", "20230101120000", "snapshot"},
		{"V1__add__users.sql", "1", "add  users"}, // first-separator split -> literal double space
		{"V1__INIT.sql", "1", "INIT"},             // description case is preserved
	}
	for _, tt := range tests {
		m, ok, err := ParseFilename(tt.name, opts)
		if err != nil || !ok {
			t.Errorf("ParseFilename(%q) ok=%v err=%v", tt.name, ok, err)
			continue
		}
		if m.Type != Versioned {
			t.Errorf("%q: type = %v, want Versioned", tt.name, m.Type)
		}
		if m.Version.String() != tt.wantVersion {
			t.Errorf("%q: version = %q, want %q", tt.name, m.Version.String(), tt.wantVersion)
		}
		if m.Description != tt.wantDesc {
			t.Errorf("%q: description = %q, want %q", tt.name, m.Description, tt.wantDesc)
		}
		if m.Script != tt.name {
			t.Errorf("%q: script = %q, want %q", tt.name, m.Script, tt.name)
		}
	}
}

func TestParseFilenameRepeatable(t *testing.T) {
	opts := DefaultOptions()
	tests := []struct {
		name     string
		wantDesc string
	}{
		{"R__refresh_views.sql", "refresh views"},
		{"R__a__b_view.sql", "a  b view"}, // only the first separator is stripped -> double space
	}
	for _, tt := range tests {
		m, ok, err := ParseFilename(tt.name, opts)
		if err != nil || !ok {
			t.Errorf("ParseFilename(%q): ok=%v err=%v", tt.name, ok, err)
			continue
		}
		if m.Type != Repeatable {
			t.Errorf("%q: type = %v, want Repeatable", tt.name, m.Type)
		}
		if m.Version != nil {
			t.Errorf("%q: repeatable version = %v, want nil", tt.name, m.Version)
		}
		if m.Description != tt.wantDesc {
			t.Errorf("%q: description = %q, want %q", tt.name, m.Description, tt.wantDesc)
		}
	}
}

func TestParseFilenameSkips(t *testing.T) {
	opts := DefaultOptions()
	// Non-migration files -> skip (ok=false, no error). Includes case-sensitivity
	// checks: a lowercase prefix or an uppercase suffix is NOT a migration in
	// Flyway defaults, and treating it as one would cause phantom migrations.
	for _, name := range []string{
		"README.md", "notes.txt", "foo.sql", "schema.sql",
		"v1__init.sql",   // lowercase versioned prefix
		"r__refresh.sql", // lowercase repeatable prefix
		"V1__init.SQL",   // uppercase suffix
	} {
		m, ok, err := ParseFilename(name, opts)
		if ok || err != nil || m != nil {
			t.Errorf("ParseFilename(%q): expected skip (nil,false,nil), got m=%v ok=%v err=%v", name, m, ok, err)
		}
	}
}

func TestParseFilenameMalformed(t *testing.T) {
	opts := DefaultOptions()
	// Names that DO match a migration prefix+suffix but are malformed must return
	// (nil, false, error) with the specific failure — not a silent skip.
	tests := []struct {
		name    string
		errWant string
	}{
		{"V1.sql", "missing version/description separator"},
		{"V__init.sql", "missing version"},
		{"V1__.sql", "empty description"},
		{"Vx__init.sql", "not a non-negative integer"},
		{"R.sql", "missing separator"},
		{"Rinit.sql", "missing separator"},
		{"R__.sql", "empty description"},
	}
	for _, tt := range tests {
		m, ok, err := ParseFilename(tt.name, opts)
		if err == nil {
			t.Errorf("ParseFilename(%q): expected error, got nil", tt.name)
			continue
		}
		if ok || m != nil {
			t.Errorf("ParseFilename(%q): expected (nil,false), got m=%v ok=%v", tt.name, m, ok)
		}
		if !strings.Contains(err.Error(), tt.errWant) {
			t.Errorf("ParseFilename(%q): error %q does not contain %q", tt.name, err.Error(), tt.errWant)
		}
	}
}

// TestParseFilenameCustomOptions exercises non-default prefixes, separator, and
// multiple suffixes — the Flyway-configurable surface that a hardcoded
// implementation would silently break.
func TestParseFilenameCustomOptions(t *testing.T) {
	opts := Options{
		SQLPrefix:        "VER",
		RepeatablePrefix: "REP",
		Separator:        "--",
		Suffixes:         []string{".sql", ".ddl"},
	}

	m, ok, err := ParseFilename("VER1--init.ddl", opts) // trimmed from the 2nd suffix
	if err != nil || !ok {
		t.Fatalf("VER1--init.ddl: ok=%v err=%v", ok, err)
	}
	if m.Type != Versioned || m.Version.String() != "1" || m.Description != "init" {
		t.Errorf("VER1--init.ddl: got type=%v version=%v desc=%q", m.Type, m.Version, m.Description)
	}

	m, ok, err = ParseFilename("REP--x.sql", opts)
	if err != nil || !ok || m.Type != Repeatable || m.Description != "x" {
		t.Errorf("REP--x.sql: ok=%v err=%v m=%+v", ok, err, m)
	}

	// A default-style name is not a migration under these options.
	if m, ok, err := ParseFilename("V1__init.sql", opts); ok || err != nil || m != nil {
		t.Errorf("V1__init.sql under custom opts: expected skip, got m=%v ok=%v err=%v", m, ok, err)
	}
}

// TestScanOrdersAndDiscovers writes a tree of files and asserts discovery,
// skipping of non-migrations, and apply-order (versioned ascending numerically,
// repeatables last by description).
func TestScanOrdersAndDiscovers(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"V1__init.sql",
		"V1.10__later.sql",
		"V1.9__earlier.sql",
		"V2__two.sql",
		"R__b_view.sql",
		"R__a_view.sql",
		"README.md",  // skipped
		"helper.txt", // skipped
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("SELECT 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Also exercise recursion into a subdirectory.
	sub := filepath.Join(dir, "more")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "V1.5__middle.sql"), []byte("SELECT 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Scan([]string{"filesystem:" + dir}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	wantOrder := []string{
		"V1__init.sql",
		"V1.5__middle.sql",
		"V1.9__earlier.sql",
		"V1.10__later.sql", // 1.10 after 1.9 (numeric)
		"V2__two.sql",
		"R__a_view.sql", // repeatables last, by description
		"R__b_view.sql",
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d migrations, want %d: %+v", len(got), len(wantOrder), scripts(got))
	}
	for i, w := range wantOrder {
		if got[i].Script != w {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, got[i].Script, w, scripts(got))
		}
	}
}

func TestScanDuplicateVersion(t *testing.T) {
	// Each pair resolves to the same canonical version -> duplicate error.
	for _, pair := range [][2]string{
		{"V1__a.sql", "V1.0__b.sql"}, // trailing-zero equality
		{"V1__a.sql", "V01__b.sql"},  // leading-zero equality
	} {
		dir := t.TempDir()
		for _, f := range pair {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, err := Scan([]string{dir}, DefaultOptions())
		if err == nil {
			t.Errorf("%v: expected duplicate-version error, got nil", pair)
			continue
		}
		if !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("%v: error %q does not mention 'duplicate'", pair, err.Error())
		}
	}
}

func TestScanMissingLocationSkipped(t *testing.T) {
	got, err := Scan([]string{filepath.Join(t.TempDir(), "does-not-exist")}, DefaultOptions())
	if err != nil {
		t.Errorf("missing location should be skipped, got error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no migrations, got %d", len(got))
	}
}

func TestScanMalformedFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Vbad__x.sql"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Scan([]string{dir}, DefaultOptions())
	if err == nil {
		t.Fatal("expected error from malformed migration, got nil")
	}
	if !strings.Contains(err.Error(), "Vbad__x.sql") {
		t.Errorf("error %q does not identify the offending file", err.Error())
	}
}

func scripts(ms []Migration) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Script
	}
	return out
}

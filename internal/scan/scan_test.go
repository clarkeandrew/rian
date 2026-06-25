package scan

import (
	"os"
	"path/filepath"
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
	m, ok, err := ParseFilename("R__refresh_views.sql", opts)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if m.Type != Repeatable {
		t.Errorf("type = %v, want Repeatable", m.Type)
	}
	if m.Version != nil {
		t.Errorf("repeatable version = %v, want nil", m.Version)
	}
	if m.Description != "refresh views" {
		t.Errorf("description = %q, want %q", m.Description, "refresh views")
	}
}

func TestParseFilenameSkips(t *testing.T) {
	opts := DefaultOptions()
	// Non-migration files: wrong suffix or no matching prefix -> skip (ok=false, no error).
	for _, name := range []string{"README.md", "notes.txt", "foo.sql", "schema.sql"} {
		_, ok, err := ParseFilename(name, opts)
		if ok || err != nil {
			t.Errorf("ParseFilename(%q): expected skip (ok=false,err=nil), got ok=%v err=%v", name, ok, err)
		}
	}
}

func TestParseFilenameMalformed(t *testing.T) {
	opts := DefaultOptions()
	// Match a migration prefix+suffix but are malformed -> error (not silent skip).
	for _, name := range []string{
		"V1.sql",       // no separator
		"V__init.sql",  // missing version
		"V1__.sql",     // empty description
		"Vx__init.sql", // non-numeric version
		"R.sql",        // repeatable without separator
		"Rinit.sql",    // repeatable missing separator
		"R__.sql",      // repeatable empty description
	} {
		_, _, err := ParseFilename(name, opts)
		if err == nil {
			t.Errorf("ParseFilename(%q): expected error, got nil", name)
		}
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
	dir := t.TempDir()
	// 1 and 1.0 are the same version -> duplicate.
	for _, f := range []string{"V1__a.sql", "V1.0__b.sql"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Scan([]string{dir}, DefaultOptions()); err == nil {
		t.Error("expected duplicate-version error, got nil")
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
	if _, err := Scan([]string{dir}, DefaultOptions()); err == nil {
		t.Error("expected error from malformed migration, got nil")
	}
}

func scripts(ms []Migration) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Script
	}
	return out
}

package scan

import "testing"

func TestParseVersionValid(t *testing.T) {
	for _, s := range []string{"1", "1.2", "1.2.3", "1_2", "20230101120000", "001.002"} {
		if _, err := ParseVersion(s); err != nil {
			t.Errorf("ParseVersion(%q) unexpected error: %v", s, err)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, s := range []string{"", "1.", ".1", "1..2", "1.x", "abc", "-1", "1.-2"} {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) expected error, got nil", s)
		}
	}
}

func mustVersion(t *testing.T, s string) *Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", s, err)
	}
	return v
}

// TestVersionCompare covers the ordering rules that, if wrong, cause phantom
// re-runs: numeric segment comparison (1.10 > 1.9) and trailing-zero equality.
func TestVersionCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.9", "1.10", -1}, // numeric, NOT lexical (lexical would say 1.9 > 1.10)
		{"1.10", "1.11", -1},
		{"1.11", "1.9", 1},
		{"1", "1.0", 0}, // trailing zeros insignificant
		{"1.0.0", "1", 0},
		{"1_0", "1.0", 0},     // '_' and '.' equivalent
		{"001.002", "1.2", 0}, // leading zeros insignificant
		{"01", "1", 0},
		{"1.1", "1", 1},
		{"2", "10", -1}, // numeric across magnitudes
		{"20230101", "20230102", -1},
		{"1.2.3", "1.2.3", 0},
	}
	for _, tt := range tests {
		a, b := mustVersion(t, tt.a), mustVersion(t, tt.b)
		if got := a.Compare(b); got != tt.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		// Compare must be antisymmetric.
		if got := b.Compare(a); got != -tt.want {
			t.Errorf("Compare(%q,%q) = %d, want %d (antisymmetry)", tt.b, tt.a, got, -tt.want)
		}
	}
}

func TestVersionCanonical(t *testing.T) {
	// Versions that Compare-equal must share a canonical key (drives dedup).
	for _, pair := range [][2]string{{"1", "1.0"}, {"1.0.0", "1_0"}, {"2.10", "2.10.0"}, {"001.002", "1.2"}, {"01", "1"}} {
		a, b := mustVersion(t, pair[0]), mustVersion(t, pair[1])
		if a.Canonical() != b.Canonical() {
			t.Errorf("Canonical(%q)=%q != Canonical(%q)=%q", pair[0], a.Canonical(), pair[1], b.Canonical())
		}
	}
	// Distinct versions must NOT collide.
	a, b := mustVersion(t, "1.9"), mustVersion(t, "1.10")
	if a.Canonical() == b.Canonical() {
		t.Errorf("Canonical collision for 1.9 and 1.10: %q", a.Canonical())
	}
}

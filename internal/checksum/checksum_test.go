package checksum

import (
	"strings"
	"testing"
)

func calc(s string) int32 { return CalculateBytes([]byte(s)) }

// TestKnownCRC32Vectors anchors the implementation to the standard CRC-32
// (IEEE) of well-known inputs. A single line with no terminator hashes exactly
// its bytes, so these are independent, verifiable reference values.
func TestKnownCRC32Vectors(t *testing.T) {
	tests := []struct {
		in   string
		want int32
	}{
		{"", 0},
		{"abc", 891568578}, // CRC32("abc") = 0x352441C2 (positive)
		{"a", -390611389},  // CRC32("a")   = 0xE8B7BE43 = -390611389 as int32
	}
	for _, tt := range tests {
		if got := calc(tt.in); got != tt.want {
			t.Errorf("CalculateBytes(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestSignedWraparound verifies the result is a signed 32-bit int (high bit set
// yields a negative value), matching Flyway's `(int) crc32.getValue()`.
func TestSignedWraparound(t *testing.T) {
	if got := calc("a"); got >= 0 {
		t.Errorf("expected negative int32 for CRC32 with high bit set, got %d", got)
	}
}

// TestLineEndingIndependence is the core drop-in invariant: LF, CRLF and lone
// CR variants of the same content must produce the same checksum.
func TestLineEndingIndependence(t *testing.T) {
	lf := "CREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	cr := strings.ReplaceAll(lf, "\n", "\r")

	a, b, c := calc(lf), calc(crlf), calc(cr)
	if a != b || a != c {
		t.Errorf("line-ending dependence: lf=%d crlf=%d cr=%d (must all match)", a, b, c)
	}
}

// TestTrailingNewlineIndependence: a trailing terminator must not change the
// checksum (readLine yields no extra empty line at EOF).
func TestTrailingNewlineIndependence(t *testing.T) {
	if calc("SELECT 1;") != calc("SELECT 1;\n") {
		t.Error("trailing newline changed the checksum")
	}
	if calc("a\nb") != calc("a\nb\n") {
		t.Error("trailing newline changed the checksum (multiline)")
	}
}

// TestBOMStripped: a leading UTF-8 BOM (U+FEFF, bytes EF BB BF) must be ignored.
func TestBOMStripped(t *testing.T) {
	withBOM := string([]byte{0xEF, 0xBB, 0xBF}) + "SELECT 1;\n"
	without := "SELECT 1;\n"
	if calc(withBOM) != calc(without) {
		t.Error("leading UTF-8 BOM was not stripped")
	}
}

// TestNoSeparatorBetweenLines: terminators are stripped and no separator is
// re-inserted, so "a","b" on two lines must equal "ab" on one line.
func TestNoSeparatorBetweenLines(t *testing.T) {
	if calc("a\nb") != calc("ab") {
		t.Error("a separator appears to be inserted between lines; checksums must match")
	}
}

// TestEmptyInput: empty content hashes to 0 (CRC32 of nothing).
func TestEmptyInput(t *testing.T) {
	if got := calc(""); got != 0 {
		t.Errorf("empty input checksum = %d, want 0", got)
	}
}

// TestCalculateReaderMatchesBytes ensures the io.Reader path agrees with the
// byte-slice path.
func TestCalculateReaderMatchesBytes(t *testing.T) {
	in := "V1__init.sql\nCREATE TABLE x (id int);\n"
	got, err := Calculate(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got != calc(in) {
		t.Errorf("Calculate reader = %d, CalculateBytes = %d", got, calc(in))
	}
}

package scan

import (
	"fmt"
	"math/big"
	"strings"
)

// bigZero is the implicit value of a missing version part during comparison.
var bigZero = big.NewInt(0)

// Version is the parsed numeric version of a versioned migration. It mirrors
// Flyway's MigrationVersion ordering: the version text is split into numeric
// parts on '.' and '_' (Flyway treats them as equivalent separators), and parts
// are compared numerically — so "1.10" > "1.9". Trailing zero parts are
// insignificant, so "1" and "1.0" compare equal.
//
// Parts are stored as big.Int so timestamp-style versions (e.g.
// 20230101120000) never overflow, matching Flyway's use of BigInteger.
type Version struct {
	raw   string
	parts []*big.Int
}

// ParseVersion parses a version string such as "1", "1.2.3", or "1_2". It
// returns an error if the version is empty or contains a non-numeric part.
func ParseVersion(s string) (*Version, error) {
	if s == "" {
		return nil, fmt.Errorf("empty version")
	}
	// Flyway treats '.' and '_' as equivalent part separators.
	normalized := strings.ReplaceAll(s, "_", ".")
	rawParts := strings.Split(normalized, ".")
	parts := make([]*big.Int, 0, len(rawParts))
	for _, p := range rawParts {
		if p == "" {
			return nil, fmt.Errorf("invalid version %q: empty numeric part", s)
		}
		n, ok := new(big.Int).SetString(p, 10)
		if !ok || n.Sign() < 0 {
			return nil, fmt.Errorf("invalid version %q: %q is not a non-negative integer", s, p)
		}
		parts = append(parts, n)
	}
	return &Version{raw: s, parts: parts}, nil
}

// String returns the original version text as it appeared in the filename.
func (v *Version) String() string { return v.raw }

// Compare returns -1, 0, or 1 as v is less than, equal to, or greater than o.
// Shorter versions are zero-padded for comparison, so "1" == "1.0" and
// "1.1" > "1".
func (v *Version) Compare(o *Version) int {
	n := len(v.parts)
	if len(o.parts) > n {
		n = len(o.parts)
	}
	for i := 0; i < n; i++ {
		a, b := bigZero, bigZero
		if i < len(v.parts) {
			a = v.parts[i]
		}
		if i < len(o.parts) {
			b = o.parts[i]
		}
		if c := a.Cmp(b); c != 0 {
			return c
		}
	}
	return 0
}

// canonical returns a separator- and trailing-zero-normalized key so that
// versions which Compare as equal ("1", "1.0", "1_0") share one key. Used for
// duplicate detection.
func (v *Version) canonical() string {
	end := len(v.parts)
	for end > 0 && v.parts[end-1].Sign() == 0 {
		end--
	}
	if end == 0 {
		return "0"
	}
	strs := make([]string, end)
	for i := 0; i < end; i++ {
		strs[i] = v.parts[i].String()
	}
	return strings.Join(strs, ".")
}

// Package checksum computes migration checksums that are byte-for-byte
// compatible with Flyway's CRC32 calculation.
//
// Compatibility is the entire point of this package: an existing
// Flyway-managed database stores a CRC32 checksum for every applied migration,
// and Rian must compute the identical value or `validate` will report spurious
// mismatches. This mirrors Flyway's
// org.flywaydb.core.internal.resolver.ChecksumCalculator.
//
// Algorithm (matching Flyway):
//
//   - Strip a leading UTF-8 byte-order mark (BOM) from the input.
//   - Read the content line by line with Java BufferedReader.readLine
//     semantics: a line is terminated by "\n", "\r", or "\r\n", and the
//     terminator is NOT part of the line.
//   - Update a CRC32 (IEEE polynomial, same as java.util.zip.CRC32) with each
//     line's bytes, with NO separator re-inserted between lines.
//   - Return the CRC32 as a signed 32-bit int (Flyway stores `(int)
//     crc32.getValue()`).
//
// Because terminators are stripped and never re-added, the checksum is
// line-ending independent: LF, CRLF, and lone-CR variants of the same content
// produce the same value, as does a trailing newline or not.
//
// Encoding note: Flyway decodes bytes using the configured encoding into a
// String and then re-encodes each line as UTF-8 before hashing. For UTF-8
// input (the default and Rian's assumption) that round-trip is the identity,
// so hashing the raw line bytes is equivalent. Non-UTF-8 source encodings with
// non-ASCII content are a known nuance to handle if/when configurable
// encodings are supported.
package checksum

import (
	"bytes"
	"hash/crc32"
)

// utf8BOM is the UTF-8 byte-order mark stripped from the start of the input.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// CalculateBytes returns the Flyway-compatible checksum of the given content.
func CalculateBytes(data []byte) int32 {
	data = bytes.TrimPrefix(data, utf8BOM)

	crc := crc32.NewIEEE()
	for _, line := range splitLines(data) {
		crc.Write(line)
	}
	return int32(crc.Sum32())
}

// splitLines splits data into lines using Java BufferedReader.readLine
// semantics: terminators are "\n", "\r", or "\r\n" and are excluded from the
// returned lines. A final line without a terminator is still returned; a
// trailing terminator does NOT produce an extra empty line. Empty input
// returns no lines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); {
		switch data[i] {
		case '\n':
			lines = append(lines, data[start:i])
			i++
			start = i
		case '\r':
			lines = append(lines, data[start:i])
			i++
			if i < len(data) && data[i] == '\n' {
				i++
			}
			start = i
		default:
			i++
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

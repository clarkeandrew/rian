package sql

import "strings"

// Split breaks a migration script into individual statements on the active
// delimiter (default ";"), while treating delimiters inside string literals,
// quoted identifiers, line/block comments, and Postgres dollar-quoted bodies as
// ordinary text. A MySQL-style `DELIMITER <token>` line (at the start of a
// statement) changes the active delimiter.
//
// Returned statements are trimmed of surrounding whitespace; empty statements
// are dropped. Comments are preserved inside the statement text.
//
// Dollar-quote detection is active only while the delimiter is the default
// ";"; once a custom delimiter is in effect (MySQL usage) a leading "$$" is the
// delimiter, not a dollar quote — the two conventions do not co-occur.
func Split(content string) []string {
	var stmts []string
	var cur strings.Builder
	delimiter := ";"
	atLineStart := true

	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}

	i, n := 0, len(content)
	for i < n {
		if atLineStart {
			if newDelim, consumed, ok := parseDelimiterDirective(content[i:]); ok &&
				strings.TrimSpace(cur.String()) == "" {
				cur.Reset()
				delimiter = newDelim
				i += consumed
				atLineStart = true
				continue
			}
		}

		ch := content[i]

		switch {
		case ch == '-' && i+1 < n && content[i+1] == '-': // line comment
			j := i
			for j < n && content[j] != '\n' {
				j++
			}
			cur.WriteString(content[i:j])
			i = j
			atLineStart = false

		case ch == '/' && i+1 < n && content[i+1] == '*': // block comment
			j := i + 2
			for j+1 < n && !(content[j] == '*' && content[j+1] == '/') {
				j++
			}
			if j+1 < n {
				j += 2 // include closing */
			} else {
				j = n
			}
			cur.WriteString(content[i:j])
			i = j
			atLineStart = false

		case ch == '\'' || ch == '"' || ch == '`': // quoted string / identifier
			// Backslash is an escape only inside Postgres E'...' strings; in a
			// plain '...' literal (standard_conforming_strings=on, MySQL default)
			// it is literal and only '' escapes a quote.
			allowBackslash := ch == '\'' && isEStringStart(content, i)
			j := consumeQuoted(content, i, ch, allowBackslash)
			cur.WriteString(content[i:j])
			i = j
			atLineStart = false

		case matchAt(content, i, delimiter): // statement boundary
			flush()
			i += len(delimiter)
			atLineStart = false

		case ch == '$' && delimiter == ";": // Postgres dollar quote
			if tag, ok := dollarTag(content, i); ok {
				rest := content[i+len(tag):]
				close := strings.Index(rest, tag)
				if close < 0 {
					cur.WriteString(content[i:])
					i = n
				} else {
					end := i + len(tag) + close + len(tag)
					cur.WriteString(content[i:end])
					i = end
				}
				atLineStart = false
				break
			}
			cur.WriteByte(ch)
			i++
			atLineStart = false

		default:
			cur.WriteByte(ch)
			i++
			atLineStart = ch == '\n'
		}
	}
	flush()
	return stmts
}

// consumeQuoted returns the index just past the closing quote that matches the
// opening quote at i. A doubled quote always escapes; backslash escapes only
// when allowBackslash is set (Postgres E'...' strings).
func consumeQuoted(s string, i int, q byte, allowBackslash bool) int {
	n := len(s)
	for j := i + 1; j < n; {
		c := s[j]
		if c == '\\' && allowBackslash && j+1 < n {
			j += 2
			continue
		}
		if c == q {
			if j+1 < n && s[j+1] == q {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return n
}

// dollarTag reports whether a Postgres dollar-quote tag starts at i (s[i]=='$')
// and returns the full opening token, e.g. "$$" or "$body$".
func dollarTag(s string, i int) (string, bool) {
	n := len(s)
	j := i + 1
	for j < n && isTagChar(s[j]) {
		j++
	}
	if j < n && s[j] == '$' {
		return s[i : j+1], true
	}
	return "", false
}

// isEStringStart reports whether the single quote at i opens a Postgres E'...'
// string: the preceding byte is 'e'/'E' and is not part of a longer identifier.
func isEStringStart(s string, i int) bool {
	if i == 0 {
		return false
	}
	if c := s[i-1]; c != 'e' && c != 'E' {
		return false
	}
	return i-2 < 0 || !isTagChar(s[i-2])
}

func isTagChar(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func matchAt(s string, i int, token string) bool {
	return token != "" && strings.HasPrefix(s[i:], token)
}

// parseDelimiterDirective recognizes a leading `DELIMITER <token>` line. s must
// begin at a line start (leading horizontal whitespace allowed). It returns the
// new delimiter and the number of bytes consumed (through the end of the line).
func parseDelimiterDirective(s string) (string, int, bool) {
	const kw = "DELIMITER"
	n := len(s)
	i := 0
	for i < n && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i+len(kw) > n || !strings.EqualFold(s[i:i+len(kw)], kw) {
		return "", 0, false
	}
	j := i + len(kw)
	if j >= n || (s[j] != ' ' && s[j] != '\t') {
		return "", 0, false
	}
	for j < n && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	k := j
	for k < n && s[k] != '\n' && s[k] != '\r' && s[k] != ' ' && s[k] != '\t' {
		k++
	}
	delim := s[j:k]
	if delim == "" {
		return "", 0, false
	}
	for k < n && s[k] != '\n' {
		k++
	}
	if k < n && s[k] == '\n' {
		k++
	}
	return delim, k, true
}

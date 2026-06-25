// Package sql prepares migration SQL for execution: it substitutes
// `${placeholder}` values and splits a script into individual statements,
// honoring string literals, comments, Postgres dollar-quoting, and MySQL
// DELIMITER changes.
package sql

import (
	"fmt"
	"strings"
)

// Substitute replaces every occurrence of prefix+name+suffix in content with
// the corresponding placeholder value. It mirrors Flyway's default behavior: an
// unresolved placeholder is an error (Flyway fails rather than leaving it
// literal). When replacement is false (Flyway's placeholderReplacement=false)
// or prefix is empty, content is returned unchanged.
func Substitute(content string, placeholders map[string]string, prefix, suffix string, replacement bool) (string, error) {
	if !replacement || prefix == "" {
		return content, nil
	}

	var b strings.Builder
	i := 0
	for i < len(content) {
		rel := strings.Index(content[i:], prefix)
		if rel < 0 {
			b.WriteString(content[i:])
			break
		}
		start := i + rel
		b.WriteString(content[i:start])

		nameStart := start + len(prefix)
		relEnd := strings.Index(content[nameStart:], suffix)
		if relEnd < 0 {
			// No closing suffix: the rest cannot contain a placeholder.
			b.WriteString(content[start:])
			break
		}
		name := content[nameStart : nameStart+relEnd]
		value, ok := placeholders[name]
		if !ok {
			return "", fmt.Errorf("no value provided for placeholder %s%s%s", prefix, name, suffix)
		}
		b.WriteString(value)
		i = nameStart + relEnd + len(suffix)
	}
	return b.String(), nil
}

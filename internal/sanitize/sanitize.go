// Package sanitize removes characters that can control a terminal or hide
// text from a human reader. Everything that originates in Jira is passed
// through it before it is printed or returned to an MCP client.
package sanitize

import (
	"strings"
	"unicode/utf8"
)

// String removes C0 controls except tab and newline, DEL, C1 controls,
// bidirectional embeddings, overrides and isolates, zero-width format
// characters that can hide text, and Unicode tag characters. Invalid UTF-8
// is replaced with U+FFFD.
func String(s string) string {
	clean := true
	for _, r := range s {
		if r == utf8.RuneError || drop(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !drop(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func drop(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0x200b, r == 0x2060, r == 0xfeff:
		return true
	case r >= 0xe0000 && r <= 0xe007f:
		return true
	}
	return false
}

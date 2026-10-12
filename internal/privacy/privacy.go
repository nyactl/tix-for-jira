// Package privacy implements tix's scope rule: in mode own, only tickets
// assigned to the user are visible. Labels for people and redaction come
// from approvekit's people package. See docs/design.md.
package privacy

import (
	"errors"
	"strings"
	"unicode"

	"github.com/nyactl/approvekit/people"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

// InScope reports whether an issue with this assignee may be shown.
func InScope(p *people.People, assignee *jira.User) bool {
	return p.Mode() == people.Off || (assignee != nil && assignee.AccountID == p.MeID())
}

// ScopeJQL restricts a JQL query to the user's own tickets in own mode.
// The result is still re-checked per issue, because JQL text alone cannot
// be trusted to keep the restriction.
func ScopeJQL(mode people.Mode, userJQL string) (string, error) {
	if mode == people.Off {
		return userJQL, nil
	}
	where, order, err := splitOrderBy(userJQL)
	if err != nil {
		return "", err
	}
	q := "assignee = currentUser()"
	if strings.TrimSpace(where) != "" {
		q += " AND (" + strings.TrimSpace(where) + ")"
	}
	if order != "" {
		q += " " + order
	}
	return q, nil
}

var errUnterminated = errors.New("JQL has an unterminated quote")

// splitOrderBy separates a trailing top-level ORDER BY clause.
func splitOrderBy(jql string) (where, order string, err error) {
	var quote rune
	escaped := false
	depth := 0
	runes := []rune(jql)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == quote:
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'':
			quote = r
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 && matchOrderBy(runes, i) {
			return string(runes[:i]), strings.TrimSpace(string(runes[i:])), nil
		}
	}
	if quote != 0 {
		return "", "", errUnterminated
	}
	return jql, "", nil
}

func matchOrderBy(r []rune, i int) bool {
	if i > 0 && !unicode.IsSpace(r[i-1]) && r[i-1] != ')' {
		return false
	}
	rest := strings.ToLower(string(r[i:]))
	if !strings.HasPrefix(rest, "order") {
		return false
	}
	rest = rest[len("order"):]
	trimmed := strings.TrimLeftFunc(rest, unicode.IsSpace)
	return len(trimmed) < len(rest) && strings.HasPrefix(trimmed, "by") &&
		(len(trimmed) == 2 || unicode.IsSpace(rune(trimmed[2])))
}

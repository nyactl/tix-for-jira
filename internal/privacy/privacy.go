// Package privacy implements the scope rule ("only my tickets") and the
// replacement of other people by stable placeholders. See docs/design.md.
package privacy

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

// MeLabel is how the authenticated user appears in own mode.
const MeLabel = "Me"

// People assigns labels to Jira users and resolves them back for mentions.
// It is safe for concurrent use; labels are stable for its lifetime.
type People struct {
	mu       sync.Mutex
	mode     config.PrivacyMode
	me       string
	label    map[string]string   // accountID -> label
	account  map[string]string   // label (lower case) -> accountID
	display  map[string]string   // accountID -> best known display name
	reliable map[string]bool     // display came from a structured field
	names    map[string][]string // accountID -> every name and email seen, for redaction
	next     int
}

func NewPeople(mode config.PrivacyMode, me *jira.User) *People {
	p := &People{
		mode:     mode,
		me:       me.AccountID,
		label:    make(map[string]string),
		account:  make(map[string]string),
		display:  make(map[string]string),
		reliable: make(map[string]bool),
		names:    make(map[string][]string),
	}
	p.remember(me.AccountID, me.DisplayName, me.EmailAddress, true)
	return p
}

// Mode returns the privacy mode.
func (p *People) Mode() config.PrivacyMode { return p.mode }

// MeID returns the authenticated user's account ID.
func (p *People) MeID() string { return p.me }

// User returns the label for u, or "" for nil.
func (p *People) User(u *jira.User) string {
	if u == nil {
		return ""
	}
	return p.ID(u.AccountID, u.DisplayName, u.EmailAddress)
}

// ID returns the label for an account ID. display and email come from a
// structured field and may be empty.
func (p *People) ID(accountID, display, email string) string {
	return p.remember(accountID, display, email, true)
}

// Mention returns the label for a name that may be outdated, such as the
// text stored with a mention or a name in the change history. It never
// replaces a name learned from a current field, but is still redacted in
// free text.
func (p *People) Mention(accountID, text string) string {
	return p.remember(accountID, text, "", false)
}

func (p *People) remember(accountID, display, email string, reliable bool) string {
	display = strings.TrimSpace(strings.TrimPrefix(display, "@"))
	if accountID == "" {
		if p.mode == config.PrivacyOwn {
			return "someone"
		}
		return display
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if display != "" {
		p.addName(accountID, display)
		if p.display[accountID] == "" || (reliable && !p.reliable[accountID]) {
			p.display[accountID] = display
		}
		p.reliable[accountID] = p.reliable[accountID] || reliable
	}
	if email != "" {
		p.addName(accountID, email)
	}

	var l string
	switch {
	case p.mode == config.PrivacyOff:
		l = p.display[accountID]
		if l == "" {
			l = "unknown user"
		}
	case accountID == p.me:
		l = MeLabel
	default:
		if existing, ok := p.label[accountID]; ok {
			return existing
		}
		p.next++
		l = "Person " + letters(p.next)
	}
	p.label[accountID] = l
	p.account[strings.ToLower(l)] = accountID
	return l
}

func (p *People) addName(accountID, name string) {
	for _, n := range p.names[accountID] {
		if strings.EqualFold(n, name) {
			return
		}
	}
	p.names[accountID] = append(p.names[accountID], name)
}

// letters returns A..Z, AA..AZ, BA.. for 1, 2, ...
func letters(n int) string {
	var b []byte
	for n > 0 {
		n--
		b = append([]byte{byte('A' + n%26)}, b...)
		n /= 26
	}
	return string(b)
}

// Resolve maps a mention label back to an account ID and the display text
// Jira stores with the mention. Only people seen in this session resolve.
func (p *People) Resolve(label string) (accountID, display string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.account[strings.ToLower(strings.TrimSpace(label))]
	if !ok {
		return "", "", false
	}
	d := p.display[id]
	if d == "" {
		d = label
	}
	return id, d, true
}

// Text replaces every name and email seen so far for other people with
// their labels. Names that were never seen cannot be recognised, so this
// is best-effort. In off mode text is unchanged.
func (p *People) Text(s string) string {
	if p.mode != config.PrivacyOwn || s == "" {
		return s
	}
	p.mu.Lock()
	type repl struct{ from, to string }
	var rs []repl
	for id, l := range p.label {
		if id == p.me {
			continue
		}
		for _, n := range p.names[id] {
			if len([]rune(n)) >= 3 {
				rs = append(rs, repl{n, l})
			}
		}
	}
	p.mu.Unlock()
	sort.Slice(rs, func(i, j int) bool { return len(rs[i].from) > len(rs[j].from) })
	for _, r := range rs {
		s = replaceWord(s, r.from, r.to)
	}
	return s
}

// replaceWord replaces case-insensitive occurrences of from that are not
// part of a longer word.
func replaceWord(s, from, to string) string {
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(from))
	locs := re.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		before, _ := utf8.DecodeLastRuneInString(s[:loc[0]])
		after, _ := utf8.DecodeRuneInString(s[loc[1]:])
		if isWord(before) || isWord(after) {
			continue
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(to)
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func isWord(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
}

// InScope reports whether an issue with this assignee may be shown.
func (p *People) InScope(assignee *jira.User) bool {
	return p.mode == config.PrivacyOff || (assignee != nil && assignee.AccountID == p.me)
}

// ScopeJQL restricts a JQL query to the user's own tickets in own mode.
// The result is still re-checked per issue, because JQL text alone cannot
// be trusted to keep the restriction.
func (p *People) ScopeJQL(userJQL string) (string, error) {
	if p.mode == config.PrivacyOff {
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

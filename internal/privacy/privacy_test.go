package privacy

import (
	"testing"

	"github.com/nyactl/approvekit/people"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

var (
	me    = &jira.User{AccountID: "acc-me", DisplayName: "Max Mustermann", EmailAddress: "max@example.com"}
	alice = &jira.User{AccountID: "acc-alice", DisplayName: "Alice Smith", EmailAddress: "alice@example.com"}
)

func newPeople(mode people.Mode) *people.People {
	return people.New(mode, people.Person{AccountID: me.AccountID, DisplayName: me.DisplayName, Email: me.EmailAddress})
}

func TestInScope(t *testing.T) {
	t.Parallel()
	own := newPeople(people.Own)
	if !InScope(own, me) || InScope(own, alice) || InScope(own, nil) {
		t.Error("own mode scope wrong")
	}
	off := newPeople(people.Off)
	if !InScope(off, alice) || !InScope(off, nil) {
		t.Error("off mode must allow everything")
	}
}

func TestScopeJQL(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"", "assignee = currentUser()"},
		{"project = A", "assignee = currentUser() AND (project = A)"},
		{"project = A ORDER BY updated DESC", "assignee = currentUser() AND (project = A) ORDER BY updated DESC"},
		{"order by created", "assignee = currentUser() order by created"},
		{`summary ~ "order by" ORDER BY rank`, `assignee = currentUser() AND (summary ~ "order by") ORDER BY rank`},
		{"x) OR (project = B", "assignee = currentUser() AND (x) OR (project = B)"},
		{"status = Open OR assignee = someoneElse", "assignee = currentUser() AND (status = Open OR assignee = someoneElse)"},
		{"labels = border", "assignee = currentUser() AND (labels = border)"},
	}
	for _, tt := range tests {
		got, err := ScopeJQL(people.Own, tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ScopeJQL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := ScopeJQL(people.Own, `summary ~ "open`); err == nil {
		t.Error("unterminated quote accepted")
	}
	if got, _ := ScopeJQL(people.Off, "project = A"); got != "project = A" {
		t.Errorf("off mode changed JQL: %q", got)
	}
}

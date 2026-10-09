package privacy

import (
	"testing"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

var (
	me    = &jira.User{AccountID: "acc-me", DisplayName: "Max Mustermann", EmailAddress: "max@example.com"}
	alice = &jira.User{AccountID: "acc-alice", DisplayName: "Alice Smith", EmailAddress: "alice@example.com"}
	bob   = &jira.User{AccountID: "acc-bob", DisplayName: "Bob Jones"}
)

func TestLabelsInOwnMode(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOwn, me)
	if got := p.User(me); got != MeLabel {
		t.Errorf("me = %q", got)
	}
	if got := p.User(alice); got != "Person A" {
		t.Errorf("alice = %q", got)
	}
	if got := p.User(bob); got != "Person B" {
		t.Errorf("bob = %q", got)
	}
	if got := p.User(alice); got != "Person A" {
		t.Errorf("alice again = %q", got)
	}
	if got := p.User(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	if got := p.ID("", "Deleted User", ""); got != "someone" {
		t.Errorf("no account id = %q", got)
	}
}

func TestLabelsInOffMode(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOff, me)
	if got := p.User(alice); got != "Alice Smith" {
		t.Errorf("alice = %q", got)
	}
	if got := p.User(me); got != "Max Mustermann" {
		t.Errorf("me = %q", got)
	}
	if id, _, ok := p.Resolve("alice smith"); !ok || id != "acc-alice" {
		t.Errorf("resolve = %q %v", id, ok)
	}
	if got := p.Text("ask Alice Smith"); got != "ask Alice Smith" {
		t.Errorf("text changed in off mode: %q", got)
	}
}

func TestLetters(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]string{1: "A", 26: "Z", 27: "AA", 28: "AB", 52: "AZ", 53: "BA"} {
		if got := letters(n); got != want {
			t.Errorf("letters(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOwn, me)
	p.User(alice)
	if id, display, ok := p.Resolve("person a"); !ok || id != "acc-alice" || display != "Alice Smith" {
		t.Errorf("Person A -> %q %q %v", id, display, ok)
	}
	if id, _, ok := p.Resolve("Me"); !ok || id != "acc-me" {
		t.Errorf("Me -> %q %v", id, ok)
	}
	if _, _, ok := p.Resolve("Alice Smith"); ok {
		t.Error("real name must not resolve in own mode")
	}
	if _, _, ok := p.Resolve("Person B"); ok {
		t.Error("unseen label resolved")
	}
}

func TestText(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOwn, me)
	p.User(alice)
	p.User(&jira.User{AccountID: "acc-al", DisplayName: "Al"})
	tests := []struct{ in, want string }{
		{"please ask Alice Smith", "please ask Person A"},
		{"ALICE SMITH said", "Person A said"},
		{"mail alice@example.com", "mail Person A"},
		{"Alice Smithson is someone else", "Alice Smithson is someone else"},
		{"Al is too short to replace safely", "Al is too short to replace safely"},
		{"Max Mustermann is me", "Max Mustermann is me"},
		{"x" + string(rune(0xe4)) + "Alice Smith", "x" + string(rune(0xe4)) + "Alice Smith"},
		{"(Alice Smith)", "(Person A)"},
	}
	for _, tt := range tests {
		if got := p.Text(tt.in); got != tt.want {
			t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestInScope(t *testing.T) {
	t.Parallel()
	own := NewPeople(config.PrivacyOwn, me)
	if !own.InScope(me) || own.InScope(alice) || own.InScope(nil) {
		t.Error("own mode scope wrong")
	}
	off := NewPeople(config.PrivacyOff, me)
	if !off.InScope(alice) || !off.InScope(nil) {
		t.Error("off mode must allow everything")
	}
}

func TestScopeJQL(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOwn, me)
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
		got, err := p.ScopeJQL(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ScopeJQL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := p.ScopeJQL(`summary ~ "open`); err == nil {
		t.Error("unterminated quote accepted")
	}
	off := NewPeople(config.PrivacyOff, me)
	if got, _ := off.ScopeJQL("project = A"); got != "project = A" {
		t.Errorf("off mode changed JQL: %q", got)
	}
}

func TestMentionTextNeverReplacesRealName(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOwn, me)
	if got := p.Mention("acc-alice", "@Ali (old name)"); got != "Person A" {
		t.Fatalf("mention label = %q", got)
	}
	p.User(alice)
	got := p.Text("Alice Smith and Ali (old name) and alice@example.com")
	if got != "Person A and Person A and Person A" {
		t.Errorf("Text = %q", got)
	}
	if _, display, _ := p.Resolve("Person A"); display != "Alice Smith" {
		t.Errorf("display = %q, want the reliable name", display)
	}
}

func TestOffModeLabelUpgradesToReliableName(t *testing.T) {
	t.Parallel()
	p := NewPeople(config.PrivacyOff, me)
	if got := p.Mention("acc-alice", "@Ali"); got != "Ali" {
		t.Fatalf("label from mention = %q", got)
	}
	if got := p.User(alice); got != "Alice Smith" {
		t.Errorf("label after structured name = %q", got)
	}
	if got := p.Mention("acc-alice", "@Ali"); got != "Alice Smith" {
		t.Errorf("stale mention text took over again: %q", got)
	}
}

package render

import (
	"strings"
	"testing"

	"github.com/nyactl/tix-jira/internal/core"
)

func TestIssueList(t *testing.T) {
	t.Parallel()
	got := IssueList([]core.IssueSummary{{Key: "A-1", Summary: "Fix", Type: "Bug", Status: "To Do", Priority: "High", Due: "2026-10-31", Labels: []string{"x", "y"}, Updated: "2026-10-08 10:00"}})
	want := "- **A-1** [To Do] Fix (High; Bug; due 2026-10-31; labels: x, y; updated 2026-10-08 10:00)\n"
	if got != want {
		t.Errorf("got %q", got)
	}
	if IssueList(nil) != "No matching tickets.\n" {
		t.Error("empty list")
	}
}

func TestIssueSanitizes(t *testing.T) {
	t.Parallel()
	d := &core.IssueDetail{
		IssueSummary: core.IssueSummary{Key: "A-1", Summary: "Hi\x1b[2J", Status: "Done", Type: "Task"},
		Assignee:     "Me",
		Description:  "line\rhidden",
		Links:        []core.Related{{Relation: "blocks", Key: "A-2", Type: "Task", Status: "To Do"}},
		Fields:       []core.FieldValue{{Name: "Notes", Value: "a\nb"}},
	}
	got := Issue(d)
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("unsanitized output: %q", got)
	}
	for _, want := range []string{"# A-1: Hi[2J", "## Description\n\nlinehidden", "- blocks A-2 [Task, To Do]", "- Notes:\n\n    a\n    b"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestJSONSanitizes(t *testing.T) {
	t.Parallel()
	got, err := JSON(map[string]string{"s": "x" + string(rune(0x202e)) + "y\x1b"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "{\n  \"s\": \"xy\\u001b\"\n}" {
		t.Errorf("got %q", got)
	}
}

func TestHistory(t *testing.T) {
	t.Parallel()
	got := History([]core.ChangeView{{Author: "Person A", When: "2026-10-02 09:00", Changes: []core.FieldChange{{Field: "assignee", From: "", To: "Me"}}}})
	if got != "- 2026-10-02 09:00, Person A:\n  - assignee: (empty) → Me\n" {
		t.Errorf("got %q", got)
	}
}

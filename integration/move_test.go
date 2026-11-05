//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

func typeAndParent(t *testing.T, jc *jira.Client, key string) (string, bool, string) {
	t.Helper()
	i, err := jc.GetIssueFields(context.Background(), key, []string{"issuetype", "parent"})
	if err != nil {
		t.Fatal(err)
	}
	parent := ""
	if i.Fields.Parent != nil {
		parent = i.Fields.Parent.Key
	}
	return i.Fields.IssueType.Name, i.Fields.IssueType.Subtask, parent
}

// TestMove turns a ticket into a subtask, moves it to another parent and
// turns it back into a task, through Jira's bulk move.
func TestMove(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	child := s.create(t, "tix-for-jira move: child", "")
	first := s.create(t, "tix-for-jira move: first parent", "")
	second := s.create(t, "tix-for-jira move: second parent", "")

	p, err := s.svc.PlanMove(ctx, child, first, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- Type: Task -> ", "- Parent: none -> " + first, "- Status: ", "follows its parent's epic and sprint"} {
		if !strings.Contains(p.Description, want) {
			t.Errorf("plan lacks %q:\n%s", want, p.Description)
		}
	}
	applied(t)(p, nil)
	if typ, sub, parent := typeAndParent(t, s.jc, child); !sub || parent != first {
		t.Fatalf("after the first move %s is a %s under %q", child, typ, parent)
	}

	// The parent now has a subtask, so it cannot become one itself.
	if _, err := s.svc.PlanMove(ctx, first, second, ""); err == nil || !strings.Contains(err.Error(), "has 1 subtask(s)") {
		t.Errorf("parent with a subtask: %v", err)
	}
	if _, err := s.svc.PlanMove(ctx, second, child, ""); err == nil || !strings.Contains(err.Error(), "goes under a standard ticket") {
		t.Errorf("subtask as parent: %v", err)
	}
	if _, err := s.svc.PlanMove(ctx, child, first, ""); err == nil || !strings.Contains(err.Error(), "already is a") {
		t.Errorf("same parent again: %v", err)
	}

	p, err = s.svc.PlanMove(ctx, child, second, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Description, "- Type:") || !strings.Contains(p.Description, first+" (") || !strings.Contains(p.Description, "-> "+second) {
		t.Errorf("plan:\n%s", p.Description)
	}
	applied(t)(p, nil)
	if typ, sub, parent := typeAndParent(t, s.jc, child); !sub || parent != second {
		t.Fatalf("after the second move %s is a %s under %q", child, typ, parent)
	}

	applied(t)(s.svc.PlanMove(ctx, child, "", "Task"))
	if typ, sub, parent := typeAndParent(t, s.jc, child); sub || typ != "Task" || parent != "" {
		t.Fatalf("after turning it back %s is a %s under %q", child, typ, parent)
	}
}

// TestMoveWithSomeoneElse checks that tickets of the other person can be
// neither moved nor used as the parent, and that an epic of theirs that the
// moved ticket leaves shows only key, type and status.
func TestMoveWithSomeoneElse(t *testing.T) {
	s := setup(t)
	other := setupOther(t, s)
	ctx := context.Background()
	epic := other.createOwnedType(t, s.project, "tix-for-jira move: their epic "+s.run, func(it jira.IssueType) bool { return it.HierarchyLevel == 1 })
	theirs := other.createOwned(t, s.project, "tix-for-jira move: their task "+s.run)
	mine := s.create(t, "tix-for-jira move: mine", "")
	parent := s.create(t, "tix-for-jira move: my parent", "")
	applied(t)(s.svc.PlanUpdate(ctx, mine, []core.FieldInput{{Field: "parent", Value: epic}}))

	if _, err := s.svc.PlanMove(ctx, theirs, parent, ""); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("moving their ticket: %v", err)
	}
	if _, err := s.svc.PlanMove(ctx, mine, theirs, ""); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("their ticket as parent: %v", err)
	}
	p, err := s.svc.PlanMove(ctx, mine, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, "- Parent: "+epic+" (") || strings.Contains(p.Description, "their epic") {
		t.Errorf("plan:\n%s", p.Description)
	}
	other.assertAbsent(t, "plan", p.Description)
	other.assertAbsent(t, "result", applied(t)(p, nil))
	if typ, sub, got := typeAndParent(t, s.jc, mine); !sub || got != parent {
		t.Fatalf("%s is a %s under %q", mine, typ, got)
	}
}

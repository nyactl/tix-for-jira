//go:build integration

package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

// TestEpicOfSomeoneElse puts a ticket of the tested account under an epic
// of the other person, which tix may name but not read.
func TestEpicOfSomeoneElse(t *testing.T) {
	s := setup(t)
	other := setupOther(t, s)
	ctx := context.Background()
	epicSummary := "tix-for-jira epics: owned by the other person " + s.run
	epic := other.createOwnedType(t, s.project, epicSummary, func(it jira.IssueType) bool { return it.HierarchyLevel == 1 })
	theirTask := other.createOwned(t, s.project, "tix-for-jira epics: their task "+s.run)
	mine := s.create(t, "tix-for-jira epics: my ticket", "")
	parentOf := func(key string) string {
		t.Helper()
		i, err := s.jc.GetIssueFields(ctx, key, []string{"parent"})
		if err != nil {
			t.Fatal(err)
		}
		if i.Fields.Parent == nil {
			return ""
		}
		return i.Fields.Parent.Key
	}

	if _, err := s.svc.Issue(ctx, epic); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("the epic itself is visible: %v", err)
	}
	if _, err := s.svc.PlanUpdate(ctx, mine, []core.FieldInput{{Field: "parent", Value: theirTask}}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("their task as parent: %v", err)
	}

	p, err := s.svc.PlanUpdate(ctx, mine, []core.FieldInput{{Field: "Parent", Value: epic}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, epic+" (") || strings.Contains(p.Description, "owned by the other person") {
		t.Errorf("plan:\n%s", p.Description)
	}
	other.assertAbsent(t, "plan", p.Description)
	applied(t)(p, nil)
	if got := parentOf(mine); got != epic {
		t.Fatalf("parent = %q, want %s", got, epic)
	}

	d, err := s.svc.Issue(ctx, mine)
	if err != nil {
		t.Fatal(err)
	}
	if d.Parent == nil || d.Parent.Key != epic || d.Parent.Summary != "" || d.Parent.Type == "" {
		t.Errorf("parent view %+v", d.Parent)
	}
	other.assertAbsent(t, "issue", d)

	epics, err := s.svc.Epics(ctx, s.project, 200)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(epics, func(e core.Related) bool { return e.Key == epic })
	if i < 0 || epics[i].Summary != "" || epics[i].Status == "" {
		t.Errorf("epics: %+v", epics)
	}
	other.assertAbsent(t, "epics", epics)

	s.svc.ShowEpicSummaries(true)
	epics, _ = s.svc.Epics(ctx, s.project, 200)
	if i := slices.IndexFunc(epics, func(e core.Related) bool { return e.Key == epic }); i < 0 || epics[i].Summary != epicSummary {
		t.Errorf("epics with summaries: %+v", epics)
	}
	if d, _ := s.svc.Issue(ctx, mine); d == nil || d.Parent == nil || d.Parent.Summary != epicSummary {
		t.Errorf("parent with summary: %+v", d)
	}
	s.svc.ShowEpicSummaries(false)

	msg := applied(t)(s.svc.PlanCreate(ctx, core.CreateInput{Project: s.project, Type: "Task", Summary: "tix-for-jira epics: created under it " + s.run, Parent: epic, Labels: []string{"tix-for-jira-it"}}))
	created := strings.TrimSuffix(strings.Fields(msg)[1], ":")
	closeWhenDone(t, s.jc, created)
	if got := parentOf(created); got != epic {
		t.Errorf("created ticket's parent = %q", got)
	}

	p, err = s.svc.PlanUpdate(ctx, mine, []core.FieldInput{{Field: "parent", Value: ""}})
	if err != nil {
		t.Fatal(err)
	}
	other.assertAbsent(t, "removal plan", p.Description)
	applied(t)(p, nil)
	if got := parentOf(mine); got != "" {
		t.Errorf("parent after removal = %q", got)
	}
}

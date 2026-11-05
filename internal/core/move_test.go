package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
	"github.com/nyactl/tix-for-jira/internal/jiratest"
)

func init() { jira.BulkPollInterval = time.Millisecond }

func moveFixture(t *testing.T) *jiratest.Fake {
	t.Helper()
	f := fixture(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-10", Summary: "Epic of Alice", Assignee: "acc-alice", Type: "Epic"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-11", Summary: "Story to fold", Assignee: "acc-me", Status: "In Progress", Parent: "PROJ-10"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-12", Summary: "Another story", Assignee: "acc-me"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-13", Summary: "My subtask", Assignee: "acc-me", Type: "Subtask", Parent: "PROJ-12"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-14", Summary: "My epic", Assignee: "acc-me", Type: "Epic"})
	f.AddIssue(jiratest.Issue{Key: "OTHER-1", Summary: "Elsewhere", Assignee: "acc-me"})
	return f
}

func TestPlanMoveToSubtask(t *testing.T) {
	t.Parallel()
	f := moveFixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	p, err := s.PlanMove(ctx, "proj-11", "PROJ-12", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Move PROJ-11 (Story to fold):",
		"- Type: Task -> Subtask",
		"- Parent: PROJ-10 (Epic, To Do) -> PROJ-12 (Another story)",
		"- Status: In Progress is not in the Subtask workflow; Jira sets its default status",
		"follows its parent's epic and sprint",
	} {
		if !strings.Contains(p.Description, want) {
			t.Errorf("description lacks %q:\n%s", want, p.Description)
		}
	}
	if p.Kind != Destructive || p.Key != "PROJ-11" {
		t.Errorf("plan = %s %s", p.Kind, p.Key)
	}
	assertNoPersonalData(t, p.Description)
	msg, err := p.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "Moved PROJ-11: now a Subtask of PROJ-12." {
		t.Errorf("msg = %q", msg)
	}
	i := f.Issues["PROJ-11"]
	if i.Type != "Subtask" || i.Parent != "PROJ-12" || i.Status != "To Do" {
		t.Errorf("issue = %s under %s in %s", i.Type, i.Parent, i.Status)
	}
	polls := 0
	for _, r := range f.Requests {
		if strings.HasPrefix(r, "GET /rest/api/3/bulk/queue/") {
			polls++
		}
	}
	if polls != 2 {
		t.Errorf("polled %d times, want 2 (running, then complete)", polls)
	}
}

func TestPlanMoveSubtaskElsewhereAndBack(t *testing.T) {
	t.Parallel()
	f := moveFixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	p, err := s.PlanMove(ctx, "PROJ-13", "PROJ-11", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Description, "Type:") || strings.Contains(p.Description, "follows its parent") {
		t.Errorf("type stays, so no type line:\n%s", p.Description)
	}
	if !strings.Contains(p.Description, "- Parent: PROJ-12 (Task, To Do) -> PROJ-11 (Story to fold)") || !strings.Contains(p.Description, "- Status: stays To Do") {
		t.Errorf("description:\n%s", p.Description)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if i := f.Issues["PROJ-13"]; i.Type != "Subtask" || i.Parent != "PROJ-11" {
		t.Errorf("issue = %s under %s", i.Type, i.Parent)
	}

	p, err = s.PlanMove(ctx, "PROJ-13", "", "bug")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, "- Type: Subtask -> Bug") || !strings.Contains(p.Description, "- Parent: PROJ-11 (Task, In Progress) -> none") {
		t.Errorf("description:\n%s", p.Description)
	}
	msg, err := p.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "Moved PROJ-13: now a Bug." {
		t.Errorf("msg = %q", msg)
	}
	if i := f.Issues["PROJ-13"]; i.Type != "Bug" || i.Parent != "" {
		t.Errorf("issue = %s under %q", i.Type, i.Parent)
	}
}

func TestPlanMoveRefuses(t *testing.T) {
	t.Parallel()
	f := moveFixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	for _, c := range []struct{ key, parent, typ, want string }{
		{"PROJ-1", "PROJ-12", "", "has 1 subtask(s); Jira cannot nest subtasks"},
		{"PROJ-14", "PROJ-12", "", "is a Epic; only standard tickets and subtasks"},
		{"PROJ-3", "", "", "is no subtask; name the ticket"},
		{"PROJ-13", "", "", "name the type"},
		{"PROJ-13", "", "Subtask", `"Subtask" is no standard type in PROJ; choose one of: Task, Bug`},
		{"PROJ-11", "PROJ-12", "Bug", `"Bug" is no subtask type in PROJ; choose one of: Subtask`},
		{"PROJ-11", "PROJ-11", "", "cannot be its own parent"},
		{"PROJ-11", "PROJ-13", "", "goes under a standard ticket, not under PROJ-13"},
		{"PROJ-11", "PROJ-14", "", "goes under a standard ticket, not under PROJ-14"},
		{"PROJ-11", "PROJ-10", "", "goes under a standard ticket, not under PROJ-10"},
		{"PROJ-11", "PROJ-2", "", "outside your privacy scope"},
		{"PROJ-11", "OTHER-1", "", "OTHER-1 is in another project"},
		{"PROJ-13", "PROJ-12", "", "already is a Subtask of PROJ-12"},
		{"PROJ-2", "PROJ-12", "", "outside your privacy scope"},
	} {
		_, err := s.PlanMove(ctx, c.key, c.parent, c.typ)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("PlanMove(%s, %q, %q) = %v, want %q", c.key, c.parent, c.typ, err, c.want)
		}
	}
	for _, r := range f.Requests {
		if strings.Contains(r, "/bulk/") {
			t.Errorf("refused moves must not reach Jira: %s", r)
		}
	}
}

func TestPlanMoveReportsJiraFailure(t *testing.T) {
	t.Parallel()
	f := moveFixture(t)
	f.MoveFailure = "The parent is in another sprint."
	s := service(t, f, config.PrivacyOwn)
	p, err := s.PlanMove(context.Background(), "PROJ-11", "PROJ-12", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "jira did not move PROJ-11: The parent is in another sprint.") {
		t.Errorf("err = %v", err)
	}
	if f.Issues["PROJ-11"].Type != "Task" {
		t.Error("failed move changed the issue")
	}
}

func TestMoveTarget(t *testing.T) {
	t.Parallel()
	types := []jira.IssueType{
		{ID: "1", Name: "Story"}, {ID: "2", Name: "Task"}, {ID: "3", Name: "Epic", HierarchyLevel: 1},
		{ID: "4", Name: "Sub-task", Subtask: true, HierarchyLevel: -1}, {ID: "5", Name: "Bug-subtask", Subtask: true, HierarchyLevel: -1},
	}
	story, sub := &types[0], &types[4]
	for _, c := range []struct {
		from      *jira.IssueType
		toSubtask bool
		name      string
		want, err string
	}{
		{sub, true, "", "5", ""}, // a subtask keeps its type when it moves
		{story, true, "", "", "name the type, one of: Sub-task, Bug-subtask"},
		{story, true, "subtask", "4", ""},
		{sub, false, "STORY", "1", ""},
		{sub, false, "Epic", "", `"Epic" is no standard type in P; choose one of: Story, Task`},
		{sub, false, "", "", "name the type, one of: Story, Task"},
	} {
		got, err := moveTarget(types, c.from, c.toSubtask, c.name, "P")
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("moveTarget(%s, %v, %q) = %v, want error %q", c.from.Name, c.toSubtask, c.name, err, c.err)
		case c.err == "" && (err != nil || got.ID != c.want):
			t.Errorf("moveTarget(%s, %v, %q) = %+v, %v, want %s", c.from.Name, c.toSubtask, c.name, got, err, c.want)
		}
	}
	if _, err := moveTarget(types[:3], story, true, "", "P"); err == nil || err.Error() != "P has no subtask issue type" {
		t.Errorf("no subtask type: %v", err)
	}
}

func TestPlanMoveSurvivesFailedChecks(t *testing.T) {
	t.Parallel()
	f := moveFixture(t)
	f.QueueErrors = 2
	s := service(t, f, config.PrivacyOwn)
	p, err := s.PlanMove(context.Background(), "PROJ-11", "PROJ-12", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(context.Background()); err != nil {
		t.Fatalf("two failed checks ended the move: %v", err)
	}

	f.QueueErrors = 3
	p, err = s.PlanMove(context.Background(), "PROJ-13", "PROJ-3", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "could not check the move of PROJ-13, which may still finish in Jira") {
		t.Errorf("three failed checks: %v", err)
	}
}

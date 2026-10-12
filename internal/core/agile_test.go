package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jiratest"
)

func agileFixture(t *testing.T) *jiratest.Fake {
	f := fixture(t)
	f.Boards = []jiratest.Board{
		{ID: 1, Name: "PROJ board", Type: "scrum", Project: "PROJ"},
		{ID: 2, Name: "PROJ flow", Type: "kanban", Project: "PROJ"},
		{ID: 3, Name: "Alice Smith's board", Type: "scrum", LocationOwner: "acc-alice"},
	}
	f.Sprints = []jiratest.Sprint{
		{ID: 11, Board: 1, Name: "Sprint 1", State: "active", Start: "2026-10-05T08:00:00.000Z", End: "2026-10-19T08:00:00.000Z", Goal: "Ship login for Alice Smith"},
		{ID: 12, Board: 1, Name: "Sprint 2", State: "future"},
		{ID: 10, Board: 1, Name: "Sprint 0", State: "closed"},
	}
	f.Issues["PROJ-1"].Sprint = 11
	f.Issues["PROJ-2"].Sprint = 11
	f.AddIssue(jiratest.Issue{Key: "PROJ-5", Summary: "Backlog item", Assignee: "acc-me"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-6", Summary: "Alice's backlog item", Assignee: "acc-alice"})
	return f
}

func TestBoardsAndSprints(t *testing.T) {
	t.Parallel()
	f := agileFixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	bs, err := s.Boards(ctx, "")
	if err != nil || len(bs) != 3 {
		t.Fatalf("boards = %+v, %v", bs, err)
	}
	assertNoPersonalData(t, bs)
	if bs[0].Project != "PROJ" || bs[2].Project != "" {
		t.Errorf("boards = %+v", bs)
	}
	if bs, _ := s.Boards(ctx, "proj"); len(bs) != 2 {
		t.Errorf("boards of PROJ = %+v", bs)
	}

	ss, err := s.Sprints(ctx, "proj board")
	if err != nil || len(ss) != 2 || ss[0].Name != "Sprint 1" || ss[1].State != "future" {
		t.Errorf("sprints = %+v, %v", ss, err)
	}
	assertNoPersonalData(t, ss)

	for ref, want := range map[string]string{
		"PROJ":    "ambiguous",
		"nothing": "no board named",
		"flow":    "has no sprints",
		"":        "name a board",
	} {
		if _, err := s.Sprints(ctx, ref); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", ref, err, want)
		}
	}

	gs, err := s.ActiveSprints(ctx, "1", 0)
	if err != nil || len(gs) != 1 {
		t.Fatalf("active = %+v, %v", gs, err)
	}
	g := gs[0]
	if g.Sprint.ID != 11 || g.Total != 2 || len(g.Issues) != 1 || g.Issues[0].Key != "PROJ-1" {
		t.Errorf("active sprint = %+v", g)
	}
	assertNoPersonalData(t, gs)

	bl, err := s.Backlog(ctx, "PROJ board", 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, i := range bl.Issues {
		keys = append(keys, i.Key)
	}
	if strings.Join(keys, ",") != "PROJ-5" || bl.Total != 3 {
		t.Errorf("backlog = %v, total %d", keys, bl.Total)
	}
}

func TestBoardIssuesAreRecheckedPerIssue(t *testing.T) {
	t.Parallel()
	f := agileFixture(t)
	f.UnscopedSearch = true
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	gs, err := s.ActiveSprints(ctx, "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	bl, err := s.Backlog(ctx, "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal([]any{gs, bl})
	if strings.Contains(string(b), "PROJ-2") || strings.Contains(string(b), "PROJ-6") || strings.Contains(string(b), "Alice") {
		t.Errorf("other people's tickets leaked: %s", b)
	}

	off := service(t, f, config.PrivacyOff)
	gs, err = off.ActiveSprints(ctx, "1", 0)
	if err != nil || len(gs[0].Issues) != 2 {
		t.Errorf("off mode = %+v, %v", gs, err)
	}
}

func TestSprintsOnBoardWithoutSprints(t *testing.T) {
	t.Parallel()
	f := agileFixture(t)
	f.Boards = append(f.Boards, jiratest.Board{ID: 4, Name: "Team board", Type: "simple", Project: "PROJ"})
	s := service(t, f, config.PrivacyOwn)
	if _, err := s.ActiveSprints(context.Background(), "4", 0); err == nil || !strings.Contains(err.Error(), "has no sprints") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanMoveSprint(t *testing.T) {
	t.Parallel()
	f := agileFixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	p, err := s.PlanMoveSprint(ctx, "PROJ-1", "sprint 2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || !strings.Contains(p.Description, "from sprint Sprint 1 to sprint Sprint 2 (future)") {
		t.Errorf("description = %q", p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-1"].Sprint != 12 {
		t.Errorf("sprint = %d", f.Issues["PROJ-1"].Sprint)
	}

	p, err = s.PlanMoveSprint(ctx, "PROJ-1", "Backlog")
	if err != nil || !strings.Contains(p.Description, "from sprint Sprint 2 to the backlog") {
		t.Fatalf("to backlog: %+v, %v", p, err)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-1"].Sprint != 0 {
		t.Errorf("sprint after backlog = %d", f.Issues["PROJ-1"].Sprint)
	}

	p, err = s.PlanMoveSprint(ctx, "PROJ-1", "11")
	if err != nil || !strings.Contains(p.Description, "from the backlog to sprint Sprint 1 (active)") {
		t.Fatalf("by ID: %+v, %v", p, err)
	}

	for _, tc := range []struct{ key, target, want string }{
		{"PROJ-1", "backlog", "already in the backlog"},
		{"PROJ-1", "10", "is closed"},
		{"PROJ-1", "Sprint 0", "choose one of: Sprint 1, Sprint 2"},
		{"PROJ-1", "", "name a sprint"},
		{"PROJ-2", "Sprint 1", ErrNotFound.Error()},
	} {
		if _, err := s.PlanMoveSprint(ctx, tc.key, tc.target); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s -> %q: err = %v, want %q", tc.key, tc.target, err, tc.want)
		}
	}
	f.Issues["PROJ-1"].Sprint = 11
	if _, err := s.PlanMoveSprint(ctx, "PROJ-1", "Sprint 1"); err == nil || !strings.Contains(err.Error(), "already in sprint") {
		t.Errorf("same sprint: %v", err)
	}
}

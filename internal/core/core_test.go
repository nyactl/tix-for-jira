package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/jira"
	"github.com/nyactl/tix-jira/internal/jiratest"
)

// fixture builds a site where Max (the user) has PROJ-1 and PROJ-3, Alice
// owns PROJ-2 and PROJ-4 (a subtask of PROJ-1), and PROJ-1 blocks PROJ-2.
func fixture(t *testing.T) *jiratest.Fake {
	t.Helper()
	f := jiratest.New(t)
	f.AddIssue(jiratest.Issue{
		Key: "PROJ-1", Summary: "Fix login for Alice Smith", Assignee: "acc-me", Reporter: "acc-alice", Priority: "High",
		Labels:      []string{"auth"},
		Description: jiratest.Doc("Reported by {acc-alice}; ask Alice Smith or alice@example.com."),
		Custom: map[string]any{
			"customfield_10016": 5,
			"customfield_10050": map[string]any{"id": "1", "value": "Alpha"},
			"customfield_10060": "acc-bob",
		},
		Comments: []jiratest.Comment{
			{ID: "1", Author: "acc-alice", Created: "2026-10-02T10:00:00.000+0000", Body: jiratest.Doc("Bob Jones knows more")},
			{ID: "2", Author: "acc-bob", Created: "2026-10-03T10:00:00.000+0000", Body: jiratest.Doc("ping {acc-me}")},
		},
		Changes: []jiratest.Change{
			{Author: "acc-alice", Created: "2026-10-02T09:00:00.000+0000", Field: "assignee", FieldType: "jira", From: "acc-bob", FromS: "Bob Jones", To: "acc-me", ToS: "Max Mustermann"},
			{Author: "acc-me", Created: "2026-10-04T09:00:00.000+0000", Field: "status", FieldType: "jira", FromS: "To Do", ToS: "In Progress"},
		},
		Worklogs:    []jiratest.Worklog{{ID: "7", Author: "acc-alice", Started: "2026-10-02T08:00:00.000+0000", Seconds: 1800, Comment: jiratest.Doc("pairing")}},
		Attachments: []jiratest.Attachment{{ID: "501", Filename: "../../etc/log.txt", Author: "acc-alice", MimeType: "text/plain", Data: []byte("stack trace")}},
	})
	f.AddIssue(jiratest.Issue{Key: "PROJ-2", Summary: "Alice's secret project", Assignee: "acc-alice", Reporter: "acc-alice",
		Attachments: []jiratest.Attachment{{ID: "502", Filename: "secret.txt", Author: "acc-alice", MimeType: "text/plain", Data: []byte("secret")}}})
	f.AddIssue(jiratest.Issue{Key: "PROJ-3", Summary: "Done thing", Assignee: "acc-me", Status: "Done"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-4", Summary: "Alice's subtask", Assignee: "acc-alice", Parent: "PROJ-1", Type: "Subtask"})
	f.Links = append(f.Links, jiratest.Link{ID: "900", Type: "Blocks", Outward: "PROJ-1", Inward: "PROJ-2"})
	return f
}

func service(t *testing.T, f *jiratest.Fake, mode config.PrivacyMode) *Service {
	t.Helper()
	s, err := New(context.Background(), jira.New(f.URL(), "me@example.com", "tok-me", "test"), mode)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }
	return s
}

// assertNoPersonalData fails if v mentions anyone else's identity.
func assertNoPersonalData(t *testing.T, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	s := string(b)
	for _, bad := range []string{"acc-alice", "acc-bob", "Alice", "Bob", "alice@", "bob@", "avatar", "secret"} {
		if strings.Contains(s, bad) {
			t.Errorf("output contains %q: %s", bad, s)
		}
	}
}

func TestMyIssues(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	open, err := s.MyIssues(ctx, MyIssuesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Key != "PROJ-1" || open[0].Summary != "Fix login for Person A" {
		t.Errorf("open = %+v", open)
	}
	all, _ := s.MyIssues(ctx, MyIssuesOptions{IncludeDone: true, Since: "-7d", Project: "proj", Text: `say "hi"`})
	if len(all) != 2 {
		t.Errorf("all = %+v", all)
	}
	jql := f.JQL[len(f.JQL)-1]
	want := `assignee = currentUser() AND updated >= "-7d" AND project = PROJ AND text ~ "say \"hi\"" ORDER BY updated DESC`
	if jql != want {
		t.Errorf("jql = %s", jql)
	}
	if _, err := s.MyIssues(ctx, MyIssuesOptions{Since: "yesterday"}); err == nil {
		t.Error("invalid since accepted")
	}
}

func TestSearchIsScopedEvenIfJQLEscapes(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.UnscopedSearch = true
	s := service(t, f, config.PrivacyOwn)
	got, err := s.Search(context.Background(), "x) OR (project = PROJ", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range got {
		if i.Key == "PROJ-2" || i.Key == "PROJ-4" {
			t.Errorf("out-of-scope issue %s returned", i.Key)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d issues, want PROJ-1 and PROJ-3", len(got))
	}
	if !strings.HasPrefix(f.JQL[0], "assignee = currentUser() AND (") {
		t.Errorf("jql not scoped: %s", f.JQL[0])
	}
}

func TestIssueOwnMode(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	if _, err := s.Issue(ctx, "PROJ-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("PROJ-2: %v", err)
	}
	if _, err := s.Issue(ctx, "PROJ-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("PROJ-99: %v", err)
	}
	d, err := s.Issue(ctx, "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	assertNoPersonalData(t, d)
	if d.Assignee != "Me" || d.Reporter != "Person A" {
		t.Errorf("people = %q / %q", d.Assignee, d.Reporter)
	}
	if d.Description != "Reported by @[Person A]; ask Person A or Person A." {
		t.Errorf("description = %q", d.Description)
	}
	fields := map[string]string{}
	for _, fv := range d.Fields {
		fields[fv.Name] = fv.Value
	}
	if fields["Story Points"] != "5" || fields["Team"] != "Alpha" || fields["Reviewer"] != "Person B" {
		t.Errorf("custom fields = %v", fields)
	}
	if len(d.Subtasks) != 1 || d.Subtasks[0].Key != "PROJ-4" || d.Subtasks[0].Summary != "" {
		t.Errorf("subtasks = %+v", d.Subtasks)
	}
	if len(d.Links) != 1 || d.Links[0].Relation != "blocks" || d.Links[0].Key != "PROJ-2" || d.Links[0].Summary != "" {
		t.Errorf("links = %+v", d.Links)
	}
	if len(d.Attachments) != 1 || d.Attachments[0].Author != "Person A" {
		t.Errorf("attachments = %+v", d.Attachments)
	}
	if d.URL != f.URL()+"/browse/PROJ-1" {
		t.Errorf("url = %s", d.URL)
	}
}

func TestIssueOffMode(t *testing.T) {
	t.Parallel()
	s := service(t, fixture(t), config.PrivacyOff)
	d, err := s.Issue(context.Background(), "PROJ-2")
	if err != nil {
		t.Fatal(err)
	}
	if d.Assignee != "Alice Smith" {
		t.Errorf("assignee = %q", d.Assignee)
	}
	d1, _ := s.Issue(context.Background(), "PROJ-1")
	if d1.Links[0].Summary != "Alice's secret project" {
		t.Errorf("link summary hidden in off mode: %+v", d1.Links[0])
	}
}

func TestCommentsHistoryWorklogs(t *testing.T) {
	t.Parallel()
	s := service(t, fixture(t), config.PrivacyOwn)
	ctx := context.Background()

	cs, err := s.Comments(ctx, "PROJ-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPersonalData(t, cs)
	if cs[0].Author != "Person A" || cs[0].Body != "Person B knows more" || cs[1].Author != "Person B" || cs[1].Body != "ping @[Me]" {
		t.Errorf("comments = %+v", cs)
	}

	hist, err := s.History(ctx, "PROJ-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPersonalData(t, hist)
	if hist[0].Changes[0].From != "Person B" || hist[0].Changes[0].To != "Me" || hist[1].Changes[0].To != "In Progress" {
		t.Errorf("history = %+v", hist)
	}
	recent, _ := s.History(ctx, "PROJ-1", "-5d", 10)
	if len(recent) != 1 {
		t.Errorf("since filter: %+v", recent)
	}

	ws, err := s.Worklogs(ctx, "PROJ-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ws[0].Author != "Person A" || ws[0].TimeSpent != "30m" || ws[0].Comment != "pairing" {
		t.Errorf("worklogs = %+v", ws)
	}

	for _, call := range []func() error{
		func() error { _, err := s.Comments(ctx, "PROJ-2", 10); return err },
		func() error { _, err := s.History(ctx, "PROJ-2", "", 10); return err },
		func() error { _, err := s.Worklogs(ctx, "PROJ-2", 10); return err },
		func() error { _, err := s.Transitions(ctx, "PROJ-2"); return err },
		func() error { _, err := s.EditableFields(ctx, "PROJ-2"); return err },
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("out-of-scope read: %v", err)
		}
	}
}

func TestAttachment(t *testing.T) {
	t.Parallel()
	s := service(t, fixture(t), config.PrivacyOwn)
	dir := t.TempDir()
	a, err := s.Attachment(context.Background(), "PROJ-1", "501", dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Data) != "stack trace" || a.Filename != "log.txt" {
		t.Errorf("attachment = %+v data=%q", a, a.Data)
	}
	if rel, _ := filepath.Rel(dir, a.Path); strings.HasPrefix(rel, "..") {
		t.Errorf("path escapes dir: %s", a.Path)
	}
	if info, err := os.Stat(a.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", info, err)
	}
	if _, err := s.Attachment(context.Background(), "PROJ-2", "502", dir); !errors.Is(err, ErrNotFound) {
		t.Errorf("out-of-scope attachment: %v", err)
	}
	if _, err := s.Attachment(context.Background(), "PROJ-1", "502", dir); err == nil {
		t.Error("attachment of another ticket accepted")
	}
}

func TestPlanCommentResolvesMentions(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	if _, err := s.Issue(ctx, "PROJ-1"); err != nil { // learn Person A
		t.Fatal(err)
	}
	p, err := s.PlanComment(ctx, "PROJ-1", "Thanks @[Person A], **done**.")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Additive || p.Key != "PROJ-1" || !strings.Contains(p.Description, "Thanks @[Person A], **done**.") {
		t.Errorf("plan = %+v", p)
	}
	if len(f.Issues["PROJ-1"].Comments) != 2 {
		t.Fatal("plan must not write before Apply")
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx); err == nil {
		t.Error("plan applied twice")
	}
	body := string(f.Issues["PROJ-1"].Comments[2].Body)
	if !strings.Contains(body, `"id":"acc-alice"`) || !strings.Contains(body, `"type":"mention"`) {
		t.Errorf("stored comment = %s", body)
	}

	if _, err := s.PlanComment(ctx, "PROJ-1", "hi @[Person Q]"); err == nil {
		t.Error("unknown mention accepted")
	}
	if _, err := s.PlanComment(ctx, "PROJ-2", "hi"); !errors.Is(err, ErrNotFound) {
		t.Errorf("comment on out-of-scope ticket: %v", err)
	}
}

func TestPlanUpdate(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	p, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{
		{"Summary", "Fix login"},
		{"labels", "auth, urgent"},
		{"Story Points", "8"},
		{"team", "beta"},
		{"Description", "## New\n\n- item"},
		{"priority", "low"},
		{"Due date", "2026-10-31"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || !strings.Contains(p.Description, `from: "5"`) || !strings.Contains(p.Description, `to:   "8"`) {
		t.Errorf("description:\n%s", p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	i := f.Issues["PROJ-1"]
	if i.Summary != "Fix login" || strings.Join(i.Labels, ",") != "auth,urgent" || i.Priority != "" && i.Priority != "Low" || i.DueDate != "2026-10-31" {
		t.Errorf("issue = %+v", i)
	}
	if i.Custom["customfield_10016"] != float64(8) || i.Custom["customfield_10050"].(map[string]any)["id"] != "2" {
		t.Errorf("custom = %v", i.Custom)
	}
	if !strings.Contains(string(i.Description), `"type":"heading"`) {
		t.Errorf("description = %s", i.Description)
	}

	for _, tc := range []struct {
		in   FieldInput
		want string
	}{
		{FieldInput{"Reviewer", "Person A"}, "cannot be changed"},
		{FieldInput{"Nope", "x"}, "not editable"},
		{FieldInput{"Story Points", "many"}, "needs a number"},
		{FieldInput{"Team", "Gamma"}, "choose one of: Alpha, Beta"},
		{FieldInput{"Labels", "has space"}, "must not contain spaces"},
		{FieldInput{"Summary", ""}, ""},
	} {
		_, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{tc.in})
		if tc.want == "" {
			if err != nil {
				t.Errorf("%v: %v", tc.in, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.in, err, tc.want)
		}
	}
	if _, err := s.PlanUpdate(ctx, "PROJ-2", []FieldInput{{"Summary", "x"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update out of scope: %v", err)
	}
}

func TestPlanTransition(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	p, err := s.PlanTransition(ctx, "PROJ-1", "in progress", "Starting now")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || !strings.Contains(p.Description, `from "To Do" to "In Progress"`) {
		t.Errorf("description = %s", p.Description)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-1"].Status != "In Progress" || len(f.Issues["PROJ-1"].Comments) != 3 {
		t.Errorf("status=%s comments=%d", f.Issues["PROJ-1"].Status, len(f.Issues["PROJ-1"].Comments))
	}
	if _, err := s.PlanTransition(ctx, "PROJ-1", "Archived", ""); err == nil || !strings.Contains(err.Error(), `"Done" (to Done)`) {
		t.Errorf("err = %v", err)
	}
}

func TestPlanCreate(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	p, err := s.PlanCreate(ctx, CreateInput{Project: "proj", Type: "task", Summary: "New work", Description: "Details", Labels: []string{"x"}, Priority: "High"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := p.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "Created PROJ-5") {
		t.Errorf("msg = %s", msg)
	}
	if got := f.Issues["PROJ-5"]; got.Assignee != "acc-me" || got.Type != "Task" {
		t.Errorf("created = %+v", got)
	}

	sub, err := s.PlanCreate(ctx, CreateInput{Project: "PROJ", Type: "Subtask", Summary: "Part", Parent: "PROJ-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-6"].Parent != "PROJ-1" {
		t.Errorf("subtask parent = %q", f.Issues["PROJ-6"].Parent)
	}

	for _, tc := range []struct {
		in   CreateInput
		want string
	}{
		{CreateInput{Project: "PROJ", Type: "Subtask", Summary: "x"}, "needs a parent"},
		{CreateInput{Project: "PROJ", Type: "Subtask", Summary: "x", Parent: "PROJ-2"}, "outside your privacy scope"},
		{CreateInput{Project: "PROJ", Type: "Epic", Summary: "x"}, "choose one of: Task, Bug, Subtask"},
		{CreateInput{Project: "PROJ", Type: "Task", Summary: "two\nlines"}, "single non-empty line"},
		{CreateInput{Project: "PROJ", Type: "Task", Summary: "x", Fields: map[string]string{"Story Points": "3"}}, "not on the create screen"},
	} {
		if _, err := s.PlanCreate(ctx, tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.in, err, tc.want)
		}
	}
}

func TestPlanWorklog(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	p, err := s.PlanWorklog(ctx, "PROJ-1", "1h 30m", "09:30", "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, "Log 1h 30m on PROJ-1") || !strings.Contains(p.Description, "started 2026-10-08 09:30") {
		t.Errorf("description = %s", p.Description)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	wl := f.Issues["PROJ-1"].Worklogs[1]
	if wl.Seconds != 5400 || wl.Started != "2026-10-08T09:30:00.000+0000" {
		t.Errorf("worklog = %+v", wl)
	}
	if _, err := s.PlanWorklog(ctx, "PROJ-1", "1h", "23:00", ""); err == nil {
		t.Error("future start accepted")
	}
	if _, err := s.PlanWorklog(ctx, "PROJ-2", "1h", "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("worklog out of scope: %v", err)
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	ok := map[string]int{"1h 30m": 5400, "1h30m": 5400, "90m": 5400, "1.5h": 5400, "2h": 7200, "1m": 60, " 45m ": 2700}
	for in, want := range ok {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "1d", "1w", "0m", "25h", "abc", "1h 30"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) accepted", in)
		}
	}
}

func TestPlanLink(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	p, err := s.PlanLink(ctx, "PROJ-1", "is blocked by", "PROJ-3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, "is blocked by PROJ-3") {
		t.Errorf("description = %s", p.Description)
	}
	msg, err := p.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := f.Links[len(f.Links)-1]
	if last.Outward != "PROJ-3" || last.Inward != "PROJ-1" || msg != "Linked: PROJ-1 is blocked by PROJ-3." {
		t.Errorf("link = %+v msg = %q", last, msg)
	}

	if _, err := s.PlanLink(ctx, "PROJ-1", "blocks", "PROJ-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("link to out-of-scope ticket: %v", err)
	}
	if _, err := s.PlanLink(ctx, "PROJ-1", "is blocked by", "PROJ-3"); err == nil || !strings.Contains(err.Error(), "already linked") {
		t.Errorf("duplicate link: %v", err)
	}
	if _, err := s.PlanLink(ctx, "PROJ-1", "loves", "PROJ-3"); err == nil || !strings.Contains(err.Error(), "use one of") {
		t.Errorf("unknown relation: %v", err)
	}
}

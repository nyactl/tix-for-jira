package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
	"github.com/nyactl/tix-for-jira/internal/jiratest"
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
	s, err := New(context.Background(), jira.New(f.URL(), "me@example.com", "tok-me", "test", atlassian.WithTransport(f.Transport())), mode)
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
		{CreateInput{Project: "PROJ", Type: "Initiative", Summary: "x"}, "choose one of: Task, Bug, Subtask, Epic"},
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

func TestPlanUnlink(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.Links = append(f.Links, jiratest.Link{ID: "901", Type: "Relates", Outward: "PROJ-1", Inward: "PROJ-2"})
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	for _, tc := range []struct{ key, other, relation, want string }{
		{"PROJ-1", "PROJ-2", "", "more than once; give the relation: blocks, relates to"},
		{"PROJ-1", "PROJ-2", "is blocked by", `not linked to PROJ-2 with "is blocked by"; it blocks and relates to PROJ-2`},
		{"PROJ-1", "PROJ-3", "", "PROJ-1 is not linked to PROJ-3"},
		{"PROJ-2", "PROJ-1", "", ErrNotFound.Error()},
		{"PROJ-1", "nope", "", "invalid"},
	} {
		if _, err := s.PlanUnlink(ctx, tc.key, tc.other, tc.relation); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s %q: err = %v, want %q", tc.key, tc.other, tc.relation, err, tc.want)
		}
	}

	// The other ticket is outside the scope; it is named as on PROJ-1.
	p, err := s.PlanUnlink(ctx, "PROJ-1", "proj-2", "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || p.Description != "Remove the link: PROJ-1 (Fix login for Person A) blocks PROJ-2 (Task, To Do)." || strings.Contains(p.Description, "secret") {
		t.Errorf("plan = %s %q", p.Kind, p.Description)
	}
	assertNoPersonalData(t, p.Description)
	msg, err := p.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "Removed the link: PROJ-1 blocks PROJ-2." || len(f.Links) != 1 || f.Links[0].ID != "901" {
		t.Errorf("msg = %q links = %+v", msg, f.Links)
	}
	if p, err = s.PlanUnlink(ctx, "PROJ-1", "PROJ-2", ""); err != nil || !strings.Contains(p.Description, "relates to PROJ-2") {
		t.Errorf("single remaining link: %v", err)
	}
}

func TestCommentsAndWorklogsKeepTheNewest(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	i := f.Issues["PROJ-1"]
	for n := 3; n <= 8; n++ {
		i.Comments = append(i.Comments, jiratest.Comment{ID: strconv.Itoa(n), Author: "acc-me", Created: fmt.Sprintf("2026-10-0%dT10:00:00.000+0000", min(n, 9)), Body: jiratest.Doc("c" + strconv.Itoa(n))})
		i.Worklogs = append(i.Worklogs, jiratest.Worklog{ID: strconv.Itoa(100 + n), Author: "acc-me", Started: "2026-10-05T08:00:00.000+0000", Seconds: n * 60})
	}
	s := service(t, f, config.PrivacyOwn)
	cs, err := s.Comments(context.Background(), "PROJ-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 || cs[0].Body != "c6" || cs[2].Body != "c8" {
		t.Errorf("comments = %+v, want c6..c8 in order", cs)
	}
	ws, err := s.Worklogs(context.Background(), "PROJ-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[1].ID != "108" {
		t.Errorf("worklogs = %+v, want the newest two", ws)
	}
}

func TestIssueShowsVersionsAndEnvironment(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	i := f.Issues["PROJ-1"]
	i.FixVersions = []string{"2.1", "2.2"}
	i.Versions = []string{"2.0"}
	i.Environment = jiratest.Doc("macOS 15, reported by Alice Smith")
	d, err := service(t, f, config.PrivacyOwn).Issue(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.FixVersions, ",") != "2.1,2.2" || strings.Join(d.Affects, ",") != "2.0" {
		t.Errorf("versions = %v / %v", d.FixVersions, d.Affects)
	}
	if d.Environment != "macOS 15, reported by Person A" {
		t.Errorf("environment = %q", d.Environment)
	}
}

func TestPlanUpdateParent(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	fs, _ := s.EditableFields(ctx, "PROJ-3")
	hasParent := false
	for _, fv := range fs {
		hasParent = hasParent || fv.ID == "parent"
	}
	if !hasParent {
		t.Errorf("parent not listed as editable: %+v", fs)
	}

	p, err := s.PlanUpdate(ctx, "PROJ-3", []FieldInput{{"Parent", "proj-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || !strings.Contains(p.Description, "from: (empty)") || !strings.Contains(p.Description, `to:   "PROJ-1 (Fix login for Person A)"`) {
		t.Errorf("description:\n%s", p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-3"].Parent != "PROJ-1" {
		t.Errorf("parent = %q", f.Issues["PROJ-3"].Parent)
	}

	for _, tc := range []struct {
		value, want string
	}{
		{"PROJ-2", "outside your privacy scope"},
		{"PROJ-3", "its own parent"},
		{"nope", "invalid issue key"},
	} {
		_, err := s.PlanUpdate(ctx, "PROJ-3", []FieldInput{{"parent", tc.value}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parent %q: err = %v, want %q", tc.value, err, tc.want)
		}
	}
	if n := len(f.Requests); n == 0 {
		t.Fatal("no requests recorded")
	}
	if f.Issues["PROJ-3"].Parent != "PROJ-1" {
		t.Error("a refused change modified the parent")
	}
}

func TestPlanUpdateEstimates(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	i := f.Issues["PROJ-1"]
	i.OriginalEstimate, i.RemainingEstimate = "4h", "1h"
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	fs, err := s.EditableFields(ctx, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range fs {
		names[f.Name] = true
	}
	if !names["Original estimate"] || !names["Remaining estimate"] {
		t.Errorf("editable fields = %+v", fs)
	}

	// Changing only the original estimate must keep the remaining estimate.
	p, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{{"Original estimate", "6h 30m"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || !strings.Contains(p.Description, `from: "4h"`) || !strings.Contains(p.Description, `to:   "6h 30m"`) {
		t.Errorf("description:\n%s", p.Description)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if i.OriginalEstimate != "6h 30m" || i.RemainingEstimate != "1h" {
		t.Errorf("estimates = %q / %q, want 6h 30m / 1h", i.OriginalEstimate, i.RemainingEstimate)
	}

	p, err = s.PlanUpdate(ctx, "PROJ-1", []FieldInput{{"time remaining", "90m"}, {"originalEstimate", "40h"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if i.OriginalEstimate != "40h" || i.RemainingEstimate != "1h 30m" {
		t.Errorf("estimates = %q / %q", i.OriginalEstimate, i.RemainingEstimate)
	}
	d, _ := s.Issue(ctx, "PROJ-1")
	if d.TimeTracking == nil || d.TimeTracking.Original != "40h" || d.TimeTracking.Remaining != "1h 30m" {
		t.Errorf("time tracking view = %+v", d.TimeTracking)
	}

	for _, bad := range []FieldInput{{"Original estimate", "2d"}, {"Remaining estimate", ""}, {"Original estimate", "lots"}} {
		if _, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{bad}); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{{"Estimate", "1h"}, {"original estimate", "2h"}}); err == nil {
		t.Error("estimate listed twice accepted")
	}
}

func TestLooseName(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"Sub-task", "subtask"}, {"Sub task", "SUBTASK"}, {"Story", " story "}} {
		if looseName(pair[0]) != looseName(pair[1]) {
			t.Errorf("%q and %q should match", pair[0], pair[1])
		}
	}
	if looseName("Task") == looseName("Subtask") {
		t.Error("Task and Subtask must differ")
	}
}

func TestPlanUpdateFieldTypes(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	p, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{
		{"Region", "europe / france"},
		{"Docs link", "https://example.com/spec"},
		{"Delivery team", "platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, fv := range d.Fields {
		got[fv.Name] = fv.Value
	}
	if got["Region"] != "Europe / France" || got["Docs link"] != "https://example.com/spec" || got["Delivery team"] != "Platform" {
		t.Errorf("fields = %v", got)
	}

	if _, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{{"Region", "Europe"}}); err != nil {
		t.Errorf("region parent only: %v", err)
	}
	if _, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{{"Delivery team", "1C0B3A4D-2E5F-4A6B-9C7D-8E9F0A1B2C3D"}}); err != nil {
		t.Errorf("team by ID: %v", err)
	}
	for _, tc := range []struct {
		in   FieldInput
		want string
	}{
		{FieldInput{"Region", "Atlantis"}, "choose one of: Europe / Germany"},
		{FieldInput{"Region", "Europe / USA"}, "Europe / Germany"},
		{FieldInput{"Docs link", "javascript:alert(1)"}, "http or https"},
		{FieldInput{"Docs link", "example.com"}, "http or https"},
		{FieldInput{"Delivery team", "Plat"}, "similar: "},
		{FieldInput{"Delivery team", "Nobody"}, "no team named"},
	} {
		_, err := s.PlanUpdate(ctx, "PROJ-1", []FieldInput{tc.in})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.in, err, tc.want)
		}
	}

	fields, err := s.EditableFields(ctx, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, ef := range fields {
		if ef.Name == "Region" && strings.Join(ef.Allowed, ",") != "Europe / Germany,Europe / France,Americas / USA" {
			t.Errorf("region allowed = %v", ef.Allowed)
		}
	}
}

func TestPlanTake(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-9", Summary: "Unassigned work for Alice Smith", Reporter: "acc-alice"})
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	if _, err := s.Issue(ctx, "PROJ-9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassigned ticket visible before take: %v", err)
	}
	p, err := s.PlanTake(ctx, "proj-9")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || p.Key != "PROJ-9" || !strings.Contains(p.Description, "Assign PROJ-9 (") || strings.Contains(p.Description, "Unassigned work") {
		t.Errorf("description = %q", p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-9"].Assignee != "acc-me" {
		t.Errorf("assignee = %q", f.Issues["PROJ-9"].Assignee)
	}
	if _, err := s.Issue(ctx, "PROJ-9"); err != nil {
		t.Errorf("taken ticket not in scope: %v", err)
	}

	for key, want := range map[string]string{
		"PROJ-9":   "already assigned to you",
		"PROJ-2":   "assigned to someone else",
		"PROJ-404": ErrNotFound.Error(),
	} {
		if _, err := s.PlanTake(ctx, key); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", key, err, want)
		}
	}

	off := service(t, f, config.PrivacyOff)
	f.AddIssue(jiratest.Issue{Key: "PROJ-10", Summary: "Shared chore"})
	p, err = off.PlanTake(ctx, "PROJ-10")
	if err != nil || !strings.Contains(p.Description, "Shared chore") {
		t.Errorf("off mode: %v, %v", p, err)
	}
}

func TestPlanWatch(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.Issues["PROJ-1"].Watchers = []string{"acc-alice"}
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	d, err := s.Issue(ctx, "PROJ-1")
	if err != nil || d.Watchers != 1 || d.Watching {
		t.Fatalf("before: %+v, %v", d, err)
	}
	if _, err := s.PlanWatch(ctx, "PROJ-1", false); err == nil || !strings.Contains(err.Error(), "do not watch") {
		t.Errorf("unwatch when not watching: %v", err)
	}
	p, err := s.PlanWatch(ctx, "PROJ-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Additive || !strings.HasPrefix(p.Description, "Watch PROJ-1 (") {
		t.Errorf("plan = %+v", p)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Issue(ctx, "PROJ-1")
	if d.Watchers != 2 || !d.Watching {
		t.Errorf("after watch: watchers=%d watching=%v", d.Watchers, d.Watching)
	}
	if _, err := s.PlanWatch(ctx, "PROJ-1", true); err == nil || !strings.Contains(err.Error(), "already watch") {
		t.Errorf("watch twice: %v", err)
	}
	p, err = s.PlanWatch(ctx, "PROJ-1", false)
	if err != nil || p.Kind != Destructive {
		t.Fatalf("unwatch plan: %+v, %v", p, err)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if w := f.Issues["PROJ-1"].Watchers; len(w) != 1 || w[0] != "acc-alice" {
		t.Errorf("watchers = %v, want only the other watcher left", w)
	}
	if _, err := s.PlanWatch(ctx, "PROJ-2", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("watch out of scope: %v", err)
	}
}

func TestPlanAttach(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "notes for review.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.PlanAttach(ctx, "PROJ-1", path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Additive || !strings.Contains(p.Description, "Attach notes for review.txt (5 bytes, from "+path+")") {
		t.Errorf("description = %q", p.Description)
	}
	// The plan uploads what was approved, not what the file holds later.
	if err := os.WriteFile(path, []byte("changed after approval"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	as := f.Issues["PROJ-1"].Attachments
	if last := as[len(as)-1]; last.Filename != "notes for review.txt" || string(last.Data) != "hello" {
		t.Errorf("attachment = %+v", last)
	}

	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, maxAttachmentBytes+1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, path, want string }{
		{"PROJ-1", big, "the limit is 50.0 MiB"},
		{"PROJ-1", dir, "not a regular file"},
		{"PROJ-1", filepath.Join(dir, "missing"), "no such file"},
		{"PROJ-2", path, ErrNotFound.Error()},
	} {
		if _, err := s.PlanAttach(ctx, tc.key, tc.path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s: err = %v, want %q", tc.key, tc.path, err, tc.want)
		}
	}
	if n := len(f.Issues["PROJ-1"].Attachments); n != len(as) {
		t.Errorf("refused uploads changed the ticket: %d attachments", n)
	}
}

func TestWebLinks(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.Issues["PROJ-1"].RemoteLinks = []jiratest.RemoteLink{
		{ID: 1, URL: "https://wiki.example.com/spec", Title: "Spec by Alice Smith"},
		{ID: 2, URL: "javascript:alert(1)", Title: "trap"},
	}
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	d, err := s.Issue(ctx, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.WebLinks) != 1 || d.WebLinks[0].URL != "https://wiki.example.com/spec" || d.WebLinks[0].Title != "Spec by Person A" {
		t.Errorf("web links = %+v", d.WebLinks)
	}

	p, err := s.PlanWebLink(ctx, "PROJ-1", "https://ci.example.com/run/7", "CI run")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Additive || !strings.Contains(p.Description, "CI run\n    https://ci.example.com/run/7") {
		t.Errorf("description = %q", p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := s.PlanWebLink(ctx, "PROJ-1", "https://ci.example.com/run/8", ""); err != nil || !strings.Contains(p.Description, "https://ci.example.com/run/8\n    https://ci.example.com/run/8") {
		t.Errorf("default title: %+v, %v", p, err)
	}
	d, _ = s.Issue(ctx, "PROJ-1")
	if len(d.WebLinks) != 2 || d.WebLinks[1].Title != "CI run" {
		t.Errorf("after add = %+v", d.WebLinks)
	}

	for _, tc := range []struct{ key, url, title, want string }{
		{"PROJ-1", "javascript:alert(1)", "", "http or https"},
		{"PROJ-1", "ci.example.com/run/7", "", "http or https"},
		{"PROJ-1", "https://ci.example.com/run/7", "again", "already links"},
		{"PROJ-1", "https://x.example.com", strings.Repeat("a", 256), "longer than 255"},
		{"PROJ-2", "https://x.example.com", "", ErrNotFound.Error()},
	} {
		if _, err := s.PlanWebLink(ctx, tc.key, tc.url, tc.title); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s: err = %v, want %q", tc.key, tc.url, err, tc.want)
		}
	}

	for _, tc := range []struct{ key, url, want string }{
		{"PROJ-1", "https://ci.example.com/run/9", "PROJ-1 has no web link to https://ci.example.com/run/9"},
		{"PROJ-1", "javascript:alert(1)", "no web link"},
		{"PROJ-2", "https://wiki.example.com/spec", ErrNotFound.Error()},
	} {
		if _, err := s.PlanRemoveWebLink(ctx, tc.key, tc.url); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("remove %s %s: err = %v, want %q", tc.key, tc.url, err, tc.want)
		}
	}
	p, err = s.PlanRemoveWebLink(ctx, "PROJ-1", " https://wiki.example.com/spec ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Destructive || !strings.Contains(p.Description, "Remove the web link from PROJ-1 (Fix login for Person A):\n\n    Spec by Person A\n    https://wiki.example.com/spec") {
		t.Errorf("plan = %s %q", p.Kind, p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Issue(ctx, "PROJ-1")
	if len(d.WebLinks) != 1 || d.WebLinks[0].Title != "CI run" || len(f.Issues["PROJ-1"].RemoteLinks) != 2 {
		t.Errorf("after remove = %+v", d.WebLinks)
	}
}

func TestStatusTimes(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-7", Summary: "Flow", Assignee: "acc-me", Status: "In Progress", Created: "2026-10-01T09:00:00.000+0000",
		Changes: []jiratest.Change{
			{Author: "acc-alice", Created: "2026-10-02T09:00:00.000+0000", Field: "status", FieldType: "jira", FromS: "To Do", ToS: "In Progress"},
			{Author: "acc-me", Created: "2026-10-03T09:00:00.000+0000", Field: "labels", FieldType: "jira", ToS: "x"},
			{Author: "acc-me", Created: "2026-10-04T09:00:00.000+0000", Field: "status", FieldType: "jira", FromS: "In Progress", ToS: "In Review"},
			{Author: "acc-bob", Created: "2026-10-05T03:00:00.000+0000", Field: "status", FieldType: "jira", FromS: "In Review", ToS: "In Progress"},
		}})
	f.AddIssue(jiratest.Issue{Key: "PROJ-8", Summary: "New", Assignee: "acc-me", Created: "2026-10-06T08:30:00.000+0000"})
	s := service(t, f, config.PrivacyOwn)
	s.now = func() time.Time { return time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	v, err := s.StatusTimes(ctx, "PROJ-7")
	if err != nil {
		t.Fatal(err)
	}
	assertNoPersonalData(t, v)
	got := map[string]StatusTime{}
	for _, st := range v.Statuses {
		got[st.Status] = st
	}
	if len(v.Statuses) != 3 || v.Statuses[0].Status != "To Do" || v.Changes != 3 {
		t.Fatalf("view = %+v", v)
	}
	for status, want := range map[string]struct {
		dur     string
		visits  int
		current bool
	}{
		"To Do":       {"1d", 1, false},
		"In Progress": {"3d 6h", 2, true},
		"In Review":   {"18h", 1, false},
	} {
		st := got[status]
		if st.Duration != want.dur || st.Visits != want.visits || st.Current != want.current {
			t.Errorf("%s = %+v, want %+v", status, st, want)
		}
	}

	v, err = s.StatusTimes(ctx, "PROJ-8")
	if err != nil || len(v.Statuses) != 1 || v.Statuses[0].Duration != "30m" || !v.Statuses[0].Current {
		t.Errorf("without changes = %+v, %v", v, err)
	}
	if _, err := s.StatusTimes(ctx, "PROJ-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("out of scope: %v", err)
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	for secs, want := range map[int64]string{30: "under 1m", 60: "1m", 3600: "1h", 5400: "1h 30m", 86400: "1d", 90000: "1d 1h", 172800 + 59: "2d"} {
		if got := formatDuration(secs); got != want {
			t.Errorf("%d: %q, want %q", secs, got, want)
		}
	}
}

func TestEpicParent(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-10", Type: "Epic", Summary: "Checkout redesign", Assignee: "acc-alice", Reporter: "acc-alice",
		Description: jiratest.Doc("secret plans")})
	f.AddIssue(jiratest.Issue{Key: "PROJ-11", Type: "Epic", Summary: "Old epic", Assignee: "acc-alice", Status: "Done"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-12", Type: "Epic", Summary: "My epic", Assignee: "acc-me"})
	s := service(t, f, config.PrivacyOwn)
	ctx := context.Background()

	p, err := s.PlanUpdate(ctx, "PROJ-3", []FieldInput{{"Parent", "PROJ-10"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, `to:   "PROJ-10 (Epic, To Do)"`) || strings.Contains(p.Description, "Checkout") {
		t.Errorf("description:\n%s", p.Description)
	}
	assertNoPersonalData(t, p.Description)
	if _, err := p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Issues["PROJ-3"].Parent != "PROJ-10" {
		t.Fatalf("parent = %q", f.Issues["PROJ-3"].Parent)
	}

	// Someone else's standard ticket is still out of scope.
	if _, err := s.PlanUpdate(ctx, "PROJ-3", []FieldInput{{"Parent", "PROJ-2"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("standard parent of someone else: %v", err)
	}
	if _, err := s.PlanCreate(ctx, CreateInput{Project: "PROJ", Type: "Subtask", Summary: "x", Parent: "PROJ-10"}); err == nil || !strings.Contains(err.Error(), "standard ticket") {
		t.Errorf("subtask under an epic: %v", err)
	}
	c, err := s.PlanCreate(ctx, CreateInput{Project: "PROJ", Type: "Task", Summary: "x", Parent: "PROJ-10"})
	if err != nil || !strings.Contains(c.Description, "- Parent: PROJ-10 (Epic, To Do)") {
		t.Errorf("create under an epic: %v\n%v", err, c)
	}

	// Removing the parent.
	p, err = s.PlanUpdate(ctx, "PROJ-3", []FieldInput{{"parent", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Description, `from: "PROJ-10 (Epic, To Do)"`) || !strings.Contains(p.Description, "to:   (empty)") {
		t.Errorf("description:\n%s", p.Description)
	}
	if _, err := p.Apply(ctx); err != nil || f.Issues["PROJ-3"].Parent != "" {
		t.Fatalf("remove: %v, parent %q", err, f.Issues["PROJ-3"].Parent)
	}
	if _, err := s.PlanUpdate(ctx, "PROJ-3", []FieldInput{{"parent", ""}}); err == nil || !strings.Contains(err.Error(), "has no parent") {
		t.Errorf("remove without parent: %v", err)
	}

	// Lists of epics come only from the parents of your own tickets.
	f.AddIssue(jiratest.Issue{Key: "PROJ-13", Summary: "Mine, in a done epic", Assignee: "acc-me", Parent: "PROJ-11"})
	f.AddIssue(jiratest.Issue{Key: "PROJ-14", Type: "Epic", Summary: "Unrelated epic", Assignee: "acc-alice"})
	f.Issues["PROJ-3"].Parent = "PROJ-10"
	f.Issues["PROJ-1"].Parent = "PROJ-12"
	epics, err := s.MyEpics(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Related{{Relation: "epic", Key: "PROJ-12", Type: "Epic", Status: "To Do"}, {Relation: "epic", Key: "PROJ-10", Type: "Epic", Status: "To Do"}}
	if !reflect.DeepEqual(epics, want) {
		t.Errorf("epics = %+v", epics)
	}
	assertNoPersonalData(t, epics)
	if strings.Contains(fmt.Sprint(epics), "Checkout") {
		t.Errorf("epics show summaries: %+v", epics)
	}
	picks, err := s.MyEpicsForPicker(ctx, "proj")
	if err != nil || len(picks) != 2 || picks[0].Summary != "My epic" || picks[1].Summary != "Checkout redesign" {
		t.Errorf("picker epics = %+v, %v", picks, err)
	}
	if es, err := s.MyEpics(ctx, "OTHER"); err != nil || len(es) != 0 {
		t.Errorf("epics of another project = %+v, %v", es, err)
	}
	// The query is not trusted: other people's tickets in the results are
	// left out.
	f.UnscopedSearch = true
	f.AddIssue(jiratest.Issue{Key: "PROJ-15", Summary: "Alice's child", Assignee: "acc-alice", Parent: "PROJ-14"})
	if es, _ := s.MyEpics(ctx, ""); len(es) != 2 {
		t.Errorf("epics with an unscoped search = %+v", es)
	}
	f.UnscopedSearch = false

	d, err := s.Issue(ctx, "PROJ-3")
	if err != nil {
		t.Fatal(err)
	}
	if d.Parent == nil || d.Parent.Key != "PROJ-10" || d.Parent.Summary != "" {
		t.Errorf("parent = %+v", d.Parent)
	}
}

//go:build integration

// Package integration runs tix-for-jira against a real Jira Cloud test site.
// See README.md in this directory for the setup.
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/approvekit/keychain"
	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

type site struct {
	cfg     *config.Config
	token   string
	svc     *core.Service
	jc      *jira.Client
	project string
	other   string
	run     string
}

// closeWhenDone moves a ticket to a done status at the end of the test, so
// test runs do not pile up open tickets. Tickets cannot be deleted with
// tix-for-jira; remove them in Jira if needed.
func closeWhenDone(t *testing.T, jc *jira.Client, key string) {
	t.Cleanup(func() {
		ctx := context.Background()
		ts, err := jc.Transitions(ctx, key)
		if err != nil {
			t.Logf("cleanup %s: %v", key, err)
			return
		}
		for _, tr := range ts {
			if tr.To.StatusCategory.Key == "done" {
				if err := jc.DoTransition(ctx, key, tr.ID); err != nil {
					t.Logf("cleanup %s: %v", key, err)
				}
				return
			}
		}
		t.Logf("cleanup %s: no transition to a done status", key)
	})
}

func setup(t *testing.T) *site {
	t.Helper()
	if os.Getenv("TIX_JIRA_PROFILE") == "" && os.Getenv("TIX_JIRA_CONFIG") == "" {
		t.Fatal("set TIX_JIRA_PROFILE to the test site's profile (see integration/README.md)")
	}
	project, other := os.Getenv("TIX_JIRA_IT_PROJECT"), os.Getenv("TIX_JIRA_IT_OTHER")
	if project == "" || other == "" {
		t.Fatal("set TIX_JIRA_IT_PROJECT and TIX_JIRA_IT_OTHER (see integration/README.md)")
	}
	cfg := testSiteConfig(t, "")
	if cfg.Privacy != config.PrivacyOwn {
		t.Fatal("the test site config must use privacy mode own")
	}
	token, err := keychain.New("tix-for-jira").Get(keychain.AccountKey(cfg.Site, cfg.Email))
	if err != nil {
		t.Fatal(err)
	}
	jc := newClient(cfg, token)
	svc, err := core.New(context.Background(), jc, cfg.Privacy)
	if err != nil {
		t.Fatal(err)
	}
	return &site{cfg: cfg, token: token, svc: svc, jc: jc, project: project, other: other, run: time.Now().Format("20060102-150405")}
}

// testSiteConfig loads a profile and refuses anything that could be
// production: the default profile, profiles that did not opt in to
// development builds, and any profile on the same site as the default
// profile.
func testSiteConfig(t *testing.T, profile string) *config.Config {
	t.Helper()
	name, err := config.ResolveProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if name == config.DefaultProfile {
		t.Fatal("refusing to test against the default profile, which is meant for production; use a test profile")
	}
	cfg, err := config.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowDevBuilds {
		t.Fatalf("profile %s has not opted in to development builds (`tix config dev-builds on --profile %s`); refusing to touch it", name, name)
	}
	if prod, err := config.Load(config.DefaultProfile); err == nil && prod.Site == cfg.Site {
		t.Fatalf("profile %s points to %s, the site of the default (production) profile; refusing", name, cfg.Site)
	}
	return cfg
}

// applied returns a function that applies a plan, failing the test on any
// error; use it as applied(t)(svc.PlanX(...)).
// newClient talks to the test site as the profile's account, through the
// API gateway when the profile uses a scoped token.
func newClient(cfg *config.Config, token string) *jira.Client {
	var opts []atlassian.Option
	if cfg.ScopedToken {
		opts = append(opts, atlassian.WithGateway(atlassian.Gateway, cfg.CloudID))
	}
	return jira.New(cfg.Site, cfg.Email, token, "integration", opts...)
}

func applied(t *testing.T) func(*core.Plan, error) string {
	return func(p *core.Plan, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		msg, err := p.Apply(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
}

func (s *site) create(t *testing.T, summary, description string) string {
	t.Helper()
	msg := applied(t)(s.svc.PlanCreate(context.Background(), core.CreateInput{
		Project: s.project, Type: "Task", Summary: summary + " " + s.run, Description: description, Labels: []string{"tix-for-jira-it"},
	}))
	var key string
	if _, err := fmt.Sscanf(msg, "Created %s", &key); err != nil {
		t.Fatalf("unexpected create result %q", msg)
	}
	key = strings.TrimSuffix(key, ":")
	closeWhenDone(t, s.jc, key)
	return key
}

// upload adds an attachment through the REST API; tix-for-jira itself cannot
// upload, so this is test-only.
func (s *site) upload(t *testing.T, key, name string, data []byte) {
	t.Helper()
	if _, err := newClient(s.cfg, s.token).AddAttachment(context.Background(), key, name, data); err != nil {
		t.Fatalf("upload: %v", err)
	}
}

func TestIntegration(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	t.Run("whoami works without admin rights", func(t *testing.T) {
		me, err := s.svc.Whoami(ctx)
		if err != nil || me.Privacy != "own" {
			t.Fatalf("whoami = %+v, %v", me, err)
		}
	})

	t.Run("someone else's ticket is invisible", func(t *testing.T) {
		if _, err := s.svc.Issue(ctx, s.other); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Issue: %v", err)
		}
		if _, err := s.svc.Comments(ctx, s.other, 10); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Comments: %v", err)
		}
		found, err := s.svc.Search(ctx, "key = "+s.other, 10)
		if err != nil || len(found) != 0 {
			t.Errorf("Search = %+v, %v", found, err)
		}
		if _, err := s.svc.PlanComment(ctx, s.other, "x"); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("PlanComment: %v", err)
		}
	})

	desc := "## Steps\n\n- open the page\n- click **Save**\n\n```sh\nmake test\n```\n\nSee [docs](https://example.com)."
	a := s.create(t, "tix-for-jira integration A", desc)
	b := s.create(t, "tix-for-jira integration B", "")

	t.Run("created ticket round-trips Markdown and is assigned to me", func(t *testing.T) {
		d, err := s.svc.Issue(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if d.Assignee != "Me" || d.Description != desc {
			t.Errorf("assignee=%q description:\n%s", d.Assignee, d.Description)
		}
	})

	t.Run("browse URL is on the site", func(t *testing.T) {
		d, err := s.svc.Issue(ctx, a)
		if err != nil || d.URL != s.cfg.Site+"/browse/"+a {
			t.Errorf("URL = %q, %v", d.URL, err)
		}
	})

	t.Run("my_issues finds new tickets", func(t *testing.T) {
		list, err := s.svc.MyIssues(ctx, core.MyIssuesOptions{Since: "-1h", Text: s.run})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) < 2 {
			t.Errorf("found %d tickets: %+v", len(list), list)
		}
		one, err := s.svc.MyIssues(ctx, core.MyIssuesOptions{Max: 1})
		if err != nil || len(one) != 1 {
			t.Errorf("max=1: %d, %v", len(one), err)
		}
	})

	t.Run("comment with mention", func(t *testing.T) {
		applied(t)(s.svc.PlanComment(ctx, a, "Checked by @[Me] with `code`."))
		cs, err := s.svc.Comments(ctx, a, 10)
		if err != nil || len(cs) == 0 || cs[len(cs)-1].Body != "Checked by @[Me] with `code`." {
			t.Errorf("comments = %+v, %v", cs, err)
		}
	})

	t.Run("update fields", func(t *testing.T) {
		applied(t)(s.svc.PlanUpdate(ctx, a, []core.FieldInput{
			{Field: "Summary", Value: "tix-for-jira integration A updated " + s.run},
			{Field: "Labels", Value: "tix-for-jira-it,updated"},
		}))
		d, err := s.svc.Issue(ctx, a)
		if err != nil || !strings.Contains(d.Summary, "updated") || len(d.Labels) != 2 {
			t.Errorf("issue = %+v, %v", d, err)
		}
		hist, err := s.svc.History(ctx, a, "-1h", 50)
		if err != nil || len(hist) == 0 {
			t.Errorf("history = %+v, %v", hist, err)
		}
	})

	t.Run("transition", func(t *testing.T) {
		ts, err := s.svc.Transitions(ctx, a)
		if err != nil || len(ts) == 0 {
			t.Fatalf("transitions = %+v, %v", ts, err)
		}
		d, _ := s.svc.Issue(ctx, a)
		var target string
		for _, tr := range ts {
			if tr.To != d.Status {
				target = tr.To
				break
			}
		}
		applied(t)(s.svc.PlanTransition(ctx, a, target, ""))
		if d, _ := s.svc.Issue(ctx, a); d.Status != target {
			t.Errorf("status = %s, want %s", d.Status, target)
		}
	})

	t.Run("link direction", func(t *testing.T) {
		msg := applied(t)(s.svc.PlanLink(ctx, a, "blocks", b))
		if msg != fmt.Sprintf("Linked: %s blocks %s.", a, b) {
			t.Errorf("result = %q", msg)
		}
		da, _ := s.svc.Issue(ctx, a)
		db, _ := s.svc.Issue(ctx, b)
		if !hasLink(da.Links, "blocks", b) || !hasLink(db.Links, "is blocked by", a) {
			t.Errorf("links: %s=%+v %s=%+v", a, da.Links, b, db.Links)
		}
	})

	t.Run("worklog", func(t *testing.T) {
		p, err := s.svc.PlanWorklog(ctx, b, "15m", "", "integration test")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Apply(ctx); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "time tracking") {
				t.Skip("time tracking is disabled on the test site")
			}
			t.Fatal(err)
		}
		ws, err := s.svc.Worklogs(ctx, b, 10)
		if err != nil || len(ws) == 0 || ws[len(ws)-1].Author != "Me" {
			t.Errorf("worklogs = %+v, %v", ws, err)
		}
	})

	t.Run("attachment download", func(t *testing.T) {
		data := []byte("tix attachment " + s.run)
		s.upload(t, b, "it.txt", data)
		d, err := s.svc.Issue(ctx, b)
		if err != nil || len(d.Attachments) == 0 {
			t.Fatalf("attachments = %+v, %v", d, err)
		}
		f, err := s.svc.Attachment(ctx, b, d.Attachments[0].ID, t.TempDir())
		if err != nil || !bytes.Equal(f.Data, data) {
			t.Errorf("attachment = %+v, %v", f, err)
		}
	})

	t.Run("attachment upload", func(t *testing.T) {
		data := []byte("uploaded by tix " + s.run)
		path := filepath.Join(t.TempDir(), "tix upload.txt")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		applied(t)(s.svc.PlanAttach(ctx, b, path))
		d, err := s.svc.Issue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		var id string
		for _, at := range d.Attachments {
			if at.Filename == "tix upload.txt" {
				id = at.ID
			}
		}
		if id == "" {
			t.Fatalf("uploaded file not among %+v", d.Attachments)
		}
		f, err := s.svc.Attachment(ctx, b, id, t.TempDir())
		if err != nil || !bytes.Equal(f.Data, data) || f.Author != "Me" {
			t.Errorf("downloaded upload = %+v, %v", f, err)
		}
	})

	t.Run("web link", func(t *testing.T) {
		link := "https://example.com/tix-for-jira/" + s.run
		applied(t)(s.svc.PlanWebLink(ctx, b, link, "tix web link "+s.run))
		d, err := s.svc.Issue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, l := range d.WebLinks {
			found = found || (l.URL == link && l.Title == "tix web link "+s.run)
		}
		if !found {
			t.Errorf("web links = %+v", d.WebLinks)
		}
		if _, err := s.svc.PlanWebLink(ctx, b, link, ""); err == nil {
			t.Error("duplicate web link was accepted")
		}
	})

	t.Run("subtask", func(t *testing.T) {
		types, err := s.svc.IssueTypes(ctx, s.project)
		if err != nil {
			t.Fatal(err)
		}
		subtaskType := ""
		for _, it := range types {
			if it.Subtask {
				subtaskType = it.Name
			}
		}
		if subtaskType == "" {
			t.Skip("the test project has no subtask type")
		}
		msg := applied(t)(s.svc.PlanCreate(ctx, core.CreateInput{Project: s.project, Type: subtaskType, Summary: "tix-for-jira integration subtask " + s.run, Parent: a, Labels: []string{"tix-for-jira-it"}}))
		var sub string
		if _, err := fmt.Sscanf(msg, "Created %s", &sub); err != nil {
			t.Fatalf("unexpected create result %q", msg)
		}
		sub = strings.TrimSuffix(sub, ":")
		closeWhenDone(t, s.jc, sub)
		d, err := s.svc.Issue(ctx, sub)
		if err != nil || d.Parent == nil || d.Parent.Key != a || d.Assignee != "Me" {
			t.Errorf("subtask = %+v, %v", d, err)
		}
		parent, _ := s.svc.Issue(ctx, a)
		if !hasLink(parent.Subtasks, "subtask", sub) {
			t.Errorf("parent subtasks = %+v", parent.Subtasks)
		}
	})

	t.Run("transition with comment", func(t *testing.T) {
		ts, err := s.svc.Transitions(ctx, b)
		if err != nil || len(ts) == 0 {
			t.Fatalf("transitions = %+v, %v", ts, err)
		}
		d, _ := s.svc.Issue(ctx, b)
		var target string
		for _, tr := range ts {
			if tr.To != d.Status {
				target = tr.To
				break
			}
		}
		note := "Moved by the integration test " + s.run
		applied(t)(s.svc.PlanTransition(ctx, b, target, note))
		cs, err := s.svc.Comments(ctx, b, 20)
		if err != nil || len(cs) == 0 || cs[len(cs)-1].Body != note {
			t.Errorf("comments = %+v, %v", cs, err)
		}
	})

	t.Run("custom number field and due date", func(t *testing.T) {
		fs, err := s.svc.EditableFields(ctx, a)
		if err != nil || len(fs) == 0 {
			t.Fatalf("editable fields = %+v, %v", fs, err)
		}
		var number, due string
		for _, f := range fs {
			switch {
			case f.Type == "number" && number == "":
				number = f.Name
			case strings.HasPrefix(f.Type, "date (") && due == "":
				due = f.Name
			}
		}
		var changes []core.FieldInput
		if number != "" {
			changes = append(changes, core.FieldInput{Field: number, Value: "3"})
		}
		if due != "" {
			changes = append(changes, core.FieldInput{Field: due, Value: "2026-12-31"})
		}
		if len(changes) == 0 {
			t.Skip("no editable number or date field on the test ticket")
		}
		applied(t)(s.svc.PlanUpdate(ctx, a, changes))
		d, err := s.svc.Issue(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if due != "" && d.Due != "2026-12-31" {
			t.Errorf("due = %q", d.Due)
		}
		if number != "" {
			found := false
			for _, fv := range d.Fields {
				found = found || (fv.Name == number && fv.Value == "3")
			}
			if !found {
				t.Logf("%s not shown among custom fields (it may be a system field): %+v", number, d.Fields)
			}
		}
	})

	t.Run("versions and environment", func(t *testing.T) {
		fs, err := s.svc.EditableFields(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		var changes []core.FieldInput
		var wantFix, wantEnv string
		for _, f := range fs {
			switch {
			case f.ID == "fixVersions" && len(f.Allowed) > 0:
				wantFix = f.Allowed[0]
				changes = append(changes, core.FieldInput{Field: f.ID, Value: wantFix})
			case f.ID == "environment":
				wantEnv = "Tested on macOS " + s.run
				changes = append(changes, core.FieldInput{Field: f.ID, Value: wantEnv})
			}
		}
		if len(changes) == 0 {
			t.Skip("the test project has no fix versions or environment field to set")
		}
		applied(t)(s.svc.PlanUpdate(ctx, a, changes))
		d, err := s.svc.Issue(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if wantFix != "" && (len(d.FixVersions) != 1 || d.FixVersions[0] != wantFix) {
			t.Errorf("fix versions = %v, want %s", d.FixVersions, wantFix)
		}
		if wantEnv != "" && d.Environment != wantEnv {
			t.Errorf("environment = %q, want %q", d.Environment, wantEnv)
		}
		if wantFix == "" {
			t.Log("no versions in the test project; only the environment was checked")
		}
	})

	t.Run("move under another parent", func(t *testing.T) {
		msg := applied(t)(s.svc.PlanCreate(ctx, core.CreateInput{Project: s.project, Type: "Epic", Summary: "tix-for-jira integration epic " + s.run, Labels: []string{"tix-for-jira-it"}}))
		var epic string
		if _, err := fmt.Sscanf(msg, "Created %s", &epic); err != nil {
			t.Fatalf("unexpected create result %q", msg)
		}
		epic = strings.TrimSuffix(epic, ":")
		closeWhenDone(t, s.jc, epic)
		applied(t)(s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: "Parent", Value: epic}}))
		d, err := s.svc.Issue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		if d.Parent == nil || d.Parent.Key != epic {
			t.Errorf("parent = %+v, want %s", d.Parent, epic)
		}
		if _, err := s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: "Parent", Value: s.other}}); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("moving under someone else's ticket: %v", err)
		}
	})

	t.Run("estimates", func(t *testing.T) {
		fs, err := s.svc.EditableFields(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range fs {
			found = found || f.ID == "originalEstimate"
		}
		if !found {
			t.Skip("time tracking is not on the edit screen of the test project")
		}
		applied(t)(s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: "Original estimate", Value: "3h"}, {Field: "Remaining estimate", Value: "2h 15m"}}))
		applied(t)(s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: "Original estimate", Value: "5h"}}))
		d, err := s.svc.Issue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		if d.TimeTracking == nil || d.TimeTracking.Original != "5h" || d.TimeTracking.Remaining != "2h 15m" {
			t.Errorf("time tracking = %+v, want original 5h and remaining kept at 2h 15m", d.TimeTracking)
		}
	})

	t.Run("cascading select, URL and team fields", func(t *testing.T) {
		fs, err := s.svc.EditableFields(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		byType := map[string]core.EditableField{}
		for _, f := range fs {
			switch {
			case f.Name == "tix-for-jira Region", f.Name == "tix-for-jira Docs link":
				byType[f.Name] = f
			case strings.HasPrefix(f.Type, "team"):
				byType["team"] = f
			}
		}
		region, okR := byType["tix-for-jira Region"]
		docs, okD := byType["tix-for-jira Docs link"]
		if !okR || !okD {
			t.Skip("the custom fields from TestSetupCompanyManagedProject are not on the edit screen")
		}
		link := "https://example.com/tix-for-jira/" + strconv.FormatInt(time.Now().Unix(), 10)
		applied(t)(s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: region.ID, Value: "europe / france"}, {Field: docs.ID, Value: link}}))
		d, err := s.svc.Issue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, fv := range d.Fields {
			got[fv.Name] = fv.Value
		}
		if got[region.Name] != "Europe / France" || got[docs.Name] != link {
			t.Errorf("fields = %v", got)
		}
		if _, err := s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: docs.ID, Value: "javascript:alert(1)"}}); err == nil {
			t.Error("non-http URL was accepted")
		}

		team, ok := byType["team"]
		if !ok {
			t.Log("no team field on the edit screen; team support not checked")
			return
		}
		if _, err := s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: team.ID, Value: "no such team tix-for-jira"}}); err == nil || !strings.Contains(err.Error(), "no team named") {
			t.Errorf("unknown team: %v", err)
		}
		name := os.Getenv("TIX_JIRA_IT_TEAM")
		if name == "" {
			t.Log("TIX_JIRA_IT_TEAM not set; setting a team not checked")
			return
		}
		applied(t)(s.svc.PlanUpdate(ctx, b, []core.FieldInput{{Field: team.ID, Value: name}}))
		d, _ = s.svc.Issue(ctx, b)
		for _, fv := range d.Fields {
			if fv.Name == team.Name && !strings.EqualFold(fv.Value, name) {
				t.Errorf("team = %q, want %q", fv.Value, name)
			}
		}
	})

	t.Run("watch and unwatch", func(t *testing.T) {
		d, err := s.svc.Issue(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		// Jira usually makes the creator a watcher.
		if d.Watching {
			applied(t)(s.svc.PlanWatch(ctx, b, false))
			if d, _ = s.svc.Issue(ctx, b); d.Watching {
				t.Fatal("still watching after unwatch")
			}
		}
		before := d.Watchers
		applied(t)(s.svc.PlanWatch(ctx, b, true))
		d, _ = s.svc.Issue(ctx, b)
		if !d.Watching || d.Watchers != before+1 {
			t.Errorf("after watch: watching=%v watchers=%d (before %d)", d.Watching, d.Watchers, before)
		}
		if _, err := s.svc.PlanWatch(ctx, b, true); err == nil {
			t.Error("watching twice was accepted")
		}
		applied(t)(s.svc.PlanWatch(ctx, b, false))
		if d, _ = s.svc.Issue(ctx, b); d.Watching || d.Watchers != before {
			t.Errorf("after unwatch: watching=%v watchers=%d", d.Watching, d.Watchers)
		}
	})

	t.Run("boards, active sprint and backlog", func(t *testing.T) {
		bs, err := s.svc.Boards(ctx, s.project)
		if err != nil {
			t.Fatal(err)
		}
		if len(bs) == 0 {
			t.Fatalf("no board for %s", s.project)
		}
		var board *core.BoardView
		for i := range bs {
			if bs[i].Type == "scrum" {
				board = &bs[i]
			}
		}
		if board == nil {
			if _, err := s.svc.Sprints(ctx, strconv.Itoa(bs[0].ID)); err == nil || !strings.Contains(err.Error(), "has no sprints") {
				t.Errorf("sprints of a %s board: %v", bs[0].Type, err)
			}
			t.Skip("no scrum board in the test project")
		}
		ref := strconv.Itoa(board.ID)
		ss, err := s.svc.Sprints(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) == 0 || ss[0].State != "active" {
			t.Skip("no active sprint; run TestSetupCompanyManagedProject")
		}
		gs, err := s.svc.ActiveSprints(ctx, board.Name, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range gs {
			if g.Total < len(g.Issues) {
				t.Errorf("sprint %s: total %d < shown %d", g.Sprint.Name, g.Total, len(g.Issues))
			}
		}
		bl, err := s.svc.Backlog(ctx, ref, 200)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, i := range bl.Issues {
			found = found || i.Key == a
		}
		if !found || bl.Total < len(bl.Issues) {
			t.Errorf("new ticket %s not in the backlog of %s (%d shown, %d total)", a, board.Name, len(bl.Issues), bl.Total)
		}
	})

	t.Run("move between sprints and the backlog", func(t *testing.T) {
		bs, err := s.svc.Boards(ctx, s.project)
		if err != nil {
			t.Fatal(err)
		}
		var ss []core.SprintView
		board := ""
		for _, bd := range bs {
			if bd.Type == "scrum" {
				board = strconv.Itoa(bd.ID)
				if ss, err = s.svc.Sprints(ctx, board); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		var active, future *core.SprintView
		for i := range ss {
			switch {
			case ss[i].State == "active" && active == nil:
				active = &ss[i]
			case ss[i].State == "future" && future == nil:
				future = &ss[i]
			}
		}
		if active == nil || future == nil {
			t.Skip("needs a scrum board with an active and a future sprint; run TestSetupCompanyManagedProject")
		}
		inSprint := func(want *core.SprintView) bool {
			gs, err := s.svc.ActiveSprints(ctx, board, 200)
			if err != nil {
				t.Fatal(err)
			}
			for _, g := range gs {
				for _, i := range g.Issues {
					if i.Key == b && g.Sprint.ID == want.ID {
						return true
					}
				}
			}
			return false
		}
		applied(t)(s.svc.PlanMoveSprint(ctx, b, active.Name))
		if !inSprint(active) {
			t.Errorf("%s is not in the active sprint %s", b, active.Name)
		}
		applied(t)(s.svc.PlanMoveSprint(ctx, b, strconv.Itoa(future.ID)))
		if _, err := s.svc.PlanMoveSprint(ctx, b, strconv.Itoa(future.ID)); err == nil || !strings.Contains(err.Error(), "already in sprint") {
			t.Errorf("moving into the same sprint again: %v", err)
		}
		applied(t)(s.svc.PlanMoveSprint(ctx, b, "backlog"))
		bl, err := s.svc.Backlog(ctx, board, 200)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, i := range bl.Issues {
			found = found || i.Key == b
		}
		if !found {
			t.Errorf("%s is not back in the backlog", b)
		}
	})

	t.Run("worklog with start time", func(t *testing.T) {
		start := time.Now().Add(-2 * time.Hour).Format("2006-01-02 15:04")
		applied(t)(s.svc.PlanWorklog(ctx, a, "30m", start, ""))
		ws, err := s.svc.Worklogs(ctx, a, 10)
		if err != nil || len(ws) == 0 || ws[len(ws)-1].Started != start || ws[len(ws)-1].TimeSpent != "30m" {
			t.Errorf("worklogs = %+v, %v (want start %s)", ws, err, start)
		}
	})

	t.Run("symmetric link", func(t *testing.T) {
		applied(t)(s.svc.PlanLink(ctx, b, "relates to", a))
		db, _ := s.svc.Issue(ctx, b)
		if !hasLink(db.Links, "relates to", a) {
			t.Errorf("links of %s = %+v", b, db.Links)
		}
	})

	t.Run("history lists my changes", func(t *testing.T) {
		hist, err := s.svc.History(ctx, a, "-1h", 100)
		if err != nil || len(hist) < 3 {
			t.Fatalf("history = %+v, %v", hist, err)
		}
		for _, h := range hist {
			if h.Author != "Me" {
				t.Errorf("unexpected author %q in my own ticket's history", h.Author)
			}
		}
	})

}

func hasLink(links []core.Related, relation, key string) bool {
	for _, l := range links {
		if l.Relation == relation && l.Key == key {
			return true
		}
	}
	return false
}

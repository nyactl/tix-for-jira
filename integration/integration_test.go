//go:build integration

// Package integration runs tix-jira against a real Jira Cloud test site.
// See README.md in this directory for the setup.
package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/core"
	"github.com/nyactl/tix-jira/internal/jira"
	"github.com/nyactl/tix-jira/internal/secret"
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
// tix-jira; remove them in Jira if needed.
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
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Privacy != config.PrivacyOwn {
		t.Fatal("the test site config must use privacy mode own")
	}
	token, err := secret.NewKeychain().Get(secret.AccountKey(cfg.Site, cfg.Email))
	if err != nil {
		t.Fatal(err)
	}
	jc := jira.New(cfg.Site, cfg.Email, token, "integration")
	svc, err := core.New(context.Background(), jc, cfg.Privacy)
	if err != nil {
		t.Fatal(err)
	}
	return &site{cfg: cfg, token: token, svc: svc, jc: jc, project: project, other: other, run: time.Now().Format("20060102-150405")}
}

// applied returns a function that applies a plan, failing the test on any
// error; use it as applied(t)(svc.PlanX(...)).
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
		Project: s.project, Type: "Task", Summary: summary + " " + s.run, Description: description, Labels: []string{"tix-jira-it"},
	}))
	var key string
	if _, err := fmt.Sscanf(msg, "Created %s", &key); err != nil {
		t.Fatalf("unexpected create result %q", msg)
	}
	key = strings.TrimSuffix(key, ":")
	closeWhenDone(t, s.jc, key)
	return key
}

// upload adds an attachment through the REST API; tix-jira itself cannot
// upload, so this is test-only.
func (s *site) upload(t *testing.T, key, name string, data []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, s.cfg.Site+"/rest/api/3/issue/"+key+"/attachments", &body)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.cfg.Email+":"+s.token)))
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload: HTTP %d: %s", resp.StatusCode, b)
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
	a := s.create(t, "tix-jira integration A", desc)
	b := s.create(t, "tix-jira integration B", "")

	t.Run("created ticket round-trips Markdown and is assigned to me", func(t *testing.T) {
		d, err := s.svc.Issue(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if d.Assignee != "Me" || d.Description != desc {
			t.Errorf("assignee=%q description:\n%s", d.Assignee, d.Description)
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
			{Field: "Summary", Value: "tix-jira integration A updated " + s.run},
			{Field: "Labels", Value: "tix-jira-it,updated"},
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
		data := []byte("tix-jira attachment " + s.run)
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

	t.Run("subtask", func(t *testing.T) {
		msg := applied(t)(s.svc.PlanCreate(ctx, core.CreateInput{Project: s.project, Type: "Subtask", Summary: "tix-jira integration subtask " + s.run, Parent: a}))
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

//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nyactl/tix-jira/internal/adf"
	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/core"
	"github.com/nyactl/tix-jira/internal/jira"
	"github.com/nyactl/tix-jira/internal/secret"
)

// otherPerson is a second account on the test site, driven directly
// through the Jira client to produce data the tested account must not see
// in clear: its own tickets, comments, mentions and history entries.
type otherPerson struct {
	jc    *jira.Client
	me    *jira.User
	names []string // everything that must never appear in tix-jira output
}

func setupOther(t *testing.T, s *site) *otherPerson {
	t.Helper()
	profile := os.Getenv("TIX_JIRA_IT_OTHER_PROFILE")
	if profile == "" {
		t.Skip("set TIX_JIRA_IT_OTHER_PROFILE to a profile of a second account on the test site (see integration/README.md)")
	}
	cfg, err := config.Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Site != s.cfg.Site {
		t.Fatalf("profile %s points to %s, not the test site %s", profile, cfg.Site, s.cfg.Site)
	}
	token, err := secret.NewKeychain().Get(secret.AccountKey(cfg.Site, cfg.Email))
	if err != nil {
		t.Fatal(err)
	}
	jc := jira.New(cfg.Site, cfg.Email, token, "integration")
	me, err := jc.Myself(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.AccountID == "" || me.DisplayName == "" {
		t.Fatal("could not read the second account's identity")
	}
	o := &otherPerson{jc: jc, me: me, names: []string{me.AccountID, me.DisplayName, cfg.Email}}
	if me.EmailAddress != "" {
		o.names = append(o.names, me.EmailAddress)
	}
	return o
}

// createOwned creates a ticket as the other person and assigns it to them.
func (o *otherPerson) createOwned(t *testing.T, project, summary string) string {
	t.Helper()
	ctx := context.Background()
	types, err := o.jc.CreatableIssueTypes(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	var taskID string
	for _, it := range types {
		if it.Name == "Task" {
			taskID = it.ID
		}
	}
	if taskID == "" {
		t.Fatalf("no Task type in %s", project)
	}
	key, err := o.jc.CreateIssue(ctx, map[string]any{
		"project":   map[string]string{"key": project},
		"issuetype": map[string]string{"id": taskID},
		"summary":   summary,
		"labels":    []string{"tix-jira-it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeWhenDone(t, o.jc, key)
	if err := o.jc.AssignIssue(ctx, key, o.me.AccountID); err != nil {
		t.Fatal(err)
	}
	return key
}

func (o *otherPerson) assertAbsent(t *testing.T, what string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	out := strings.ToLower(string(b))
	for _, n := range o.names {
		if n != "" && strings.Contains(out, strings.ToLower(n)) {
			t.Errorf("%s reveals the other person (%d chars of identity found)", what, len(n))
		}
	}
}

func TestTwoPeople(t *testing.T) {
	s := setup(t)
	other := setupOther(t, s)
	ctx := context.Background()
	me, err := jira.New(s.cfg.Site, s.cfg.Email, s.token, "integration").Myself(ctx)
	if err != nil {
		t.Fatal(err)
	}

	theirs := other.createOwned(t, s.project, "tix-jira people: owned by the other person "+s.run)
	// A ticket the other person creates and then hands over to the tested
	// account, so its reporter and history name the other person.
	handed := other.createOwned(t, s.project, "tix-jira people: handed over "+s.run)
	if err := other.jc.AssignIssue(ctx, handed, me.AccountID); err != nil {
		t.Fatal(err)
	}
	comment, err := adf.FromMarkdown("Ping from "+other.me.DisplayName+": please have a look, @[tested]", func(label string) (string, string, bool) {
		return me.AccountID, me.DisplayName, label == "tested"
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.jc.AddComment(ctx, handed, comment); err != nil {
		t.Fatal(err)
	}
	if err := other.jc.CreateLink(ctx, "Blocks", theirs, handed); err != nil {
		t.Fatal(err)
	}

	t.Run("the other person's ticket is invisible", func(t *testing.T) {
		if _, err := s.svc.Issue(ctx, theirs); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Issue: %v", err)
		}
		found, err := s.svc.Search(ctx, "key = "+theirs, 10)
		if err != nil || len(found) != 0 {
			t.Errorf("Search = %+v, %v", found, err)
		}
		if _, err := s.svc.PlanComment(ctx, theirs, "x"); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("PlanComment: %v", err)
		}
	})

	t.Run("handed-over ticket shows the other person only as a placeholder", func(t *testing.T) {
		d, err := s.svc.Issue(ctx, handed)
		if err != nil {
			t.Fatal(err)
		}
		other.assertAbsent(t, "issue", d)
		if d.Assignee != "Me" || !strings.HasPrefix(d.Reporter, "Person ") {
			t.Errorf("assignee=%q reporter=%q", d.Assignee, d.Reporter)
		}
		var link *core.Related
		for i := range d.Links {
			if d.Links[i].Key == theirs {
				link = &d.Links[i]
			}
		}
		if link == nil || link.Relation != "is blocked by" || link.Summary != "" {
			t.Errorf("link to the other person's ticket = %+v", link)
		}

		cs, err := s.svc.Comments(ctx, handed, 20)
		if err != nil || len(cs) == 0 {
			t.Fatalf("comments = %+v, %v", cs, err)
		}
		other.assertAbsent(t, "comments", cs)
		last := cs[len(cs)-1]
		if last.Author != d.Reporter || !strings.Contains(last.Body, "Ping from "+d.Reporter) || !strings.Contains(last.Body, "@[Me]") {
			t.Errorf("comment = %+v", last)
		}

		hist, err := s.svc.History(ctx, handed, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		other.assertAbsent(t, "history", hist)
		handover := false
		for _, h := range hist {
			for _, c := range h.Changes {
				handover = handover || (c.Field == "assignee" && c.From == d.Reporter && c.To == "Me")
			}
		}
		if !handover {
			t.Errorf("no assignee change from %s to Me in history: %+v", d.Reporter, hist)
		}
	})

	t.Run("mentioning a placeholder reaches the real person", func(t *testing.T) {
		d, err := s.svc.Issue(ctx, handed)
		if err != nil {
			t.Fatal(err)
		}
		applied(t)(s.svc.PlanComment(ctx, handed, "Thanks @["+d.Reporter+"], done."))
		cs, err := other.jc.Comments(ctx, handed, 5)
		if err != nil || len(cs) == 0 {
			t.Fatalf("comments as the other person = %+v, %v", cs, err)
		}
		body := string(cs[len(cs)-1].Body)
		if !strings.Contains(body, `"type":"mention"`) || !strings.Contains(body, other.me.AccountID) {
			t.Errorf("the mention does not point to the other person: %s", body)
		}
	})
}

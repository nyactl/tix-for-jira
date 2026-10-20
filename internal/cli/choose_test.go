package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/nyactl/tix-for-jira/internal/jiratest"
)

func TestChooseTicketAndStatus(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.picks = []pickChoice{{"PROJ-1", ""}}
	out, _, err := e.run(t, "show")
	if err != nil || !strings.Contains(out, "# PROJ-1: Mine") {
		t.Fatalf("show without a key: %q, %v", out, err)
	}
	// Only the user's own tickets are offered.
	if len(e.tty.picked) != 1 || !strings.Contains(e.tty.picked[0], "PROJ-1") || strings.Contains(strings.Join(e.tty.picked, "\n"), "PROJ-2") {
		t.Errorf("offered %q", e.tty.picked)
	}

	e.tty.picks = []pickChoice{{"PROJ-1", ""}, {"In Progress", ""}}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "transition"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.Issues["PROJ-1"].Status; got != "In Progress" {
		t.Errorf("status %q", got)
	}
	if !strings.Contains(strings.Join(e.tty.picked, "\n"), "Done") {
		t.Errorf("statuses offered: %q", e.tty.picked)
	}

	// Leaving the picker changes nothing.
	e.tty.picks = []pickChoice{{"", ""}}
	if _, _, err := e.run(t, "comment", "--body", "x"); !errors.Is(err, errNothingChosen) {
		t.Errorf("canceled: %v", err)
	}
	// Without a terminal, the key is needed.
	e.tty.noTTY = true
	if _, _, err := e.run(t, "show"); err == nil || !strings.Contains(err.Error(), "give a ticket key") {
		t.Errorf("without a terminal: %v", err)
	}
	if _, _, err := e.run(t, "transition", "PROJ-1"); err == nil || !strings.Contains(err.Error(), "give the status") {
		t.Errorf("transition without a status or terminal: %v", err)
	}
}

func TestCompletion(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	out, _, err := e.run(t, "__complete", "show", "")
	if err != nil || !strings.Contains(out, "PROJ-1\tMine") || strings.Contains(out, "PROJ-2") {
		t.Errorf("ticket completion: %q, %v", out, err)
	}
	out, _, _ = e.run(t, "__complete", "transition", "PROJ-1", "in")
	if !strings.Contains(out, "In Progress\t") || strings.Contains(out, "Done") {
		t.Errorf("status completion: %q", out)
	}
	out, _, _ = e.run(t, "__complete", "log", "proj")
	if !strings.Contains(out, "PROJ-1\tMine") {
		t.Errorf("log completion: %q", out)
	}
}

func TestMentionsAcrossCommands(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	// One command shows the reporter as Person A ...
	out, _, err := e.run(t, "show", "PROJ-1")
	if err != nil || !strings.Contains(out, "Reporter: Person A") {
		t.Fatalf("show: %q, %v", out, err)
	}
	// ... and a later one can mention that person.
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "comment", "PROJ-1", "--body", "Thanks @[Person A]"); err != nil {
		t.Fatal(err)
	}
	cs := e.fake.Issues["PROJ-1"].Comments
	if len(cs) != 1 || !strings.Contains(string(cs[0].Body), `"attrs":{"id":"acc-alice"`) || !strings.Contains(string(cs[0].Body), `"type":"mention"`) {
		t.Errorf("comment %+v", cs)
	}
	// Labels nobody was shown do not resolve.
	if _, _, err := e.run(t, "comment", "PROJ-1", "--body", "@[Person Q]"); err == nil {
		t.Error("an unknown label resolved")
	}
}

func TestParent(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.fake.AddIssue(jiratest.Issue{Key: "PROJ-10", Type: "Epic", Summary: "Checkout redesign", Assignee: "acc-alice"})
	e.fake.AddIssue(jiratest.Issue{Key: "PROJ-11", Type: "Epic", Summary: "Unrelated epic", Assignee: "acc-alice"})
	e.fake.AddIssue(jiratest.Issue{Key: "PROJ-3", Summary: "Also mine", Assignee: "acc-me"})

	// No ticket of yours is in an epic yet: other epics need their key.
	e.tty.picks = []pickChoice{{"PROJ-1", ""}}
	if _, _, err := e.run(t, "parent"); err == nil || !strings.Contains(err.Error(), "give the epic's key") {
		t.Fatalf("parent without epics: %v", err)
	}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "parent", "PROJ-1", "PROJ-10"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.Issues["PROJ-1"].Parent; got != "PROJ-10" {
		t.Fatalf("parent %q", got)
	}
	if q := e.tty.asked[len(e.tty.asked)-1]; !strings.Contains(q, `to:   "PROJ-10 (Epic, To Do)"`) || strings.Contains(q, "Checkout") {
		t.Errorf("asked %q", q)
	}

	// Now the picker offers that epic, with its summary, and nothing else.
	e.tty.picks = []pickChoice{{"PROJ-3", ""}, {"Checkout", ""}}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "parent"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.Issues["PROJ-3"].Parent; got != "PROJ-10" {
		t.Fatalf("parent of PROJ-3 %q", got)
	}
	if offered := strings.Join(e.tty.picked, "\n"); !strings.Contains(offered, "PROJ-10    [To Do] Checkout redesign") || strings.Contains(offered, "PROJ-11") {
		t.Errorf("offered %q", offered)
	}

	// Output and completion never show the summaries of other people's epics.
	out, _, err := e.run(t, "epics")
	if err != nil || !strings.Contains(out, "- PROJ-10 [Epic, To Do]") || strings.Contains(out, "Checkout") || strings.Contains(out, "PROJ-11") {
		t.Errorf("epics: %q, %v", out, err)
	}
	out, _, _ = e.run(t, "epics", "--json")
	if strings.Contains(out, "Checkout") {
		t.Errorf("epics --json: %q", out)
	}
	out, _, _ = e.run(t, "__complete", "parent", "PROJ-1", "")
	if !strings.Contains(out, "PROJ-10\tEpic, To Do") || strings.Contains(out, "Checkout") || strings.Contains(out, "PROJ-11") {
		t.Errorf("epic completion: %q", out)
	}
	out, _, _ = e.run(t, "__complete", "create", "--parent", "")
	if !strings.Contains(out, "PROJ-10\t") || strings.Contains(out, "Checkout") {
		t.Errorf("--parent completion: %q", out)
	}

	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "parent", "PROJ-1", "--remove"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.Issues["PROJ-1"].Parent; got != "" {
		t.Errorf("parent after --remove: %q", got)
	}
	if _, _, err := e.run(t, "parent", "PROJ-1", "PROJ-10", "--remove"); err == nil {
		t.Error("--remove with an epic accepted")
	}
	if out, _, _ := e.run(t, "config", "--help"); strings.Contains(out, "epic") {
		t.Errorf("config offers an epic setting: %q", out)
	}
}

func TestUnlink(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.fake.AddIssue(jiratest.Issue{Key: "PROJ-3", Summary: "Also mine", Assignee: "acc-me"})
	e.fake.Links = append(e.fake.Links,
		jiratest.Link{ID: "900", Type: "Blocks", Outward: "PROJ-1", Inward: "PROJ-2"},
		jiratest.Link{ID: "901", Type: "Relates", Outward: "PROJ-1", Inward: "PROJ-3"})

	out, _, _ := e.run(t, "__complete", "unlink", "PROJ-1", "")
	if !strings.Contains(out, "PROJ-2\tblocks, To Do") || !strings.Contains(out, "PROJ-3\trelates to, To Do") || strings.Contains(out, "Not mine") {
		t.Errorf("other-key completion: %q", out)
	}
	if out, _, _ = e.run(t, "__complete", "unlink", "PROJ-1", "PROJ-2", ""); !strings.Contains(out, "blocks\n") {
		t.Errorf("relation completion: %q", out)
	}

	// Choose the ticket and the link; the other ticket is outside the scope.
	e.tty.picks = []pickChoice{{"PROJ-1", ""}, {"PROJ-2", ""}}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "unlink"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.tty.asked[len(e.tty.asked)-1], "Remove the link: PROJ-1 (Mine) blocks PROJ-2 (Task, To Do).") || strings.Contains(strings.Join(e.tty.picked, "\n"), "Not mine") {
		t.Errorf("asked %q, picked %q", e.tty.asked, e.tty.picked)
	}
	if len(e.fake.Links) != 1 || e.fake.Links[0].ID != "901" {
		t.Fatalf("links %+v", e.fake.Links)
	}

	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "unlink", "PROJ-1", "PROJ-3", "relates to"); err != nil || len(e.fake.Links) != 0 {
		t.Fatalf("unlink by key: %v, %+v", err, e.fake.Links)
	}
	if _, _, err := e.run(t, "unlink", "PROJ-1"); err == nil || !strings.Contains(err.Error(), "PROJ-1 has no links") {
		t.Errorf("no links: %v", err)
	}
}

func TestRemoveWebLink(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.fake.Issues["PROJ-1"].RemoteLinks = []jiratest.RemoteLink{
		{ID: 1, URL: "https://wiki.example.com/spec", Title: "Spec"},
		{ID: 2, URL: "https://ci.example.com/run/7", Title: "CI run"},
	}

	if _, _, err := e.run(t, "weblink", "PROJ-1"); err == nil {
		t.Error("weblink without a URL accepted")
	}
	if _, _, err := e.run(t, "weblink", "--remove", "PROJ-1", "https://x.example.com", "title"); err == nil {
		t.Error("--remove with a title accepted")
	}

	e.tty.picks = []pickChoice{{"CI run", ""}}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "weblink", "--remove", "PROJ-1"); err != nil {
		t.Fatal(err)
	}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "weblink", "PROJ-1", "--remove", "https://wiki.example.com/spec"); err != nil {
		t.Fatal(err)
	}
	if rls := e.fake.Issues["PROJ-1"].RemoteLinks; len(rls) != 0 {
		t.Errorf("web links left: %+v", rls)
	}
	if _, _, err := e.run(t, "weblink", "--remove", "PROJ-1"); err == nil || !strings.Contains(err.Error(), "no web links") {
		t.Errorf("no web links: %v", err)
	}
}

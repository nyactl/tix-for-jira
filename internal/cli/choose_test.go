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
	e.fake.AddIssue(jiratest.Issue{Key: "PROJ-11", Type: "Epic", Summary: "Shipped", Assignee: "acc-alice", Status: "Done"})

	out, _, err := e.run(t, "epics", "--project", "PROJ")
	if err != nil || !strings.Contains(out, "- PROJ-10 [Epic, To Do]") || strings.Contains(out, "Checkout") || strings.Contains(out, "PROJ-11") {
		t.Errorf("epics: %q, %v", out, err)
	}

	// Choose the ticket and the epic.
	e.tty.picks = []pickChoice{{"PROJ-1", ""}, {"PROJ-10", ""}}
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "parent"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.Issues["PROJ-1"].Parent; got != "PROJ-10" {
		t.Fatalf("parent %q", got)
	}
	if !strings.Contains(e.tty.asked[len(e.tty.asked)-1], `to:   "PROJ-10 (Epic, To Do)"`) {
		t.Errorf("asked %q", e.tty.asked[len(e.tty.asked)-1])
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

	out, _, _ = e.run(t, "__complete", "parent", "PROJ-1", "")
	if !strings.Contains(out, "PROJ-10\tTo Do") || strings.Contains(out, "PROJ-11") {
		t.Errorf("epic completion: %q", out)
	}
	out, _, _ = e.run(t, "__complete", "create", "--project", "PROJ", "--parent", "")
	if !strings.Contains(out, "PROJ-10\t") {
		t.Errorf("--parent completion: %q", out)
	}

	// Summaries need a confirmed opt-in.
	e.tty.answers = []string{"yes"}
	if _, _, err := e.run(t, "config", "epic-summaries", "on"); err == nil {
		t.Fatal("epic summaries switched on without typing on")
	}
	e.tty.answers = []string{"on"}
	if _, _, err := e.run(t, "config", "epic-summaries", "on"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = e.run(t, "epics", "--project", "PROJ")
	if !strings.Contains(out, "- PROJ-10 [Epic, To Do] Checkout redesign") {
		t.Errorf("epics with summaries: %q", out)
	}
	if _, _, err := e.run(t, "config", "epic-summaries", "off"); err != nil {
		t.Fatal(err)
	}
	out, _, _ = e.run(t, "epics", "--project", "PROJ")
	if strings.Contains(out, "Checkout") {
		t.Errorf("epics after off: %q", out)
	}
}

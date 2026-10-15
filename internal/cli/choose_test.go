package cli

import (
	"errors"
	"strings"
	"testing"
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

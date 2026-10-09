package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/jiratest"
	"github.com/nyactl/tix-jira/internal/secret"
)

// script is a fake terminal that answers prompts in order.
type script struct {
	answers []string
	secrets []string
	asked   []string
	noTTY   bool
}

func (s *script) Prompt(q string) (string, error) {
	s.asked = append(s.asked, q)
	if s.noTTY || len(s.answers) == 0 {
		return "", errNoTerminal
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func (s *script) Secret(q string) (string, error) {
	s.asked = append(s.asked, q)
	if s.noTTY || len(s.secrets) == 0 {
		return "", errNoTerminal
	}
	a := s.secrets[0]
	s.secrets = s.secrets[1:]
	return a, nil
}

type env struct {
	fake    *jiratest.Fake
	secrets *secret.Memory
	tty     *script
}

func newEnv(t *testing.T) *env {
	t.Setenv("TIX_JIRA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	f := jiratest.New(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-1", Summary: "Mine", Assignee: "acc-me", Reporter: "acc-alice", Description: jiratest.Doc("Ask Alice Smith")})
	f.AddIssue(jiratest.Issue{Key: "PROJ-2", Summary: "Not mine", Assignee: "acc-alice"})
	return &env{fake: f, secrets: secret.NewMemory(), tty: &script{}}
}

func (e *env) loggedIn(t *testing.T) *env {
	t.Helper()
	cfg := &config.Config{Site: e.fake.URL(), Email: "me@example.com", Privacy: config.PrivacyOwn}
	if err := cfg.Save(""); err != nil {
		t.Fatal(err)
	}
	if err := e.secrets.Set(secret.AccountKey(cfg.Site, cfg.Email), "tok-me"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return e.runStdin(t, "", args...)
}

func (e *env) runStdin(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &App{Version: "test", In: strings.NewReader(stdin), Out: &out, Err: &errOut, TTY: e.tty, Secrets: e.secrets, Transport: e.fake.Transport(),
		Now: func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }}
	err := app.Execute(context.Background(), args)
	return out.String(), errOut.String(), err
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	e.tty.answers = []string{e.fake.URL(), "me@example.com", "2027-01-31"}
	e.tty.secrets = []string{"tok-me"}
	out, _, err := e.run(t, "auth", "login")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Logged in as Max Mustermann") {
		t.Errorf("out = %q", out)
	}
	cfg, err := config.Load("")
	if err != nil || cfg.TokenExpiry != "2027-01-31" || cfg.Privacy != config.PrivacyOwn {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if tok, _ := e.secrets.Get(secret.AccountKey(cfg.Site, cfg.Email)); tok != "tok-me" {
		t.Errorf("stored token = %q", tok)
	}
}

func TestLoginWithTokenFromStdin(t *testing.T) {
	e := newEnv(t)
	e.tty.noTTY = true
	_, _, err := e.runStdin(t, "tok-me\n", "auth", "login", "--site", e.fake.URL(), "--email", "me@example.com", "--expires", "2027-01-31", "--with-token")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load("")
	if tok, _ := e.secrets.Get(secret.AccountKey(cfg.Site, cfg.Email)); tok != "tok-me" {
		t.Errorf("stored token = %q", tok)
	}
}

func TestLoginRejectsBadTokenAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.tty.answers = []string{"me@example.com", ""}
	e.tty.secrets = []string{"wrong"}
	_, _, err := e.run(t, "auth", "login", "--site", e.fake.URL())
	if err == nil || !strings.Contains(err.Error(), "credentials rejected by Jira") {
		t.Fatalf("err = %v", err)
	}
	if _, err := config.Load(""); !errors.Is(err, config.ErrNotConfigured) {
		t.Errorf("config written: %v", err)
	}
}

func TestLoginRequiresTerminalForToken(t *testing.T) {
	e := newEnv(t)
	e.tty.noTTY = true
	_, _, err := e.run(t, "auth", "login", "--site", e.fake.URL(), "--email", "me@example.com", "--expires", "")
	if !errors.Is(err, errNoTerminal) {
		t.Errorf("err = %v", err)
	}
}

func TestNotLoggedIn(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.run(t, "mine")
	if !errors.Is(err, config.ErrNotConfigured) {
		t.Errorf("err = %v", err)
	}
}

func TestReads(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	out, _, err := e.run(t, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "**PROJ-1** [To Do] Mine") || strings.Contains(out, "PROJ-2") {
		t.Errorf("mine = %q", out)
	}
	if _, _, err := e.run(t, "mine", "--since", "-1d"); err != nil {
		t.Errorf("mine --since -1d: %v", err)
	}
	out, _, err = e.run(t, "show", "PROJ-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Alice") || !strings.Contains(out, `"description": "Ask Person A"`) {
		t.Errorf("show --json = %s", out)
	}
	if _, _, err := e.run(t, "show", "PROJ-2"); err == nil || !strings.Contains(err.Error(), "privacy scope") {
		t.Errorf("show PROJ-2: %v", err)
	}
}

func TestCommentNeedsYes(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.answers = []string{"n"}
	if _, _, err := e.run(t, "comment", "PROJ-1", "--body", "hello"); err == nil {
		t.Fatal("comment applied without confirmation")
	}
	if len(e.fake.Issues["PROJ-1"].Comments) != 0 {
		t.Fatal("comment stored after declining")
	}
	e.tty.answers = []string{"y"}
	out, _, err := e.run(t, "comment", "PROJ-1", "--body", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Added comment") || len(e.fake.Issues["PROJ-1"].Comments) != 1 {
		t.Errorf("out = %q", out)
	}
	if last := e.tty.asked[len(e.tty.asked)-1]; !strings.Contains(last, "Add a comment to PROJ-1") || !strings.Contains(last, "Jira site: "+e.fake.URL()+" (profile default)") {
		t.Errorf("prompt = %q", e.tty.asked)
	}
}

func TestTransitionNeedsTypedKey(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "transition", "PROJ-1", "Done"); err == nil {
		t.Fatal("destructive change accepted with y")
	}
	if e.fake.Issues["PROJ-1"].Status != "To Do" {
		t.Fatal("status changed")
	}
	e.tty.answers = []string{"proj-1"}
	if _, _, err := e.run(t, "transition", "PROJ-1", "Done"); err != nil {
		t.Fatal(err)
	}
	if e.fake.Issues["PROJ-1"].Status != "Done" {
		t.Errorf("status = %s", e.fake.Issues["PROJ-1"].Status)
	}
}

func TestWritesWithoutTerminalChangeNothing(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.noTTY = true
	for _, args := range [][]string{
		{"comment", "PROJ-1", "--body", "x"},
		{"update", "PROJ-1", "--set", "Summary=x"},
		{"transition", "PROJ-1", "Done"},
		{"log", "PROJ-1", "1h"},
		{"create", "--project", "PROJ", "--summary", "x"},
		{"auth", "logout"},
		{"config", "privacy", "off"},
	} {
		if _, _, err := e.run(t, args...); !errors.Is(err, errNoTerminal) {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	i := e.fake.Issues["PROJ-1"]
	if len(i.Comments) != 0 || i.Summary != "Mine" || i.Status != "To Do" || len(i.Worklogs) != 0 || len(e.fake.Issues) != 2 {
		t.Errorf("something changed: %+v", i)
	}
	if cfg, _ := config.Load(""); cfg.Privacy != config.PrivacyOwn {
		t.Error("privacy changed")
	}
}

func TestUpdateParsesSets(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.answers = []string{"PROJ-1"}
	if _, _, err := e.run(t, "update", "PROJ-1", "--set", "Summary=New = title", "--set", "Labels=a,b"); err != nil {
		t.Fatal(err)
	}
	if i := e.fake.Issues["PROJ-1"]; i.Summary != "New = title" || strings.Join(i.Labels, ",") != "a,b" {
		t.Errorf("issue = %+v", i)
	}
	if _, _, err := e.run(t, "update", "PROJ-1", "--set", "novalue"); err == nil {
		t.Error("bad --set accepted")
	}
}

func TestPrivacySwitch(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.answers = []string{"yes"}
	if _, _, err := e.run(t, "config", "privacy", "off"); err == nil {
		t.Fatal("privacy off accepted without typing off")
	}
	e.tty.answers = []string{"off"}
	if _, _, err := e.run(t, "config", "privacy", "off"); err != nil {
		t.Fatal(err)
	}
	out, _, err := e.run(t, "show", "PROJ-2")
	if err != nil || !strings.Contains(out, "Alice Smith") {
		t.Errorf("off mode show: %v\n%s", err, out)
	}
	e.tty.answers = nil
	if _, _, err := e.run(t, "config", "privacy", "own"); err != nil {
		t.Errorf("switching back to own needs no prompt: %v", err)
	}
}

func TestExpiryWarning(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	cfg, _ := config.Load("")
	cfg.TokenExpiry = "2026-10-15"
	if err := cfg.Save(""); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := e.run(t, "whoami")
	if err != nil || !strings.Contains(errOut, "expires on 2026-10-15 (in 7 days)") {
		t.Errorf("err=%v stderr=%q", err, errOut)
	}
}

func TestProfilesAreIsolated(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TIX_JIRA_CONFIG", "")
	t.Setenv("TIX_JIRA_PROFILE", "")

	e.tty.noTTY = true
	if _, _, err := e.runStdin(t, "tok-me\n", "auth", "login", "--profile", "test", "--site", e.fake.URL(), "--email", "me@example.com", "--expires", "", "--with-token"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, "mine", "--profile", "default"); !errors.Is(err, config.ErrNotConfigured) {
		t.Errorf("default profile must stay unconfigured: %v", err)
	}
	if out, _, err := e.run(t, "mine", "--profile", "test"); err != nil || !strings.Contains(out, "PROJ-1") {
		t.Errorf("--profile test: %v %q", err, out)
	}
	t.Setenv("TIX_JIRA_PROFILE", "test")
	out, _, err := e.run(t, "config", "profiles")
	if err != nil || !strings.Contains(out, "* test: "+e.fake.URL()) {
		t.Errorf("profiles: %v %q", err, out)
	}
	if _, _, err := e.run(t, "mine", "--profile", "../etc"); err == nil {
		t.Error("invalid profile name accepted")
	}
}

func TestDevBuildNeedsExplicitProfile(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	t.Setenv("TIX_JIRA_CONFIG", "")
	t.Setenv("TIX_JIRA_PROFILE", "")
	var out bytes.Buffer
	for _, v := range []string{"dev", "v0.0.0-20261009122403-4c701510dbd1", "v0.1.0+dirty", "v0.1.1-0.20261009122403-4c701510dbd1"} {
		app := &App{Version: v, In: strings.NewReader(""), Out: &out, Err: &out, TTY: e.tty, Secrets: e.secrets, Transport: e.fake.Transport(), Now: time.Now}
		if err := app.Execute(context.Background(), []string{"mine"}); !errors.Is(err, errDevNeedsProfile) {
			t.Errorf("version %s: err = %v", v, err)
		}
	}
	for _, v := range []string{"v0.1.0", "v1.2.3-rc.1"} {
		if !isRelease(v) {
			t.Errorf("%s should count as a release", v)
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/approvekit/keychain"
	"github.com/nyactl/approvekit/profile"
	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jiratest"
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
	secrets *keychain.Memory
	tty     *script
	dev     bool // run as a development build instead of a release
	opened  []string
}

func (e *env) version() string {
	if e.dev {
		return "v0.0.0-20261012100000-0123456789ab"
	}
	return "v9.9.9"
}

func newEnv(t *testing.T) *env {
	t.Setenv("TIX_JIRA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	f := jiratest.New(t)
	f.AddIssue(jiratest.Issue{Key: "PROJ-1", Summary: "Mine", Assignee: "acc-me", Reporter: "acc-alice", Description: jiratest.Doc("Ask Alice Smith")})
	f.AddIssue(jiratest.Issue{Key: "PROJ-2", Summary: "Not mine", Assignee: "acc-alice"})
	return &env{fake: f, secrets: keychain.NewMemory(), tty: &script{}}
}

func (e *env) loggedIn(t *testing.T) *env {
	t.Helper()
	cfg := &config.Config{Account: profile.Account{Site: e.fake.URL(), Email: "me@example.com"}, Privacy: config.PrivacyOwn}
	if err := cfg.Save(""); err != nil {
		t.Fatal(err)
	}
	if err := e.secrets.Set(keychain.AccountKey(cfg.Site, cfg.Email), "tok-me"); err != nil {
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
	app := &App{Version: e.version(), In: strings.NewReader(stdin), Out: &out, Err: &errOut, TTY: e.tty, Secrets: e.secrets, Transport: e.fake.Transport(),
		Gateway: e.fake.URL(), Now: func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) },
		Browse: func(_ context.Context, url string) error { e.opened = append(e.opened, url); return nil }}
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
	if tok, _ := e.secrets.Get(keychain.AccountKey(cfg.Site, cfg.Email)); tok != "tok-me" {
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
	if tok, _ := e.secrets.Get(keychain.AccountKey(cfg.Site, cfg.Email)); tok != "tok-me" {
		t.Errorf("stored token = %q", tok)
	}
}

func TestLoginRejectsBadTokenAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.tty.answers = []string{"me@example.com"}
	e.tty.secrets = []string{"wrong"}
	_, _, err := e.run(t, "auth", "login", "--site", e.fake.URL())
	if !errors.Is(err, errLoginRejected) || !strings.Contains(err.Error(), "API token itself (not your password)") {
		t.Fatalf("err = %v", err)
	}
	for _, q := range e.tty.asked {
		if strings.Contains(q, "expiry") {
			t.Errorf("asked for the expiry date after a rejected token: %q", e.tty.asked)
		}
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
		if !profile.IsRelease(v) {
			t.Errorf("%s should count as a release", v)
		}
	}
}

func profileHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TIX_JIRA_CONFIG", "")
	t.Setenv("TIX_JIRA_PROFILE", "")
}

func saveProfile(t *testing.T, e *env, name string, allowDev bool) {
	t.Helper()
	cfg := &config.Config{Account: profile.Account{Site: e.fake.URL(), Email: "me@example.com", AllowDevBuilds: allowDev}, Privacy: config.PrivacyOwn}
	if err := cfg.Save(name); err != nil {
		t.Fatal(err)
	}
	if err := e.secrets.Set(keychain.AccountKey(cfg.Site, cfg.Email), "tok-me"); err != nil {
		t.Fatal(err)
	}
}

func TestDevBuildsStayAwayFromProduction(t *testing.T) {
	e := newEnv(t)
	profileHome(t)
	saveProfile(t, e, "default", true) // even a hand-edited flag must not open the default profile
	saveProfile(t, e, "test", false)
	e.dev = true
	before := len(e.fake.Requests)

	for _, args := range [][]string{
		{"mine", "--profile", "default"},
		{"mine", "--profile", "test"},
		{"mcp", "--profile", "default"},
	} {
		if _, _, err := e.run(t, args...); !errors.Is(err, profile.ErrDevBuildNotAllowed) {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	e.tty.secrets = []string{"tok-me"}
	e.tty.answers = []string{""}
	if _, _, err := e.run(t, "auth", "login", "--profile", "default", "--site", e.fake.URL(), "--email", "me@example.com"); !errors.Is(err, profile.ErrDevBuildNotAllowed) {
		t.Errorf("dev build login to default: %v", err)
	}
	if n := len(e.fake.Requests) - before; n != 0 {
		t.Errorf("a development build sent %d requests to a profile it must not use", n)
	}

	saveProfile(t, e, "test", true)
	if out, _, err := e.run(t, "mine", "--profile", "test"); err != nil || !strings.Contains(out, "PROJ-1") {
		t.Errorf("opted-in test profile: %v %q", err, out)
	}
}

func TestDevBuildsOptIn(t *testing.T) {
	e := newEnv(t)
	profileHome(t)
	saveProfile(t, e, "default", false)
	saveProfile(t, e, "test", false)
	host := strings.TrimPrefix(e.fake.URL(), "https://")

	if _, _, err := e.run(t, "config", "dev-builds", "on", "--profile", "default"); err == nil {
		t.Error("default profile opted in")
	}
	e.tty.answers = []string{"yes"}
	if _, _, err := e.run(t, "config", "dev-builds", "on", "--profile", "test"); err == nil {
		t.Error("opted in without typing the host")
	}
	e.tty.answers = []string{host}
	if _, _, err := e.run(t, "config", "dev-builds", "on", "--profile", "test"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load("test"); !cfg.AllowDevBuilds {
		t.Error("flag not saved")
	}
	e.tty.noTTY = true
	if _, _, err := e.run(t, "config", "dev-builds", "on", "--profile", "default"); err == nil {
		t.Error("default profile opted in without a terminal")
	}
	if _, _, err := e.run(t, "config", "dev-builds", "off", "--profile", "test"); err != nil {
		t.Errorf("switching off needs no terminal: %v", err)
	}
}

func TestLoginWithScopedToken(t *testing.T) {
	e := newEnv(t)
	e.fake.AddUser(jiratest.User{AccountID: "acc-me", DisplayName: "Max Mustermann", Email: "me@example.com", Token: "tok-scoped",
		Scopes: []string{"read:jira-user", "read:jira-work", "write:jira-work"}})
	e.tty.noTTY = true
	out, _, err := e.runStdin(t, "tok-scoped\n", "auth", "login", "--site", e.fake.URL(), "--email", "me@example.com", "--expires", "", "--with-token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "scoped token, via the Atlassian API gateway") {
		t.Errorf("out = %q", out)
	}
	cfg, err := config.Load("")
	if err != nil || !cfg.ScopedToken || cfg.CloudID != jiratest.CloudID {
		t.Fatalf("config = %+v, %v", cfg, err)
	}

	out, _, err = e.run(t, "show", "PROJ-1")
	if err != nil || !strings.Contains(out, "# PROJ-1: Mine") || !strings.Contains(out, e.fake.URL()+"/browse/PROJ-1") {
		t.Errorf("show via gateway: %q, %v", out, err)
	}
	gateway := false
	for _, r := range e.fake.Requests {
		gateway = gateway || strings.HasPrefix(r, "GET /ex/jira/"+jiratest.CloudID+"/rest/api/3/issue/PROJ-1")
	}
	if !gateway {
		t.Errorf("requests did not use the gateway: %v", e.fake.Requests)
	}

	// Boards need Jira Software scopes this token does not have.
	_, _, err = e.run(t, "boards")
	if !errors.Is(err, atlassian.ErrMissingScope) || !strings.Contains(err.Error(), "read:board-scope:jira-software and read:project:jira") {
		t.Errorf("boards: %v", err)
	}
	out, _, _ = e.run(t, "config", "show")
	if !strings.Contains(out, "Token: scoped") {
		t.Errorf("config show = %q", out)
	}
}

func TestLoginScopedTokenWithoutUserScope(t *testing.T) {
	e := newEnv(t)
	e.fake.AddUser(jiratest.User{AccountID: "acc-me", DisplayName: "Max Mustermann", Email: "me@example.com", Token: "tok-scoped", Scopes: []string{"read:jira-work"}})
	e.tty.noTTY = true
	_, _, err := e.runStdin(t, "tok-scoped\n", "auth", "login", "--site", e.fake.URL(), "--email", "me@example.com", "--expires", "", "--with-token")
	if err == nil || !strings.Contains(err.Error(), "read:jira-user") {
		t.Fatalf("err = %v", err)
	}
	if _, err := config.Load(""); !errors.Is(err, config.ErrNotConfigured) {
		t.Errorf("config saved despite failed login: %v", err)
	}
}

func TestOpen(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	if _, _, err := e.run(t, "open", "proj-1"); err != nil {
		t.Fatal(err)
	}
	if len(e.opened) != 1 || e.opened[0] != e.fake.URL()+"/browse/PROJ-1" {
		t.Errorf("opened %v", e.opened)
	}
	out, _, err := e.run(t, "open", "PROJ-1", "--print")
	if err != nil || strings.TrimSpace(out) != e.fake.URL()+"/browse/PROJ-1" || len(e.opened) != 1 {
		t.Errorf("--print: %q, %v, opened %v", out, err, e.opened)
	}
	for _, key := range []string{"PROJ-2", "not a key", "PROJ-1; rm -rf ~"} {
		if _, _, err := e.run(t, "open", key); err == nil {
			t.Errorf("%q was opened", key)
		}
	}
	if len(e.opened) != 1 {
		t.Errorf("opened %v", e.opened)
	}
}

func TestSavedSearchesStayScoped(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	if _, _, err := e.run(t, "config", "search", "add", "everything", "project = PROJ ORDER BY created"); err != nil {
		t.Fatal(err)
	}
	out, _, err := e.run(t, "search", "@everything")
	if err != nil {
		t.Fatal(err)
	}
	last := e.fake.JQL[len(e.fake.JQL)-1]
	if last != "assignee = currentUser() AND (project = PROJ) ORDER BY created" {
		t.Errorf("JQL = %q", last)
	}
	if !strings.Contains(out, "PROJ-1") || strings.Contains(out, "PROJ-2") {
		t.Errorf("out = %q", out)
	}
	if _, _, err := e.run(t, "search", "@missing"); err == nil || !strings.Contains(err.Error(), "no saved search") {
		t.Errorf("missing search: %v", err)
	}
	out, _, _ = e.run(t, "config", "search", "list")
	if !strings.Contains(out, "@everything: project = PROJ ORDER BY created") {
		t.Errorf("list = %q", out)
	}
	if _, _, err := e.run(t, "config", "search", "remove", "everything"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(""); len(cfg.Searches) != 0 {
		t.Errorf("searches = %v", cfg.Searches)
	}
}

func TestCreateUsesDefaultProject(t *testing.T) {
	e := newEnv(t).loggedIn(t)
	e.tty.answers = []string{"y"}
	if _, _, err := e.run(t, "create", "--summary", "No project"); err == nil || !strings.Contains(err.Error(), "tix config project") {
		t.Fatalf("without default: %v", err)
	}
	if _, _, err := e.run(t, "config", "project", "proj"); err != nil {
		t.Fatal(err)
	}
	out, _, err := e.run(t, "create", "--summary", "Uses the default")
	if err != nil || !strings.Contains(out, "Created PROJ-") {
		t.Fatalf("create: %q, %v", out, err)
	}
	if !strings.Contains(e.tty.asked[len(e.tty.asked)-1], "Create a Task in PROJ:") {
		t.Errorf("asked %q", e.tty.asked[len(e.tty.asked)-1])
	}
	if out, _, _ := e.run(t, "config", "project", "--clear"); !strings.Contains(out, "No default project") {
		t.Errorf("clear: %q", out)
	}
}

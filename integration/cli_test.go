//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/approvekit/keychain"
	"github.com/nyactl/tix-for-jira/internal/cli"
)

// yes answers every confirmation with "y" and picks the line with key.
type yes struct{ key string }

func (yes) Prompt(string) (string, error) { return "y", nil }
func (yes) Secret(string) (string, error) { return "", errors.New("no secrets in tests") }

func (y yes) Pick(_ string, lines []string) (int, cli.PickAction, error) {
	for i, l := range lines {
		if strings.HasPrefix(l, y.key+" ") {
			return i, cli.PickShow, nil
		}
	}
	return 0, cli.PickQuit, nil
}

// TestCLISavedSearchAndDefaultProject runs the real command line against the
// test site, with a temporary copy of the test profile.
func TestCLISavedSearchAndDefaultProject(t *testing.T) {
	s := setup(t)
	cfg := *s.cfg
	cfg.Searches = map[string]string{"it": "project = " + s.project + " AND labels = tix-for-jira-it AND summary ~ \"cli " + s.run + "\""}
	cfg.DefaultProject = s.project
	t.Setenv("TIX_JIRA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := cfg.Save("test"); err != nil {
		t.Fatal(err)
	}
	var picked string
	run := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		app := &cli.App{Version: "dev", In: strings.NewReader(""), Out: &out, Err: &errOut, TTY: yes{key: picked},
			Secrets: keychain.New("tix-for-jira"), Now: time.Now}
		if err := app.Execute(context.Background(), append(args, "--profile", "test")); err != nil {
			t.Fatalf("tix %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
		}
		return out.String()
	}

	out := run("create", "--summary", "tix-for-jira cli "+s.run, "--label", "tix-for-jira-it")
	key := regexp.MustCompile(`Created ([A-Z][A-Z0-9_]*-[0-9]+)`).FindStringSubmatch(out)
	if key == nil || !strings.HasPrefix(key[1], s.project+"-") {
		t.Fatalf("create without --project: %q", out)
	}
	closeWhenDone(t, s.jc, key[1])

	// The search index catches up within seconds.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if out := run("search", "@it"); strings.Contains(out, key[1]) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("saved search did not find %s", key[1])
		}
		time.Sleep(3 * time.Second)
	}

	// The picker offers the new ticket, and choosing it shows it.
	picked = key[1]
	if out := run("pick", "--project", s.project); !strings.Contains(out, "# "+key[1]+":") {
		t.Errorf("pick did not show %s: %q", key[1], out)
	}
}

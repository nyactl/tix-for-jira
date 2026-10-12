package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyactl/approvekit/profile"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	t.Setenv("TIX_JIRA_CONFIG", path)

	if _, err := Load(""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Load before save: %v", err)
	}
	c := &Config{Account: profile.Account{Site: "team.atlassian.net", Email: "me@example.com", TokenExpiry: "2027-01-31"}, Privacy: PrivacyOff}
	if err := c.Save(""); err != nil {
		t.Fatal(err)
	}
	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Account: profile.Account{Site: "https://team.atlassian.net", Email: "me@example.com", TokenExpiry: "2027-01-31"}, Privacy: PrivacyOff}
	if *got != want {
		t.Errorf("loaded %+v", got)
	}
}

// TestReadsExistingFiles keeps config files written by earlier versions
// readable.
func TestReadsExistingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TIX_JIRA_CONFIG", path)
	old := `{"site":"https://team.atlassian.net","email":"me@example.com","privacy":"own","tokenExpiry":"2027-01-31","allowDevBuilds":true,"scopedToken":true,"cloudId":"0b8e7c1a-5d2f-4e3a-9b6c-1f2e3d4c5b6a"}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Site != "https://team.atlassian.net" || c.Privacy != PrivacyOwn || !c.AllowDevBuilds || !c.ScopedToken || c.CloudID == "" || c.TokenExpiry != "2027-01-31" {
		t.Errorf("loaded %+v", c)
	}
}

func TestLoadDefaultsPrivacyToOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TIX_JIRA_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{"site":"https://team.atlassian.net","email":"me@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Privacy != PrivacyOwn {
		t.Errorf("privacy = %q, want own", c.Privacy)
	}
}

func TestValidateRejectsPrivacy(t *testing.T) {
	t.Parallel()
	c := Config{Account: profile.Account{Site: "https://team.atlassian.net", Email: "me@example.com"}, Privacy: "some"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "invalid privacy") {
		t.Errorf("err = %v", err)
	}
}

func TestExpiryWarningNamesLogin(t *testing.T) {
	t.Parallel()
	c := Config{Account: profile.Account{TokenExpiry: "2026-10-20"}}
	if got := c.ExpiryWarning(time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)); !strings.Contains(got, "in 12 days") || !strings.Contains(got, "tix auth login") {
		t.Errorf("got %q", got)
	}
}

func TestProfilePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TIX_JIRA_CONFIG", "")
	t.Setenv("TIX_JIRA_PROFILE", "test")
	p, err := Path("")
	if err != nil || filepath.Base(p) != "test.json" || !strings.Contains(p, filepath.Join("tix-for-jira", "profiles")) {
		t.Errorf("Path = %s, %v", p, err)
	}
}

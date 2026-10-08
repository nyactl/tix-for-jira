package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want, wantErr string
	}{
		{in: "team.atlassian.net", want: "https://team.atlassian.net"},
		{in: "https://Team.Atlassian.net/", want: "https://team.atlassian.net"},
		{in: " https://team.atlassian.net ", want: "https://team.atlassian.net"},
		{in: "http://team.atlassian.net", wantErr: "must use https"},
		{in: "https://team.atlassian.net/jira", wantErr: "without a path"},
		{in: "https://u:p@team.atlassian.net", wantErr: "credentials"},
		{in: "https://team.atlassian.net?x=1", wantErr: "query"},
		{in: "", wantErr: "required"},
		{in: "https://", wantErr: "no host"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeSite(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	t.Setenv("TIX_JIRA_CONFIG", path)

	if _, err := Load(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Load before save: %v", err)
	}
	c := &Config{Site: "team.atlassian.net", Email: "me@example.com", Privacy: PrivacyOwn, TokenExpiry: "2027-01-31"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config permissions = %o, want 600", perm)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != (Config{Site: "https://team.atlassian.net", Email: "me@example.com", Privacy: PrivacyOwn, TokenExpiry: "2027-01-31"}) {
		t.Errorf("loaded %+v", got)
	}
}

func TestLoadDefaultsPrivacyToOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TIX_JIRA_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{"site":"https://team.atlassian.net","email":"me@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Privacy != PrivacyOwn {
		t.Errorf("privacy = %q, want own", c.Privacy)
	}
}

func TestValidateRejects(t *testing.T) {
	t.Parallel()
	base := Config{Site: "https://team.atlassian.net", Email: "me@example.com", Privacy: PrivacyOwn}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"bad email", func(c *Config) { c.Email = "not-an-email" }, "invalid email"},
		{"display name email", func(c *Config) { c.Email = "Me <me@example.com>" }, "invalid email"},
		{"bad privacy", func(c *Config) { c.Privacy = "some" }, "invalid privacy"},
		{"bad expiry", func(c *Config) { c.TokenExpiry = "31.01.2027" }, "invalid token expiry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := base
			tt.mutate(&c)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestExpiryWarning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		expiry string
		want   string
	}{
		{"", ""},
		{"2026-12-31", ""},
		{"2026-10-20", "in 12 days"},
		{"2026-10-08", "in 0 days"},
		{"2026-10-01", "expired"},
	}
	for _, tt := range tests {
		c := Config{TokenExpiry: tt.expiry}
		got := c.ExpiryWarning(now)
		if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
			t.Errorf("expiry %q: got %q, want containing %q", tt.expiry, got, tt.want)
		}
	}
}

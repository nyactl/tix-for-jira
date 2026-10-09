// Package config stores the non-secret settings: Jira site, account email,
// privacy mode and token expiry. The API token itself lives in the Keychain
// (see package secret).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PrivacyMode controls what the assistant can see; see docs/design.md.
type PrivacyMode string

const (
	PrivacyOwn PrivacyMode = "own"
	PrivacyOff PrivacyMode = "off"
)

// Config is persisted as JSON in the user's config directory.
type Config struct {
	Site        string      `json:"site"`
	Email       string      `json:"email"`
	Privacy     PrivacyMode `json:"privacy"`
	TokenExpiry string      `json:"tokenExpiry,omitempty"` // YYYY-MM-DD
}

// ErrNotConfigured is returned by Load when no login has happened yet.
var ErrNotConfigured = errors.New("this profile is not set up yet; run `tix auth login` (add --profile <name> for other profiles)")

// DefaultProfile is used when no profile is selected.
const DefaultProfile = "default"

var profileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ResolveProfile returns profile, or TIX_JIRA_PROFILE, or DefaultProfile.
func ResolveProfile(profile string) (string, error) {
	if profile == "" {
		profile = os.Getenv("TIX_JIRA_PROFILE")
	}
	if profile == "" {
		return DefaultProfile, nil
	}
	if !profileRe.MatchString(profile) {
		return "", fmt.Errorf("invalid profile name %q: use lowercase letters, digits and dashes", profile)
	}
	return profile, nil
}

func dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tix-for-jira"), nil
}

// Path returns the config file of a profile. TIX_JIRA_CONFIG overrides it.
func Path(profile string) (string, error) {
	if p := os.Getenv("TIX_JIRA_CONFIG"); p != "" {
		return p, nil
	}
	profile, err := ResolveProfile(profile)
	if err != nil {
		return "", err
	}
	d, err := dir()
	if err != nil {
		return "", err
	}
	if profile == DefaultProfile {
		return filepath.Join(d, "config.json"), nil
	}
	return filepath.Join(d, "profiles", profile+".json"), nil
}

// Profiles lists the profiles that have a config file.
func Profiles() ([]string, error) {
	d, err := dir()
	if err != nil {
		return nil, err
	}
	var out []string
	if _, err := os.Stat(filepath.Join(d, "config.json")); err == nil {
		out = append(out, DefaultProfile)
	}
	entries, err := os.ReadDir(filepath.Join(d, "profiles"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok && profileRe.MatchString(name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// Load reads and validates a profile's config file.
func Load(profile string) (*Config, error) {
	p, err := Path(profile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p) //nolint:gosec // G304: the path is the user's own config location
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	if c.Privacy == "" {
		c.Privacy = PrivacyOwn
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &c, nil
}

// Save validates and writes a profile's config file with owner-only
// permissions.
func (c *Config) Save(profile string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	p, err := Path(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Validate checks every field.
func (c *Config) Validate() error {
	site, err := NormalizeSite(c.Site)
	if err != nil {
		return err
	}
	c.Site = site
	if _, err := mail.ParseAddress(c.Email); err != nil || strings.ContainsAny(c.Email, "<> ") {
		return fmt.Errorf("invalid email %q", c.Email)
	}
	switch c.Privacy {
	case PrivacyOwn, PrivacyOff:
	default:
		return fmt.Errorf("invalid privacy mode %q (want %q or %q)", c.Privacy, PrivacyOwn, PrivacyOff)
	}
	if c.TokenExpiry != "" {
		if _, err := time.Parse(time.DateOnly, c.TokenExpiry); err != nil {
			return fmt.Errorf("invalid token expiry %q (want YYYY-MM-DD)", c.TokenExpiry)
		}
	}
	return nil
}

// NormalizeSite accepts "example.atlassian.net" or "https://example.atlassian.net/"
// and returns "https://example.atlassian.net". Only https is allowed.
func NormalizeSite(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("site is required, e.g. https://your-team.atlassian.net")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid site: %w", err)
	}
	switch {
	case u.Scheme != "https":
		return "", fmt.Errorf("site must use https, got %q", u.Scheme)
	case u.Hostname() == "":
		return "", errors.New("site has no host")
	case u.User != nil:
		return "", errors.New("site must not contain credentials")
	case u.RawQuery != "" || u.Fragment != "":
		return "", errors.New("site must not contain a query or fragment")
	case strings.Trim(u.Path, "/") != "":
		return "", errors.New("site must be the bare site URL without a path")
	}
	return "https://" + strings.ToLower(u.Host), nil
}

// ExpiryWarning returns a message when the recorded token expiry is within
// two weeks or has passed, and "" otherwise.
func (c *Config) ExpiryWarning(now time.Time) string {
	if c.TokenExpiry == "" {
		return ""
	}
	exp, err := time.Parse(time.DateOnly, c.TokenExpiry)
	if err != nil {
		return ""
	}
	days := int(exp.Sub(now.Truncate(24*time.Hour)).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("your Jira API token expired on %s; create a new one and run `tix auth login`", c.TokenExpiry)
	case days <= 14:
		return fmt.Sprintf("your Jira API token expires on %s (in %d days); create a new one and run `tix auth login`", c.TokenExpiry, days)
	}
	return ""
}

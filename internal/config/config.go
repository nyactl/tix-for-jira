// Package config stores the non-secret settings: Jira site, account email,
// privacy mode and token expiry, per profile. The API token itself lives in
// the Keychain. Profiles, validation and the development-build lock come
// from approvekit's profile package.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/nyactl/approvekit/profile"
)

// PrivacyMode controls what the assistant can see; see docs/design.md.
type PrivacyMode string

const (
	PrivacyOwn PrivacyMode = "own"
	PrivacyOff PrivacyMode = "off"
)

// Config is persisted as JSON in the user's config directory.
type Config struct {
	profile.Account
	Privacy PrivacyMode `json:"privacy"`
}

// DefaultProfile is used when no profile is selected; it is meant for
// production.
const DefaultProfile = profile.Default

// ErrNotConfigured is returned by Load when no login has happened yet.
var ErrNotConfigured = errors.New("this profile is not set up yet; run `tix auth login` (add --profile <name> for other profiles)")

// Store locates tix's profiles.
var Store = profile.Store{App: "tix-for-jira", ProfileEnv: "TIX_JIRA_PROFILE", ConfigEnv: "TIX_JIRA_CONFIG"}

// ResolveProfile returns profile, or TIX_JIRA_PROFILE, or DefaultProfile.
func ResolveProfile(name string) (string, error) { return Store.Resolve(name) }

// Path returns the config file of a profile. TIX_JIRA_CONFIG overrides it.
func Path(name string) (string, error) { return Store.Path(name) }

// Profiles lists the profiles that have a config file.
func Profiles() ([]string, error) { return Store.List() }

// Load reads and validates a profile's config file.
func Load(name string) (*Config, error) {
	var c Config
	if err := Store.Load(name, &c); err != nil {
		if errors.Is(err, profile.ErrNotConfigured) {
			return nil, ErrNotConfigured
		}
		return nil, err
	}
	return &c, nil
}

// Save validates and writes a profile's config file with owner-only
// permissions.
func (c *Config) Save(name string) error { return Store.Save(name, c) }

// Validate checks every field. A missing privacy mode means own.
func (c *Config) Validate() error {
	if c.Privacy == "" {
		c.Privacy = PrivacyOwn
	}
	if err := c.Account.Validate(); err != nil {
		return err
	}
	switch c.Privacy {
	case PrivacyOwn, PrivacyOff:
	default:
		return fmt.Errorf("invalid privacy mode %q (want %q or %q)", c.Privacy, PrivacyOwn, PrivacyOff)
	}
	return nil
}

// ExpiryWarning returns a message when the recorded token expiry is within
// two weeks or has passed, and "" otherwise.
func (c *Config) ExpiryWarning(now time.Time) string {
	return c.Account.ExpiryWarning(now, "run `tix auth login`")
}

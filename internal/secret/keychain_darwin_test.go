package secret

import (
	"errors"
	"os"
	"testing"
)

// TestKeychainRoundTrip writes to the real login keychain, so it only runs
// when TIX_JIRA_KEYCHAIN_TEST=1.
func TestKeychainRoundTrip(t *testing.T) {
	if os.Getenv("TIX_JIRA_KEYCHAIN_TEST") != "1" {
		t.Skip("set TIX_JIRA_KEYCHAIN_TEST=1 to test against the login keychain")
	}
	k := NewKeychain()
	key := AccountKey("https://keychain-test.invalid", "test@example.com")
	t.Cleanup(func() { _ = k.Delete(key) })

	if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before Set: %v", err)
	}
	for _, tok := range []string{"first", "second"} {
		if err := k.Set(key, tok); err != nil {
			t.Fatal(err)
		}
		if got, err := k.Get(key); err != nil || got != tok {
			t.Fatalf("Get = %q, %v; want %q", got, err, tok)
		}
	}
	if err := k.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: %v", err)
	}
}

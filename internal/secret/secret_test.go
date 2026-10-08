package secret

import (
	"errors"
	"testing"
)

func TestMemory(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	key := AccountKey("https://team.atlassian.net", "me@example.com")
	if _, err := m.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty store: %v", err)
	}
	if err := m.Set(key, "tok"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Get(key); err != nil || got != "tok" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := m.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
}

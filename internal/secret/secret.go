// Package secret stores the Jira API token outside the config file.
package secret

import (
	"errors"
	"sync"
)

// ErrNotFound means no token is stored for the account.
var ErrNotFound = errors.New("no API token stored; run `tix-jira auth login`")

// Store keeps one token per account key (site and email).
type Store interface {
	Get(account string) (string, error)
	Set(account, token string) error
	Delete(account string) error
}

// AccountKey identifies the token for a site and email.
func AccountKey(site, email string) string { return email + "@" + site }

// Memory is an in-process Store for tests.
type Memory struct {
	mu     sync.Mutex
	tokens map[string]string
}

func NewMemory() *Memory { return &Memory{tokens: make(map[string]string)} }

func (m *Memory) Get(account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[account]
	if !ok {
		return "", ErrNotFound
	}
	return t, nil
}

func (m *Memory) Set(account, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[account] = token
	return nil
}

func (m *Memory) Delete(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, account)
	return nil
}

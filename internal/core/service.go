// Package core implements every tix-jira operation on top of the Jira
// client. It is the only package that sees raw Jira data: it enforces the
// privacy scope, replaces people with labels, converts rich text to
// Markdown and returns plain views. Writes are returned as plans that the
// caller must confirm with the human before applying.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nyactl/tix-jira/internal/adf"
	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/jira"
	"github.com/nyactl/tix-jira/internal/privacy"
)

// ErrNotFound is returned for missing tickets and for tickets outside the
// privacy scope alike, so the scope does not leak which keys exist.
var ErrNotFound = errors.New("ticket not found, not visible to you, or outside your privacy scope")

// Service runs operations for one user.
type Service struct {
	jc     *jira.Client
	people *privacy.People
	now    func() time.Time

	fieldsMu sync.Mutex
	fields   map[string]jira.Field // field ID -> definition
}

// New verifies the credentials and prepares a service.
func New(ctx context.Context, jc *jira.Client, mode config.PrivacyMode) (*Service, error) {
	me, err := jc.Myself(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{jc: jc, people: privacy.NewPeople(mode, me), now: time.Now}, nil
}

// Mode returns the privacy mode.
func (s *Service) Mode() config.PrivacyMode { return s.people.Mode() }

// Site returns the Jira site URL.
func (s *Service) Site() string { return s.jc.Site() }

func (s *Service) issueURL(key string) string { return s.jc.Site() + "/browse/" + key }

// scoped fetches the fields needed for a scope check and returns ErrNotFound
// when the ticket is missing or out of scope.
func (s *Service) scoped(ctx context.Context, key string, fields ...string) (*jira.Issue, error) {
	k, err := jira.NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	issue, err := s.jc.GetIssueFields(ctx, k, append([]string{"assignee", "reporter", "summary", "status", "issuetype"}, fields...))
	if errors.Is(err, jira.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.people.InScope(issue.Fields.Assignee) {
		return nil, ErrNotFound
	}
	s.learn(issue)
	return issue, nil
}

// learn registers the people on an issue, so their names are known before
// any free text of the issue is redacted.
func (s *Service) learn(issue *jira.Issue) {
	f := issue.Fields
	s.people.User(f.Assignee)
	s.people.User(f.Reporter)
	for _, a := range f.Attachments {
		s.people.User(a.Author)
	}
	for id, raw := range issue.Raw {
		if !strings.HasPrefix(id, "customfield_") {
			continue
		}
		var users []jira.User
		var one jira.User
		switch {
		case json.Unmarshal(raw, &one) == nil && one.AccountID != "":
			s.people.User(&one)
		case json.Unmarshal(raw, &users) == nil:
			for i := range users {
				if users[i].AccountID != "" {
					s.people.User(&users[i])
				}
			}
		}
	}
}

// markdown renders ADF with people replaced, then redacts known names in
// the remaining text.
func (s *Service) markdown(raw json.RawMessage) string {
	md, err := adf.ToMarkdown(raw, s.people.Mention)
	if err != nil {
		return "(content could not be converted)"
	}
	return s.people.Text(md)
}

// toADF converts Markdown written by the assistant or user into ADF,
// resolving @[label] mentions.
func (s *Service) toADF(md string) (json.RawMessage, error) {
	if md == "" {
		return nil, nil
	}
	return adf.FromMarkdown(md, s.people.Resolve)
}

func (s *Service) fieldDefs(ctx context.Context) (map[string]jira.Field, error) {
	s.fieldsMu.Lock()
	defer s.fieldsMu.Unlock()
	if s.fields != nil {
		return s.fields, nil
	}
	list, err := s.jc.Fields(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading field definitions: %w", err)
	}
	s.fields = make(map[string]jira.Field, len(list))
	for _, f := range list {
		s.fields[f.ID] = f
	}
	return s.fields, nil
}

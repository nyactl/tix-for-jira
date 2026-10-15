// Package core implements every tix-for-jira operation on top of the Jira
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

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/approvekit/people"
	adf "github.com/nyactl/go-adf"
	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
	"github.com/nyactl/tix-for-jira/internal/privacy"
)

// ErrNotFound is returned for missing tickets and for tickets outside the
// privacy scope alike, so the scope does not leak which keys exist.
var ErrNotFound = errors.New("ticket not found, not visible to you, or outside your privacy scope")

// Service runs operations for one user.
type Service struct {
	jc     *jira.Client
	people *people.People
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
	return &Service{jc: jc, people: people.New(people.Mode(mode), person(me)), now: time.Now}, nil
}

// KeepLabels keeps people's labels in store, so a person has the same label
// in every command and session of the profile, and mentions can name people
// shown earlier.
func (s *Service) KeepLabels(store people.LabelStore) { s.people.WithStore(store) }

// Mode returns the privacy mode.
func (s *Service) Mode() config.PrivacyMode { return config.PrivacyMode(s.people.Mode()) }

func person(u *jira.User) people.Person {
	return people.Person{AccountID: u.AccountID, DisplayName: u.DisplayName, Email: u.EmailAddress}
}

// user returns the label for a Jira user, or "" for nil.
func (s *Service) user(u *jira.User) string {
	if u == nil {
		return ""
	}
	p := person(u)
	return s.people.Person(&p)
}

// visible reports whether an issue with this assignee may be shown.
func (s *Service) visible(assignee *jira.User) bool {
	return privacy.InScope(s.people, assignee)
}

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
	if errors.Is(err, atlassian.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.visible(issue.Fields.Assignee) {
		return nil, ErrNotFound
	}
	s.learn(issue)
	return issue, nil
}

// learn registers the people on an issue, so their names are known before
// any free text of the issue is redacted.
func (s *Service) learn(issue *jira.Issue) {
	f := issue.Fields
	s.user(f.Assignee)
	s.user(f.Reporter)
	for _, a := range f.Attachments {
		s.user(a.Author)
	}
	for id, raw := range issue.Raw {
		if !strings.HasPrefix(id, "customfield_") {
			continue
		}
		var users []jira.User
		var one jira.User
		switch {
		case json.Unmarshal(raw, &one) == nil && one.AccountID != "":
			s.user(&one)
		case json.Unmarshal(raw, &users) == nil:
			for i := range users {
				if users[i].AccountID != "" {
					s.user(&users[i])
				}
			}
		}
	}
}

// markdown renders ADF with people replaced. Known names are redacted in
// each text piece before conversion, where Markdown escaping cannot split
// them, and again in the result, for names spread over several pieces.
func (s *Service) markdown(raw json.RawMessage) string {
	s0 := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s0, "{") {
		var doc adf.Node
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "(content could not be converted)"
		}
		s.redactText(&doc)
		redacted, err := json.Marshal(doc)
		if err != nil {
			return "(content could not be converted)"
		}
		raw = redacted
	}
	md, err := adf.ToMarkdown(raw, adf.WithMentionText(s.people.Mention))
	if err != nil {
		return "(content could not be converted)"
	}
	return s.people.Text(md)
}

func (s *Service) redactText(n *adf.Node) {
	if n.Text != "" {
		n.Text = s.people.Text(n.Text)
	}
	if t, ok := n.Attrs["text"].(string); ok && n.Type != "mention" {
		n.Attrs["text"] = s.people.Text(t)
	}
	for i := range n.Content {
		s.redactText(&n.Content[i])
	}
}

// toADF converts Markdown written by the assistant or user into ADF,
// resolving @[label] mentions.
func (s *Service) toADF(md string) (json.RawMessage, error) {
	if md == "" {
		return nil, nil
	}
	doc, err := adf.FromMarkdown(md, adf.WithMentionResolver(s.people.Resolve))
	var me *adf.MentionError
	if errors.As(err, &me) {
		return nil, fmt.Errorf("cannot mention @[%s]: only people already shown in this session can be mentioned", me.Label)
	}
	return doc, err
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

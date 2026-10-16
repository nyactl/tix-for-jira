package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

// ShowEpicSummaries makes epics outside the privacy scope show their
// summaries, redacted; otherwise only their key, type and status are shown.
func (s *Service) ShowEpicSummaries(on bool) { s.epicSummaries = on }

func aboveStandard(t *jira.IssueType) bool { return t != nil && t.HierarchyLevel > 0 }

// epicView describes a ticket above standard tickets that may be outside
// the privacy scope. No people are learned from it.
func (s *Service) epicView(issue *jira.Issue) Related {
	f := issue.Fields
	v := Related{Relation: "epic", Key: issue.Key}
	if f.IssueType != nil {
		v.Type = f.IssueType.Name
	}
	if f.Status != nil {
		v.Status = f.Status.Name
	}
	if s.visible(f.Assignee) || s.epicSummaries {
		v.Summary = s.people.Text(f.Summary)
	}
	return v
}

func describeRelated(r Related) string {
	var parts []string
	for _, p := range []string{r.Type, r.Status} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	out := r.Key
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	if r.Summary != "" {
		out += " " + r.Summary
	}
	return out
}

// parentFor fetches the ticket that is to become the parent of child. Your
// own tickets qualify as before. A ticket of someone else qualifies only
// when its type is above standard tickets, such as an epic: tix reads its
// key, type and status and, if allowed, its summary, nothing else.
func (s *Service) parentFor(ctx context.Context, key, child string) (*jira.Issue, string, error) {
	k, err := jira.NormalizeKey(key)
	if err != nil {
		return nil, "", err
	}
	if k == child {
		return nil, "", errors.New("a ticket cannot be its own parent")
	}
	issue, err := s.jc.GetIssueFields(ctx, k, []string{"assignee", "summary", "status", "issuetype"})
	if errors.Is(err, atlassian.ErrNotFound) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if s.visible(issue.Fields.Assignee) {
		s.learn(issue)
		return issue, s.people.Text(headline(issue)), nil
	}
	if !aboveStandard(issue.Fields.IssueType) {
		return nil, "", ErrNotFound
	}
	return issue, describeRelated(s.epicView(issue)), nil
}

// Epics lists the open tickets of a project above standard tickets, such
// as epics, newest first, at most limit (default 50). They may be outside the privacy scope, so each
// shows only its key, type and status and, if allowed, its summary.
func (s *Service) Epics(ctx context.Context, project string, limit int) ([]Related, error) {
	pk, err := jira.NormalizeProjectKey(project)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	types, err := s.jc.ProjectIssueTypes(ctx, pk)
	if errors.Is(err, atlassian.ErrNotFound) {
		return nil, fmt.Errorf("project %s not found", pk)
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for i := range types {
		if aboveStandard(&types[i]) {
			ids = append(ids, types[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("project %s has no epics or other types above standard tickets", pk)
	}
	jql := fmt.Sprintf("project = %s AND issuetype in (%s) AND statusCategory != Done ORDER BY created DESC", pk, strings.Join(ids, ", "))
	issues, err := s.jc.Search(ctx, jql, []string{"assignee", "summary", "status", "issuetype"}, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Related, 0, len(issues))
	for i := range issues {
		// The query is not trusted to keep to these types.
		if aboveStandard(issues[i].Fields.IssueType) {
			out = append(out, s.epicView(&issues[i]))
		}
	}
	return out, nil
}

package core

import (
	"context"
	"errors"
	"strings"

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

func aboveStandard(t *jira.IssueType) bool { return t != nil && t.HierarchyLevel > 0 }

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
// when its type is above standard tickets, such as an epic: tix then reads
// only its key, type and status, which tix shows for any parent anyway.
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
	v := Related{Key: issue.Key}
	if t := issue.Fields.IssueType; t != nil {
		v.Type = t.Name
	}
	if st := issue.Fields.Status; st != nil {
		v.Status = st.Name
	}
	return issue, describeRelated(v), nil
}

// MyEpics lists the open epics that your tickets belong to, most recently
// used first, optionally only those of one project. They come from your
// own tickets' parents, so they show no more than `tix show` does for a
// parent: key, type and status (and the summary in privacy mode off).
func (s *Service) MyEpics(ctx context.Context, project string) ([]Related, error) {
	es, _, err := s.myEpics(ctx, project)
	return es, err
}

// MyEpicsForPicker is MyEpics with every epic's summary, names replaced.
// Use it only for the interactive picker, which only the person at the
// terminal sees; never print or return these summaries.
func (s *Service) MyEpicsForPicker(ctx context.Context, project string) ([]Related, error) {
	es, summaries, err := s.myEpics(ctx, project)
	for i := range es {
		es[i].Summary = s.people.Text(summaries[i])
	}
	return es, err
}

func (s *Service) myEpics(ctx context.Context, project string) ([]Related, []string, error) {
	pk := ""
	if project != "" {
		var err error
		if pk, err = jira.NormalizeProjectKey(project); err != nil {
			return nil, nil, err
		}
	}
	issues, err := s.jc.Search(ctx, "assignee = currentUser() AND parent is not EMPTY ORDER BY updated DESC", []string{"assignee", "parent"}, 200)
	if err != nil {
		return nil, nil, err
	}
	var out []Related
	var summaries []string
	seen := map[string]bool{}
	for _, is := range issues {
		p := is.Fields.Parent
		// The query is not trusted to keep to your tickets with epics.
		if is.Fields.Assignee == nil || is.Fields.Assignee.AccountID != s.people.MeID() || p == nil || !aboveStandard(p.Fields.IssueType) {
			continue
		}
		if seen[p.Key] || (pk != "" && !strings.HasPrefix(p.Key, pk+"-")) || (p.Fields.Status != nil && p.Fields.Status.StatusCategory.Key == "done") {
			continue
		}
		seen[p.Key] = true
		r := s.related("epic", p)
		out = append(out, r)
		summaries = append(summaries, p.Fields.Summary)
	}
	return out, summaries, nil
}

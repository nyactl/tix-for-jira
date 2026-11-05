package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nyactl/tix-for-jira/internal/jira"
)

// PlanMove prepares changing a ticket between standard and subtask types
// within its project. With a parent, the ticket becomes a subtask of that
// ticket, or moves there if it already is one; typ then picks the subtask
// type and may be empty. Without a parent, a subtask becomes a standard
// ticket of type typ.
func (s *Service) PlanMove(ctx context.Context, key, parent, typ string) (*Plan, error) {
	issue, err := s.scoped(ctx, key, "project", "parent", "subtasks")
	if err != nil {
		return nil, err
	}
	f := issue.Fields
	if f.IssueType == nil || f.Project == nil {
		return nil, fmt.Errorf("jira did not return the type and project of %s", issue.Key)
	}
	from := f.IssueType
	if aboveStandard(from) {
		return nil, fmt.Errorf("%s is a %s; only standard tickets and subtasks can be moved", issue.Key, from.Name)
	}
	if len(f.Subtasks) > 0 {
		return nil, fmt.Errorf("%s has %d subtask(s); Jira cannot nest subtasks, so move or convert them first", issue.Key, len(f.Subtasks))
	}
	parent, typ = strings.TrimSpace(parent), strings.TrimSpace(typ)
	if parent == "" && !from.Subtask {
		return nil, fmt.Errorf("%s is no subtask; name the ticket it should go under", issue.Key)
	}
	if parent == "" && typ == "" {
		return nil, fmt.Errorf("name the type %s should become, such as Story or Task", issue.Key)
	}
	types, err := s.jc.CreatableIssueTypes(ctx, f.Project.Key)
	if err != nil {
		return nil, err
	}
	to, err := moveTarget(types, from, parent != "", typ, f.Project.Key)
	if err != nil {
		return nil, err
	}

	var newParent *jira.Issue
	lines := []string{"Move " + s.people.Text(headline(issue)) + ":"}
	if from.ID != to.ID {
		lines = append(lines, fmt.Sprintf("- Type: %s -> %s", from.Name, to.Name))
	}
	oldParent := "none"
	if f.Parent != nil {
		oldParent = describeRelated(s.related("parent", f.Parent))
	}
	if parent != "" {
		var desc string
		newParent, desc, err = s.parentFor(ctx, parent, issue.Key)
		if err != nil {
			return nil, fmt.Errorf("parent %s: %w", parent, err)
		}
		if pt := newParent.Fields.IssueType; pt == nil || pt.Subtask || aboveStandard(pt) {
			return nil, fmt.Errorf("a subtask goes under a standard ticket, not under %s", newParent.Key)
		}
		if !strings.HasPrefix(newParent.Key, f.Project.Key+"-") {
			return nil, fmt.Errorf("%s is in another project; tix moves tickets only within %s", newParent.Key, f.Project.Key)
		}
		if from.ID == to.ID && f.Parent != nil && f.Parent.Key == newParent.Key {
			return nil, fmt.Errorf("%s already is a %s of %s", issue.Key, to.Name, newParent.Key)
		}
		lines = append(lines, fmt.Sprintf("- Parent: %s -> %s", oldParent, desc))
	} else {
		lines = append(lines, fmt.Sprintf("- Parent: %s -> none", oldParent))
	}

	if f.Status != nil {
		statuses, err := s.jc.ProjectStatuses(ctx, f.Project.Key)
		if err != nil {
			return nil, err
		}
		if slices.Contains(statuses[to.ID], f.Status.Name) {
			lines = append(lines, fmt.Sprintf("- Status: stays %s", f.Status.Name))
		} else {
			lines = append(lines, fmt.Sprintf("- Status: %s is not in the %s workflow; Jira sets its default status", f.Status.Name, to.Name))
		}
	}
	if to.Subtask && !from.Subtask {
		lines = append(lines, "As a subtask it follows its parent's epic and sprint.")
	}

	parentKey := ""
	if newParent != nil {
		parentKey = newParent.Key
	}
	return &Plan{
		Kind:        Destructive,
		Key:         issue.Key,
		Description: strings.Join(lines, "\n"),
		apply: func(ctx context.Context) (string, error) {
			if err := s.jc.MoveIssue(ctx, f.Project.Key, issue.Key, to.ID, parentKey); err != nil {
				return "", err
			}
			if parentKey != "" {
				return fmt.Sprintf("Moved %s: now a %s of %s.", issue.Key, to.Name, parentKey), nil
			}
			return fmt.Sprintf("Moved %s: now a %s.", issue.Key, to.Name), nil
		},
	}, nil
}

// MoveParents lists your open tickets that key could become a subtask of:
// standard tickets in its project other than key itself.
func (s *Service) MoveParents(ctx context.Context, key string) ([]IssueSummary, error) {
	k, err := jira.NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	project, _, _ := strings.Cut(k, "-")
	types, err := s.jc.CreatableIssueTypes(ctx, project)
	if err != nil {
		return nil, err
	}
	standard := map[string]bool{}
	for _, t := range types {
		if !t.Subtask && !aboveStandard(&t) {
			standard[t.Name] = true
		}
	}
	list, err := s.MyIssues(ctx, MyIssuesOptions{Project: project, Max: 200})
	if err != nil {
		return nil, err
	}
	var out []IssueSummary
	for _, is := range list {
		if is.Key != k && standard[is.Type] {
			out = append(out, is)
		}
	}
	return out, nil
}

// moveTarget picks the issue type a ticket of type from becomes: a subtask
// type when it gets a parent, a standard type otherwise.
func moveTarget(types []jira.IssueType, from *jira.IssueType, toSubtask bool, name, project string) (*jira.IssueType, error) {
	var candidates []jira.IssueType
	var names []string
	for _, t := range types {
		if t.Subtask == toSubtask && !aboveStandard(&t) {
			candidates = append(candidates, t)
			names = append(names, t.Name)
		}
	}
	kind := "standard"
	if toSubtask {
		kind = "subtask"
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%s has no %s issue type", project, kind)
	}
	if name != "" {
		for i := range candidates {
			if looseName(candidates[i].Name) == looseName(name) {
				return &candidates[i], nil
			}
		}
		return nil, fmt.Errorf("%q is no %s type in %s; choose one of: %s", name, kind, project, strings.Join(names, ", "))
	}
	if toSubtask && from.Subtask {
		for i := range candidates {
			if candidates[i].ID == from.ID {
				return &candidates[i], nil
			}
		}
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	return nil, errors.New("name the type, one of: " + strings.Join(names, ", "))
}

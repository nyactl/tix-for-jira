package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nyactl/tix-jira/internal/jira"
)

// Kind classifies a write for the confirmation step.
type Kind string

const (
	Additive    Kind = "additive"
	Destructive Kind = "destructive"
)

// Plan is a prepared write. Description states exactly what Apply will do;
// callers must show it to the human and get approval first. A plan can be
// applied once.
type Plan struct {
	Kind        Kind
	Key         string // the ticket the user confirms against (empty for create)
	Description string

	once  sync.Once
	apply func(context.Context) (string, error)
}

// Apply performs the write and returns a short result message.
func (p *Plan) Apply(ctx context.Context) (string, error) {
	var (
		msg string
		err = errors.New("plan was already applied")
	)
	p.once.Do(func() { msg, err = p.apply(ctx) })
	return msg, err
}

func headline(issue *jira.Issue) string {
	return issue.Key + " (" + issue.Fields.Summary + ")"
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// PlanComment prepares a comment written in Markdown.
func (s *Service) PlanComment(ctx context.Context, key, body string) (*Plan, error) {
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("comment is empty")
	}
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	doc, err := s.toADF(body)
	if err != nil {
		return nil, err
	}
	return &Plan{
		Kind:        Additive,
		Key:         issue.Key,
		Description: fmt.Sprintf("Add a comment to %s:\n\n%s", s.people.Text(headline(issue)), indent(body)),
		apply: func(ctx context.Context) (string, error) {
			c, err := s.jc.AddComment(ctx, issue.Key, doc)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Added comment %s to %s.", c.ID, issue.Key), nil
		},
	}, nil
}

// FieldInput is one requested field change; Value is user-facing text
// (Markdown for rich text fields, comma-separated for lists).
type FieldInput struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// PlanUpdate prepares field changes. Changing people fields is not supported.
func (s *Service) PlanUpdate(ctx context.Context, key string, changes []FieldInput) (*Plan, error) {
	if len(changes) == 0 {
		return nil, errors.New("no fields to change")
	}
	k, err := jira.NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	issue, err := s.jc.GetIssue(ctx, k)
	if errors.Is(err, jira.ErrNotFound) || (err == nil && !s.people.InScope(issue.Fields.Assignee)) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.learn(issue)
	meta, err := s.jc.EditMeta(ctx, issue.Key)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	var lines []string
	for _, ch := range changes {
		id, m, err := findField(meta, ch.Field)
		if err != nil {
			return nil, err
		}
		if _, dup := fields[id]; dup {
			return nil, fmt.Errorf("field %s is listed twice", m.Name)
		}
		k := fieldKind(id, m)
		if k == kindUnsupported {
			return nil, fmt.Errorf("%s cannot be changed with tix-jira", m.Name)
		}
		v, err := s.convert(k, m, ch.Value)
		if err != nil {
			return nil, err
		}
		fields[id] = v
		old, _ := s.renderValue(issue.Raw[id])
		lines = append(lines, fmt.Sprintf("- %s:\n    from: %s\n    to:   %s", m.Name, describe(old), describe(strings.TrimSpace(ch.Value))))
	}
	return &Plan{
		Kind:        Destructive,
		Key:         issue.Key,
		Description: fmt.Sprintf("Change %s:\n%s", s.people.Text(headline(issue)), strings.Join(lines, "\n")),
		apply: func(ctx context.Context) (string, error) {
			if err := s.jc.EditIssue(ctx, issue.Key, fields); err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated %d field(s) on %s.", len(fields), issue.Key), nil
		},
	}, nil
}

func describe(v string) string {
	if v == "" {
		return "(empty)"
	}
	if strings.Contains(v, "\n") {
		return "\n" + indent(indent(v))
	}
	return strconv.Quote(v)
}

// PlanTransition prepares a status change. target matches a transition
// name or the name of the status it leads to.
func (s *Service) PlanTransition(ctx context.Context, key, target, comment string) (*Plan, error) {
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	ts, err := s.jc.Transitions(ctx, issue.Key)
	if err != nil {
		return nil, err
	}
	target = strings.TrimSpace(target)
	var byName, byStatus []jira.Transition
	for _, t := range ts {
		if strings.EqualFold(t.Name, target) {
			byName = append(byName, t)
		}
		if strings.EqualFold(t.To.Name, target) {
			byStatus = append(byStatus, t)
		}
	}
	matches := byName
	if len(matches) == 0 {
		matches = byStatus
	}
	if len(matches) != 1 {
		var options []string
		for _, t := range ts {
			options = append(options, fmt.Sprintf("%q (to %s)", t.Name, t.To.Name))
		}
		reason := "no transition matches"
		if len(matches) > 1 {
			reason = "several transitions match"
		}
		return nil, fmt.Errorf("%s %q on %s; available: %s", reason, target, issue.Key, strings.Join(options, ", "))
	}
	t := matches[0]
	current := ""
	if issue.Fields.Status != nil {
		current = issue.Fields.Status.Name
	}
	doc, err := s.toADF(comment)
	if err != nil {
		return nil, err
	}
	desc := fmt.Sprintf("Move %s from %q to %q (transition %q).", s.people.Text(headline(issue)), current, t.To.Name, t.Name)
	if comment != "" {
		desc += "\n\nWith comment:\n\n" + indent(comment)
	}
	return &Plan{
		Kind:        Destructive,
		Key:         issue.Key,
		Description: desc,
		apply: func(ctx context.Context) (string, error) {
			if err := s.jc.DoTransition(ctx, issue.Key, t.ID, doc); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s is now %s.", issue.Key, t.To.Name), nil
		},
	}, nil
}

// CreateInput describes a new ticket. The ticket is assigned to the user so
// it stays within the privacy scope.
type CreateInput struct {
	Project     string            `json:"project"`
	Type        string            `json:"type"`
	Summary     string            `json:"summary"`
	Description string            `json:"description,omitempty"`
	Labels      []string          `json:"labels,omitempty"`
	Priority    string            `json:"priority,omitempty"`
	Parent      string            `json:"parent,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
}

// PlanCreate prepares a new ticket.
func (s *Service) PlanCreate(ctx context.Context, in CreateInput) (*Plan, error) {
	pk, err := jira.NormalizeProjectKey(in.Project)
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(in.Summary)
	if summary == "" || strings.ContainsAny(summary, "\r\n") {
		return nil, errors.New("summary must be a single non-empty line")
	}
	types, err := s.jc.CreatableIssueTypes(ctx, pk)
	if err != nil {
		return nil, err
	}
	var typ *jira.IssueType
	var typeNames []string
	for i := range types {
		typeNames = append(typeNames, types[i].Name)
		if strings.EqualFold(types[i].Name, strings.TrimSpace(in.Type)) {
			typ = &types[i]
		}
	}
	if typ == nil {
		return nil, fmt.Errorf("issue type %q is not available in %s; choose one of: %s", in.Type, pk, strings.Join(typeNames, ", "))
	}
	metaList, err := s.jc.CreateFields(ctx, pk, typ.ID)
	if err != nil {
		return nil, err
	}
	meta := make(map[string]jira.FieldMeta, len(metaList))
	for _, m := range metaList {
		meta[m.FieldID] = m
	}

	fields := map[string]any{
		"project":   map[string]string{"key": pk},
		"issuetype": map[string]string{"id": typ.ID},
		"summary":   summary,
	}
	lines := []string{fmt.Sprintf("Create a %s in %s:", typ.Name, pk), "- Summary: " + strconv.Quote(summary), "- Assignee: Me"}

	if in.Parent != "" || typ.Subtask {
		if in.Parent == "" {
			return nil, fmt.Errorf("a %s needs a parent ticket", typ.Name)
		}
		parent, err := s.scoped(ctx, in.Parent)
		if err != nil {
			return nil, fmt.Errorf("parent %s: %w", in.Parent, err)
		}
		fields["parent"] = map[string]string{"key": parent.Key}
		lines = append(lines, "- Parent: "+s.people.Text(headline(parent)))
	}
	set := func(id, label, value string) error {
		m, ok := meta[id]
		if !ok {
			return fmt.Errorf("%s cannot be set when creating a %s in %s", label, typ.Name, pk)
		}
		k := fieldKind(id, withSet(m))
		if k == kindUnsupported {
			return fmt.Errorf("%s cannot be set with tix-jira", m.Name)
		}
		v, err := s.convert(k, m, value)
		if err != nil {
			return err
		}
		fields[id] = v
		lines = append(lines, fmt.Sprintf("- %s: %s", m.Name, describe(value)))
		return nil
	}
	if in.Description != "" {
		if err := set("description", "Description", in.Description); err != nil {
			return nil, err
		}
	}
	if len(in.Labels) > 0 {
		if err := set("labels", "Labels", strings.Join(in.Labels, ",")); err != nil {
			return nil, err
		}
	}
	if in.Priority != "" {
		if err := set("priority", "Priority", in.Priority); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(in.Fields))
	for n := range in.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		id, _, err := findField(meta, n)
		if err != nil {
			return nil, fmt.Errorf("field %q is not on the create screen for %s in %s", n, typ.Name, pk)
		}
		if _, dup := fields[id]; dup {
			return nil, fmt.Errorf("field %s is given twice", n)
		}
		if err := set(id, n, in.Fields[n]); err != nil {
			return nil, err
		}
	}

	handled := map[string]bool{"project": true, "issuetype": true, "reporter": true, "assignee": true}
	var missing []string
	for id, m := range meta {
		if _, ok := fields[id]; !ok && m.Required && !handled[id] {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("the project requires these fields: %s", strings.Join(missing, ", "))
	}

	_, assignOnCreate := meta["assignee"]
	if assignOnCreate {
		fields["assignee"] = map[string]string{"accountId": s.people.MeID()}
	}
	return &Plan{
		Kind:        Additive,
		Description: strings.Join(lines, "\n"),
		apply: func(ctx context.Context) (string, error) {
			key, err := s.jc.CreateIssue(ctx, fields)
			if err != nil {
				return "", err
			}
			if !assignOnCreate {
				if err := s.jc.AssignIssue(ctx, key, s.people.MeID()); err != nil {
					return fmt.Sprintf("Created %s, but could not assign it to you (%v); it may fall outside your privacy scope.", key, err), nil
				}
			}
			return fmt.Sprintf("Created %s: %s", key, s.issueURL(key)), nil
		},
	}, nil
}

// withSet treats create-screen fields as settable; createmeta does not
// always list operations.
func withSet(m jira.FieldMeta) jira.FieldMeta {
	if !contains(m.Operations, "set") {
		m.Operations = append(append([]string(nil), m.Operations...), "set")
	}
	return m
}

var durationRe = regexp.MustCompile(`^\s*(?:(\d+(?:\.\d+)?)\s*h)?\s*(?:(\d+)\s*m)?\s*$`)

// ParseDuration accepts "1h 30m", "1h30m", "90m" or "1.5h". Days and weeks
// are rejected because their length depends on site settings.
func ParseDuration(s string) (int, error) {
	m := durationRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil || (m[1] == "" && m[2] == "") {
		return 0, fmt.Errorf("invalid duration %q: use hours and minutes, e.g. 1h 30m or 45m", s)
	}
	secs := 0.0
	if m[1] != "" {
		h, _ := strconv.ParseFloat(m[1], 64)
		secs += h * 3600
	}
	if m[2] != "" {
		mins, _ := strconv.Atoi(m[2])
		secs += float64(mins) * 60
	}
	total := int(math.Round(secs/60)) * 60
	if total < 60 || total > 24*3600 {
		return 0, fmt.Errorf("duration %q must be between 1 minute and 24 hours", s)
	}
	return total, nil
}

func formatSeconds(secs int) string {
	h, m := secs/3600, (secs%3600)/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}

func (s *Service) parseStart(started string) (time.Time, error) {
	now := s.now()
	started = strings.TrimSpace(started)
	if started == "" {
		return now, nil
	}
	loc := now.Location()
	if t, err := time.ParseInLocation("15:04", started, loc); err == nil {
		return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc), nil
	}
	for _, layout := range []string{"2006-01-02 15:04", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, started, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid start %q: use 09:30, 2026-10-08 09:30 or RFC 3339", started)
}

// PlanWorklog prepares logging time on a ticket.
func (s *Service) PlanWorklog(ctx context.Context, key, duration, started, comment string) (*Plan, error) {
	secs, err := ParseDuration(duration)
	if err != nil {
		return nil, err
	}
	start, err := s.parseStart(started)
	if err != nil {
		return nil, err
	}
	if start.After(s.now().Add(time.Minute)) {
		return nil, errors.New("work cannot start in the future")
	}
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	doc, err := s.toADF(comment)
	if err != nil {
		return nil, err
	}
	desc := fmt.Sprintf("Log %s on %s, started %s.", formatSeconds(secs), s.people.Text(headline(issue)), start.Format("2006-01-02 15:04"))
	if comment != "" {
		desc += "\n\nWith comment:\n\n" + indent(comment)
	}
	return &Plan{
		Kind:        Additive,
		Key:         issue.Key,
		Description: desc,
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.jc.AddWorklog(ctx, issue.Key, secs, start, doc); err != nil {
				return "", err
			}
			return fmt.Sprintf("Logged %s on %s.", formatSeconds(secs), issue.Key), nil
		},
	}, nil
}

// PlanLink prepares a link "key <relation> other", e.g. "PROJ-1 blocks
// PROJ-2". relation is a link type's outward or inward phrase, or its name.
func (s *Service) PlanLink(ctx context.Context, key, relation, other string) (*Plan, error) {
	from, err := s.scoped(ctx, key, "issuelinks")
	if err != nil {
		return nil, err
	}
	to, err := s.scoped(ctx, other)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", other, err)
	}
	if from.Key == to.Key {
		return nil, errors.New("a ticket cannot be linked to itself")
	}
	types, err := s.jc.LinkTypes(ctx)
	if err != nil {
		return nil, err
	}
	relation = strings.TrimSpace(relation)
	var lt *jira.LinkType
	outward := true
	var phrases []string
	for i := range types {
		t := &types[i]
		phrases = append(phrases, t.Outward)
		if t.Inward != t.Outward {
			phrases = append(phrases, t.Inward)
		}
		switch {
		case strings.EqualFold(t.Outward, relation) || strings.EqualFold(t.Name, relation):
			lt, outward = t, true
		case strings.EqualFold(t.Inward, relation):
			lt, outward = t, false
		}
		if lt != nil {
			break
		}
	}
	if lt == nil {
		return nil, fmt.Errorf("unknown relation %q; use one of: %s", relation, strings.Join(phrases, ", "))
	}
	phrase := lt.Outward
	subject, object := from.Key, to.Key
	if !outward {
		phrase = lt.Inward
		subject, object = to.Key, from.Key
	}
	for _, l := range from.Fields.IssueLinks {
		if l.Type.Name == lt.Name && ((l.OutwardIssue != nil && l.OutwardIssue.Key == to.Key) || (l.InwardIssue != nil && l.InwardIssue.Key == to.Key)) {
			return nil, fmt.Errorf("%s and %s are already linked with %q", from.Key, to.Key, lt.Name)
		}
	}
	return &Plan{
		Kind:        Additive,
		Key:         from.Key,
		Description: fmt.Sprintf("Link %s %s %s.", s.people.Text(headline(from)), phrase, s.people.Text(headline(to))),
		apply: func(ctx context.Context) (string, error) {
			if err := s.jc.CreateLink(ctx, lt.Name, subject, object); err != nil {
				return "", err
			}
			return s.verifyLink(ctx, from.Key, to.Key, lt.Name, outward, phrase)
		},
	}, nil
}

// verifyLink reads the link back, because Jira's create endpoint is easy to
// get backwards.
func (s *Service) verifyLink(ctx context.Context, key, other, typeName string, outward bool, phrase string) (string, error) {
	issue, err := s.jc.GetIssueFields(ctx, key, []string{"issuelinks"})
	if err != nil {
		return fmt.Sprintf("Linked %s %s %s (could not verify: %v).", key, phrase, other, err), nil
	}
	for _, l := range issue.Fields.IssueLinks {
		if l.Type.Name != typeName {
			continue
		}
		if outward && l.OutwardIssue != nil && l.OutwardIssue.Key == other {
			return fmt.Sprintf("Linked: %s %s %s.", key, phrase, other), nil
		}
		if !outward && l.InwardIssue != nil && l.InwardIssue.Key == other {
			return fmt.Sprintf("Linked: %s %s %s.", key, phrase, other), nil
		}
		if (outward && l.InwardIssue != nil && l.InwardIssue.Key == other) || (!outward && l.OutwardIssue != nil && l.OutwardIssue.Key == other) {
			return "", fmt.Errorf("the %s link between %s and %s was recorded in the opposite direction; please correct it in Jira", typeName, key, other)
		}
	}
	return fmt.Sprintf("Linked %s %s %s (link not visible yet).", key, phrase, other), nil
}

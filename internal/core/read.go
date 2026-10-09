package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/jira"
	"github.com/nyactl/tix-for-jira/internal/sanitize"
)

// summaryFields includes the reporter only to learn their name for redaction.
var summaryFields = []string{"summary", "status", "issuetype", "priority", "assignee", "reporter", "updated", "duedate", "labels"}

const (
	maxAttachmentBytes = 50 << 20
	maxInlineBytes     = 1 << 20
	maxChangeText      = 300
)

// Whoami describes the authenticated user and settings.
func (s *Service) Whoami(ctx context.Context) (*Me, error) {
	u, err := s.jc.Myself(ctx)
	if err != nil {
		return nil, err
	}
	return &Me{DisplayName: u.DisplayName, Email: u.EmailAddress, Site: s.jc.Site(), Privacy: string(s.people.Mode())}, nil
}

// MyIssuesOptions filters the list of tickets assigned to the user.
type MyIssuesOptions struct {
	IncludeDone bool
	Since       string // "-7d", "-12h", "-2w" or "2026-10-01"
	Project     string
	Text        string
	Max         int
}

var relativeRe = regexp.MustCompile(`^-[1-9][0-9]{0,3}[mhdw]$`)

// MyIssues lists tickets assigned to the user, most recently updated first.
func (s *Service) MyIssues(ctx context.Context, o MyIssuesOptions) ([]IssueSummary, error) {
	conds := []string{"assignee = currentUser()"}
	if !o.IncludeDone {
		conds = append(conds, "statusCategory != Done")
	}
	if o.Since != "" {
		since, err := sinceJQL(o.Since)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "updated >= "+since)
	}
	if o.Project != "" {
		pk, err := jira.NormalizeProjectKey(o.Project)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "project = "+pk)
	}
	if o.Text != "" {
		conds = append(conds, "text ~ "+quoteJQL(o.Text))
	}
	return s.search(ctx, strings.Join(conds, " AND ")+" ORDER BY updated DESC", o.Max)
}

// Search runs JQL limited to the privacy scope.
func (s *Service) Search(ctx context.Context, jql string, max int) ([]IssueSummary, error) {
	scoped, err := s.people.ScopeJQL(jql)
	if err != nil {
		return nil, err
	}
	return s.search(ctx, scoped, max)
}

func (s *Service) search(ctx context.Context, jql string, max int) ([]IssueSummary, error) {
	if max <= 0 {
		max = 50
	}
	issues, err := s.jc.Search(ctx, jql, summaryFields, min(max, 200))
	if err != nil {
		return nil, err
	}
	var in []*jira.Issue
	for i := range issues {
		if s.people.InScope(issues[i].Fields.Assignee) {
			s.learn(&issues[i])
			in = append(in, &issues[i])
		}
	}
	out := make([]IssueSummary, 0, len(in))
	for _, i := range in {
		out = append(out, s.summary(i))
	}
	return out, nil
}

func sinceJQL(since string) (string, error) {
	if relativeRe.MatchString(since) {
		return `"` + since + `"`, nil
	}
	if _, err := time.Parse(time.DateOnly, since); err == nil {
		return `"` + since + `"`, nil
	}
	return "", fmt.Errorf("invalid since %q: use e.g. -7d, -12h, -2w or 2026-10-01", since)
}

func quoteJQL(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

func (s *Service) summary(i *jira.Issue) IssueSummary {
	f := i.Fields
	v := IssueSummary{
		Key:     i.Key,
		Summary: s.people.Text(f.Summary),
		Updated: s.when(f.Updated),
		Due:     f.DueDate,
		Labels:  f.Labels,
	}
	if f.IssueType != nil {
		v.Type = f.IssueType.Name
	}
	if f.Status != nil {
		v.Status, v.StatusCategory = f.Status.Name, f.Status.StatusCategory.Key
	}
	if f.Priority != nil {
		v.Priority = f.Priority.Name
	}
	return v
}

// when converts a Jira timestamp to local "2006-01-02 15:04".
func (s *Service) when(ts string) string {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.In(s.now().Location()).Format("2006-01-02 15:04")
		}
	}
	return ts
}

// Issue returns a ticket with its details.
func (s *Service) Issue(ctx context.Context, key string) (*IssueDetail, error) {
	k, err := jira.NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	issue, err := s.jc.GetIssue(ctx, k)
	if errors.Is(err, jira.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f := issue.Fields
	if !s.people.InScope(f.Assignee) {
		return nil, ErrNotFound
	}

	s.learn(issue)
	d := &IssueDetail{
		URL:      s.issueURL(issue.Key),
		Assignee: s.people.User(f.Assignee),
		Reporter: s.people.User(f.Reporter),
		Created:  s.when(f.Created),
	}
	if d.Assignee == "" {
		d.Assignee = "Unassigned"
	}
	for _, a := range f.Attachments {
		d.Attachments = append(d.Attachments, AttachmentInfo{
			ID: a.ID, Filename: sanitize.String(path.Base(a.Filename)), MimeType: a.MimeType, Size: a.Size,
			Author: s.people.User(a.Author), Created: s.when(a.Created),
		})
	}
	d.Fields = s.customFields(ctx, issue)

	d.IssueSummary = s.summary(issue)
	if f.Project != nil {
		d.Project = f.Project.Key
	}
	if f.Resolution != nil {
		d.Resolution = f.Resolution.Name
	}
	for _, c := range f.Components {
		d.Components = append(d.Components, c.Name)
	}
	d.Description = s.markdown(f.Description)
	if f.Parent != nil {
		r := s.related("parent", f.Parent)
		d.Parent = &r
	}
	for i := range f.Subtasks {
		d.Subtasks = append(d.Subtasks, s.related("subtask", &f.Subtasks[i]))
	}
	for _, l := range f.IssueLinks {
		switch {
		case l.OutwardIssue != nil:
			d.Links = append(d.Links, s.related(l.Type.Outward, l.OutwardIssue))
		case l.InwardIssue != nil:
			d.Links = append(d.Links, s.related(l.Type.Inward, l.InwardIssue))
		}
	}
	if tt := f.TimeTracking; tt != nil && (tt.OriginalEstimate != "" || tt.RemainingEstimate != "" || tt.TimeSpent != "") {
		d.TimeTracking = &TimeTracking{Original: tt.OriginalEstimate, Remaining: tt.RemainingEstimate, Spent: tt.TimeSpent}
	}
	return d, nil
}

func (s *Service) related(relation string, r *jira.RelatedIssue) Related {
	v := Related{Relation: relation, Key: r.Key}
	if r.Fields.IssueType != nil {
		v.Type = r.Fields.IssueType.Name
	}
	if r.Fields.Status != nil {
		v.Status = r.Fields.Status.Name
	}
	if s.people.Mode() == config.PrivacyOff {
		v.Summary = s.people.Text(r.Fields.Summary)
	}
	return v
}

// customFields renders non-empty custom fields by name. Values whose shape
// is not understood are left out rather than dumped as raw JSON.
func (s *Service) customFields(ctx context.Context, issue *jira.Issue) []FieldValue {
	names := issue.Names
	var defs map[string]jira.Field
	var out []FieldValue
	for id, raw := range issue.Raw {
		if !strings.HasPrefix(id, "customfield_") {
			continue
		}
		val, ok := s.renderValue(raw)
		if !ok {
			continue
		}
		name := names[id]
		if name == "" {
			if defs == nil {
				defs, _ = s.fieldDefs(ctx)
			}
			name = defs[id].Name
		}
		if name == "" {
			name = id
		}
		out = append(out, FieldValue{Name: name, Value: val})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Service) renderValue(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		if t == "" || t == "{}" {
			return "", false
		}
		return s.people.Text(t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	case map[string]any:
		if t["type"] == "doc" {
			md := s.markdown(raw)
			return md, md != ""
		}
		if id, ok := t["accountId"].(string); ok {
			name, _ := t["displayName"].(string)
			email, _ := t["emailAddress"].(string)
			return s.people.ID(id, name, email), true
		}
		for _, k := range []string{"value", "name", "title"} {
			if str, ok := t[k].(string); ok && str != "" {
				if child, ok := t["child"].(map[string]any); ok {
					if cv, ok := child["value"].(string); ok {
						str += " / " + cv
					}
				}
				return s.people.Text(str), true
			}
		}
		return "", false
	case []any:
		var parts []string
		for _, item := range t {
			b, _ := json.Marshal(item)
			if p, ok := s.renderValue(b); ok {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, ", "), len(parts) > 0
	}
	return "", false
}

// Comments returns up to max comments, oldest first.
func (s *Service) Comments(ctx context.Context, key string, max int) ([]CommentView, error) {
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		max = 50
	}
	cs, err := s.jc.Comments(ctx, issue.Key, max)
	if err != nil {
		return nil, err
	}
	out := make([]CommentView, len(cs))
	for i, c := range cs {
		out[i] = CommentView{ID: c.ID, Author: s.people.User(c.Author), Created: s.when(c.Created), Edited: c.Updated != "" && c.Updated != c.Created}
	}
	for i, c := range cs {
		out[i].Body = s.markdown(c.Body)
	}
	return out, nil
}

var personFields = map[string]bool{"assignee": true, "reporter": true, "creator": true}

// History returns change history entries, oldest first. since filters by
// time ("-7d" or a date) and may be empty.
func (s *Service) History(ctx context.Context, key, since string, max int) ([]ChangeView, error) {
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	var cutoff time.Time
	if since != "" {
		if cutoff, err = s.parseSince(since); err != nil {
			return nil, err
		}
	}
	if max <= 0 {
		max = 100
	}
	entries, err := s.jc.Changelog(ctx, issue.Key, 1000)
	if err != nil {
		return nil, err
	}
	defs, _ := s.fieldDefs(ctx)

	var out []ChangeView
	for _, e := range entries {
		if !cutoff.IsZero() {
			if t, err := time.Parse("2006-01-02T15:04:05.000-0700", e.Created); err == nil && t.Before(cutoff) {
				continue
			}
		}
		v := ChangeView{Author: s.people.User(e.Author), When: s.when(e.Created)}
		for _, it := range e.Items {
			v.Changes = append(v.Changes, s.change(it, defs))
		}
		out = append(out, v)
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out, nil
}

func (s *Service) change(it jira.ChangeItem, defs map[string]jira.Field) FieldChange {
	c := FieldChange{Field: it.Field}
	def, known := defs[it.FieldID]
	isPerson := personFields[strings.ToLower(it.Field)] || (known && def.Schema != nil && def.Schema.Type == "user")
	isPeople := known && def.Schema != nil && def.Schema.Type == "array" && def.Schema.Items == "user"
	switch {
	case isPeople:
		if s.people.Mode() == config.PrivacyOwn {
			c.From, c.To = "(people)", "(people)"
		} else {
			c.From, c.To = it.FromString, it.ToString
		}
	case isPerson:
		// Names in history are from the time of the change, so they are
		// treated like mention text: redacted, but never the display name.
		if it.From != "" {
			c.From = s.people.Mention(it.From, it.FromString)
		}
		if it.To != "" {
			c.To = s.people.Mention(it.To, it.ToString)
		}
	default:
		c.From, c.To = clip(s.people.Text(it.FromString)), clip(s.people.Text(it.ToString))
	}
	return c
}

func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxChangeText {
		return s
	}
	return string(r[:maxChangeText]) + "…"
}

func (s *Service) parseSince(since string) (time.Time, error) {
	if relativeRe.MatchString(since) {
		n, _ := strconv.Atoi(since[1 : len(since)-1])
		unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[since[len(since)-1]]
		return s.now().Add(-time.Duration(n) * unit), nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, since, s.now().Location()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid since %q: use e.g. -7d, -12h, -2w or 2026-10-01", since)
}

// Worklogs returns logged work, oldest first.
func (s *Service) Worklogs(ctx context.Context, key string, max int) ([]WorklogView, error) {
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		max = 100
	}
	// The endpoint lists oldest first and cannot sort, so fetch generously
	// and keep the newest.
	ws, err := s.jc.Worklogs(ctx, issue.Key, 1000)
	if err != nil {
		return nil, err
	}
	if len(ws) > max {
		ws = ws[len(ws)-max:]
	}
	out := make([]WorklogView, len(ws))
	for i, w := range ws {
		out[i] = WorklogView{ID: w.ID, Author: s.people.User(w.Author), Started: s.when(w.Started), TimeSpent: w.TimeSpent}
	}
	for i, w := range ws {
		out[i].Comment = s.markdown(w.Comment)
	}
	return out, nil
}

// Transitions lists the status changes available to the user.
func (s *Service) Transitions(ctx context.Context, key string) ([]TransitionView, error) {
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	ts, err := s.jc.Transitions(ctx, issue.Key)
	if err != nil {
		return nil, err
	}
	out := make([]TransitionView, len(ts))
	for i, t := range ts {
		out[i] = TransitionView{Name: t.Name, To: t.To.Name, Category: t.To.StatusCategory.Key}
	}
	return out, nil
}

// EditableFields lists the fields the user may change on a ticket.
func (s *Service) EditableFields(ctx context.Context, key string) ([]EditableField, error) {
	issue, err := s.scoped(ctx, key)
	if err != nil {
		return nil, err
	}
	meta, err := s.jc.EditMeta(ctx, issue.Key)
	if err != nil {
		return nil, err
	}
	var out []EditableField
	for id, m := range meta {
		kind := fieldKind(id, m)
		if kind == kindUnsupported {
			continue
		}
		ef := EditableField{ID: id, Name: m.Name, Type: kind.String(), Required: m.Required}
		ef.Allowed = allowedNames(m.AllowedValues, 50)
		out = append(out, ef)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LinkTypes lists the issue link types of the site.
func (s *Service) LinkTypes(ctx context.Context) ([]LinkTypeView, error) {
	lts, err := s.jc.LinkTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LinkTypeView, len(lts))
	for i, l := range lts {
		out[i] = LinkTypeView{Name: l.Name, Outward: l.Outward, Inward: l.Inward}
	}
	return out, nil
}

// Projects lists projects in which the user can create tickets.
func (s *Service) Projects(ctx context.Context) ([]ProjectView, error) {
	ps, err := s.jc.Projects(ctx, "create", 200)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectView, len(ps))
	for i, p := range ps {
		out[i] = ProjectView{Key: p.Key, Name: p.Name}
	}
	return out, nil
}

// IssueTypes lists the issue types the user can create in a project.
func (s *Service) IssueTypes(ctx context.Context, project string) ([]IssueTypeView, error) {
	ts, err := s.jc.CreatableIssueTypes(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]IssueTypeView, len(ts))
	for i, t := range ts {
		out[i] = IssueTypeView{Name: t.Name, Subtask: t.Subtask}
	}
	return out, nil
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

// Attachment downloads an attachment of an in-scope ticket into dir and
// returns its path. Small text and image files are also returned inline.
func (s *Service) Attachment(ctx context.Context, key, id, dir string) (*AttachmentFile, error) {
	issue, err := s.scoped(ctx, key, "attachment")
	if err != nil {
		return nil, err
	}
	var meta *jira.Attachment
	for i := range issue.Fields.Attachments {
		if issue.Fields.Attachments[i].ID == id {
			meta = &issue.Fields.Attachments[i]
		}
	}
	if meta == nil {
		return nil, fmt.Errorf("attachment %s not found on %s", id, issue.Key)
	}
	if meta.Size > maxAttachmentBytes {
		return nil, fmt.Errorf("attachment is %d bytes; the limit is %d", meta.Size, maxAttachmentBytes)
	}
	name := strings.Trim(unsafeFileChars.ReplaceAllString(filepath.Base(meta.Filename), "_"), ". ")
	if name == "" {
		name = "attachment"
	}
	target := filepath.Join(dir, issue.Key, id+"-"+name)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	n, err := s.jc.DownloadAttachment(ctx, id, &buf, maxAttachmentBytes)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o600); err != nil {
		return nil, err
	}
	out := &AttachmentFile{
		AttachmentInfo: AttachmentInfo{ID: id, Filename: name, MimeType: meta.MimeType, Size: n, Author: s.people.User(meta.Author), Created: s.when(meta.Created)},
		Path:           target,
	}
	if n <= maxInlineBytes && inlineable(meta.MimeType) {
		out.Data = buf.Bytes()
	}
	return out, nil
}

func inlineable(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	switch {
	case strings.HasPrefix(mime, "text/"):
		return true
	case mime == "application/json", mime == "application/xml", mime == "application/x-yaml":
		return true
	case mime == "image/png", mime == "image/jpeg", mime == "image/gif", mime == "image/webp":
		return true
	}
	return false
}

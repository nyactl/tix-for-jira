// Package render formats core views as Markdown or JSON. It is the last
// step before output, so it also removes control and invisible characters.
package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/sanitize"
)

// JSON renders any view as indented JSON.
func JSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return sanitize.String(string(b)), nil
}

func done(b *strings.Builder) string {
	return sanitize.String(strings.TrimRight(b.String(), "\n") + "\n")
}

func line(b *strings.Builder, format string, args ...any) {
	fmt.Fprintf(b, format+"\n", args...)
}

func issueLine(i core.IssueSummary) string {
	var meta []string
	if i.Priority != "" {
		meta = append(meta, i.Priority)
	}
	meta = append(meta, i.Type)
	if i.Due != "" {
		meta = append(meta, "due "+i.Due)
	}
	if len(i.Labels) > 0 {
		meta = append(meta, "labels: "+strings.Join(i.Labels, ", "))
	}
	meta = append(meta, "updated "+i.Updated)
	return fmt.Sprintf("- **%s** [%s] %s (%s)", i.Key, i.Status, i.Summary, strings.Join(meta, "; "))
}

// IssueList renders search results.
func IssueList(issues []core.IssueSummary) string {
	var b strings.Builder
	if len(issues) == 0 {
		return "No matching tickets.\n"
	}
	for _, i := range issues {
		line(&b, "%s", issueLine(i))
	}
	return done(&b)
}

// Issue renders one ticket.
func Issue(d *core.IssueDetail) string {
	var b strings.Builder
	line(&b, "# %s: %s", d.Key, d.Summary)
	line(&b, "")
	line(&b, "- Status: %s", d.Status)
	line(&b, "- Type: %s", d.Type)
	if d.Priority != "" {
		line(&b, "- Priority: %s", d.Priority)
	}
	line(&b, "- Assignee: %s", d.Assignee)
	if d.Reporter != "" {
		line(&b, "- Reporter: %s", d.Reporter)
	}
	if d.Resolution != "" {
		line(&b, "- Resolution: %s", d.Resolution)
	}
	if d.Due != "" {
		line(&b, "- Due: %s", d.Due)
	}
	if len(d.Labels) > 0 {
		line(&b, "- Labels: %s", strings.Join(d.Labels, ", "))
	}
	if len(d.Components) > 0 {
		line(&b, "- Components: %s", strings.Join(d.Components, ", "))
	}
	line(&b, "- Created: %s, updated: %s", d.Created, d.Updated)
	if tt := d.TimeTracking; tt != nil {
		line(&b, "- Time: estimate %s, remaining %s, spent %s", or(tt.Original, "-"), or(tt.Remaining, "-"), or(tt.Spent, "-"))
	}
	for _, f := range d.Fields {
		if strings.Contains(f.Value, "\n") {
			line(&b, "- %s:\n\n%s\n", f.Name, indent(f.Value))
		} else {
			line(&b, "- %s: %s", f.Name, f.Value)
		}
	}
	line(&b, "- URL: %s", d.URL)

	if d.Description != "" {
		line(&b, "\n## Description\n\n%s", d.Description)
	}
	if d.Parent != nil || len(d.Subtasks) > 0 || len(d.Links) > 0 {
		line(&b, "\n## Related")
		if d.Parent != nil {
			line(&b, "- %s", related(*d.Parent))
		}
		for _, r := range d.Subtasks {
			line(&b, "- %s", related(r))
		}
		for _, r := range d.Links {
			line(&b, "- %s", related(r))
		}
	}
	if len(d.Attachments) > 0 {
		line(&b, "\n## Attachments")
		for _, a := range d.Attachments {
			line(&b, "- %s: %s (%s, %s) by %s, %s", a.ID, a.Filename, a.MimeType, size(a.Size), a.Author, a.Created)
		}
	}
	return done(&b)
}

func related(r core.Related) string {
	s := fmt.Sprintf("%s %s", r.Relation, r.Key)
	var meta []string
	if r.Type != "" {
		meta = append(meta, r.Type)
	}
	if r.Status != "" {
		meta = append(meta, r.Status)
	}
	if len(meta) > 0 {
		s += " [" + strings.Join(meta, ", ") + "]"
	}
	if r.Summary != "" {
		s += " " + r.Summary
	}
	return s
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Comments renders comments, oldest first.
func Comments(cs []core.CommentView) string {
	if len(cs) == 0 {
		return "No comments.\n"
	}
	var b strings.Builder
	for _, c := range cs {
		edited := ""
		if c.Edited {
			edited = " (edited)"
		}
		line(&b, "### %s, %s%s\n\n%s\n", c.Author, c.Created, edited, c.Body)
	}
	return done(&b)
}

// History renders change history.
func History(hs []core.ChangeView) string {
	if len(hs) == 0 {
		return "No changes.\n"
	}
	var b strings.Builder
	for _, h := range hs {
		line(&b, "- %s, %s:", h.When, h.Author)
		for _, c := range h.Changes {
			line(&b, "  - %s: %s → %s", c.Field, or(c.From, "(empty)"), or(c.To, "(empty)"))
		}
	}
	return done(&b)
}

// Worklogs renders logged work.
func Worklogs(ws []core.WorklogView) string {
	if len(ws) == 0 {
		return "No work logged.\n"
	}
	var b strings.Builder
	for _, w := range ws {
		line(&b, "- %s by %s, started %s", w.TimeSpent, w.Author, w.Started)
		if w.Comment != "" {
			line(&b, "%s", indent(w.Comment))
		}
	}
	return done(&b)
}

// Transitions renders available status changes.
func Transitions(ts []core.TransitionView) string {
	if len(ts) == 0 {
		return "No transitions available.\n"
	}
	var b strings.Builder
	for _, t := range ts {
		line(&b, "- %q → %s", t.Name, t.To)
	}
	return done(&b)
}

// EditableFields renders the fields that can be changed.
func EditableFields(fs []core.EditableField) string {
	if len(fs) == 0 {
		return "No editable fields.\n"
	}
	var b strings.Builder
	for _, f := range fs {
		s := fmt.Sprintf("- %s (%s)", f.Name, f.Type)
		if f.Required {
			s += ", required"
		}
		if len(f.Allowed) > 0 {
			s += ": " + strings.Join(f.Allowed, ", ")
		}
		line(&b, "%s", s)
	}
	return done(&b)
}

// LinkTypes renders link relations.
func LinkTypes(ls []core.LinkTypeView) string {
	var b strings.Builder
	for _, l := range ls {
		if l.Inward == l.Outward {
			line(&b, "- %q (%s)", l.Outward, l.Name)
		} else {
			line(&b, "- %q / %q (%s)", l.Outward, l.Inward, l.Name)
		}
	}
	return done(&b)
}

// Projects renders projects with their issue types, if given.
func Projects(ps []core.ProjectView) string {
	if len(ps) == 0 {
		return "No projects where you can create tickets.\n"
	}
	var b strings.Builder
	for _, p := range ps {
		line(&b, "- %s: %s", p.Key, p.Name)
	}
	return done(&b)
}

// IssueTypes renders creatable issue types.
func IssueTypes(ts []core.IssueTypeView) string {
	var b strings.Builder
	for _, t := range ts {
		if t.Subtask {
			line(&b, "- %s (subtask, needs a parent)", t.Name)
		} else {
			line(&b, "- %s", t.Name)
		}
	}
	return done(&b)
}

// Me renders the current user and settings.
func Me(m *core.Me) string {
	var b strings.Builder
	line(&b, "- Name: %s", m.DisplayName)
	if m.Email != "" {
		line(&b, "- Email: %s", m.Email)
	}
	line(&b, "- Site: %s", m.Site)
	line(&b, "- Privacy: %s", m.Privacy)
	return done(&b)
}

// Plan renders a write plan for confirmation.
func Plan(p *core.Plan) string {
	return sanitize.String(p.Description)
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/approvekit/picker"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/jira"
)

// Chooser lets the user choose a line by typing part of it, and read longer
// text full screen. Terminals implement it; tests script it.
type Chooser interface {
	Choose(opts picker.Options) (picker.Result, error)
	View(title, text string) error
}

func (devTTY) Choose(opts picker.Options) (picker.Result, error) {
	r, err := picker.Run(opts)
	if errors.Is(err, picker.ErrNoTerminal) {
		err = fmt.Errorf("%w (%w)", errNoTerminal, err)
	}
	return r, err
}

func (devTTY) View(title, text string) error { return picker.View(title, text) }

// errNothingChosen ends a command whose argument the user did not choose.
var errNothingChosen = errors.New("nothing chosen; nothing was changed")

// choose shows a picker and returns the chosen index.
func (a *App) choose(opts picker.Options) (int, error) {
	ch, ok := a.TTY.(Chooser)
	if !ok {
		return 0, errNoTerminal
	}
	r, err := ch.Choose(opts)
	if errors.Is(err, picker.ErrCanceled) {
		return 0, errNothingChosen
	}
	return r.Index, err
}

func ticketLine(is core.IssueSummary) string {
	return fmt.Sprintf("%-10s [%s] %s", is.Key, is.Status, is.Summary)
}

// ticket returns the ticket key in args at i, or lets the user choose one
// of their open tickets.
func (a *App) ticket(ctx context.Context, svc *core.Service, args []string, i int) (string, error) {
	if len(args) > i {
		return args[i], nil
	}
	list, err := svc.MyIssues(ctx, core.MyIssuesOptions{Max: 200})
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", errors.New("no open tickets are assigned to you; give a ticket key")
	}
	lines := make([]string, len(list))
	for k, is := range list {
		lines[k] = ticketLine(is)
	}
	k, err := a.choose(picker.Options{Title: "Choose a ticket", Items: lines})
	if errors.Is(err, errNoTerminal) {
		return "", errors.New("give a ticket key, or run this in a terminal to choose one")
	}
	if err != nil {
		return "", err
	}
	return list[k].Key, nil
}

// ticketFirst returns the ticket key leading args and the arguments after
// it. When args do not start with a ticket key, the user chooses one of
// their tickets and all of args follow it.
func (a *App) ticketFirst(ctx context.Context, svc *core.Service, args []string) (string, []string, error) {
	if len(args) > 0 {
		if _, err := jira.NormalizeKey(args[0]); err == nil {
			return args[0], args[1:], nil
		}
	}
	key, err := a.ticket(ctx, svc, nil, 0)
	return key, args, err
}

// otherTicket lets the user choose one of their open tickets other than key.
func (a *App) otherTicket(ctx context.Context, svc *core.Service, key, title string) (string, error) {
	list, err := svc.MyIssues(ctx, core.MyIssuesOptions{Max: 200})
	if err != nil {
		return "", err
	}
	var others []core.IssueSummary
	for _, is := range list {
		if !strings.EqualFold(is.Key, key) {
			others = append(others, is)
		}
	}
	if len(others) == 0 {
		return "", errors.New("you have no other open tickets; give the other ticket key")
	}
	lines := make([]string, len(others))
	for k, is := range others {
		lines[k] = ticketLine(is)
	}
	k, err := a.choose(picker.Options{Title: title, Items: lines})
	if errors.Is(err, errNoTerminal) {
		return "", errors.New("give the other ticket key, or run this in a terminal to choose one")
	}
	if err != nil {
		return "", err
	}
	return others[k].Key, nil
}

// relation lets the user choose a link relation for key.
func (a *App) relation(ctx context.Context, svc *core.Service, key string) (string, error) {
	lts, err := svc.LinkTypes(ctx)
	if err != nil {
		return "", err
	}
	var phrases []string
	for _, lt := range lts {
		for _, p := range []string{lt.Outward, lt.Inward} {
			if p != "" && !slices.Contains(phrases, p) {
				phrases = append(phrases, p)
			}
		}
	}
	if len(phrases) == 0 {
		return "", errors.New("the site has no link types")
	}
	k, err := a.choose(picker.Options{Title: strings.ToUpper(key) + " ...", Items: phrases})
	if errors.Is(err, errNoTerminal) {
		return "", errors.New("give the relation, or run this in a terminal to choose one; see tix link-types")
	}
	if err != nil {
		return "", err
	}
	return phrases[k], nil
}

// status returns the status or transition in args at i, or lets the user
// choose one of the ticket's transitions.
func (a *App) status(ctx context.Context, svc *core.Service, key string, args []string, i int) (string, error) {
	if len(args) > i {
		return args[i], nil
	}
	ts, err := svc.Transitions(ctx, key)
	if err != nil {
		return "", err
	}
	if len(ts) == 0 {
		return "", fmt.Errorf("%s has no status changes available to you", key)
	}
	lines := make([]string, len(ts))
	for k, t := range ts {
		lines[k] = t.To
		if !strings.EqualFold(t.Name, t.To) {
			lines[k] += "  (" + t.Name + ")"
		}
	}
	k, err := a.choose(picker.Options{Title: "Move " + key + " to", Items: lines})
	if errors.Is(err, errNoTerminal) {
		return "", errors.New("give the status, or run this in a terminal to choose one")
	}
	if err != nil {
		return "", err
	}
	return ts[k].Name, nil
}

func relatedLine(r core.Related) string {
	line := fmt.Sprintf("%s %-10s [%s]", r.Relation, r.Key, r.Status)
	if r.Summary != "" {
		line += " " + r.Summary
	}
	return line
}

// link lets the user choose one of the ticket's links and returns the other
// ticket and the relation.
func (a *App) link(ctx context.Context, svc *core.Service, key string) (string, string, error) {
	d, err := svc.Issue(ctx, key)
	if err != nil {
		return "", "", err
	}
	if len(d.Links) == 0 {
		return "", "", fmt.Errorf("%s has no links", d.Key)
	}
	lines := make([]string, len(d.Links))
	for k, l := range d.Links {
		lines[k] = relatedLine(l)
	}
	k, err := a.choose(picker.Options{Title: "Remove a link of " + d.Key, Items: lines})
	if errors.Is(err, errNoTerminal) {
		return "", "", errors.New("give the other ticket key, or run this in a terminal to choose a link")
	}
	if err != nil {
		return "", "", err
	}
	return d.Links[k].Key, d.Links[k].Relation, nil
}

// webLink lets the user choose one of the ticket's web links and returns its
// URL.
func (a *App) webLink(ctx context.Context, svc *core.Service, key string) (string, error) {
	d, err := svc.Issue(ctx, key)
	if err != nil {
		return "", err
	}
	if len(d.WebLinks) == 0 {
		return "", fmt.Errorf("%s has no web links", d.Key)
	}
	lines := make([]string, len(d.WebLinks))
	for k, l := range d.WebLinks {
		lines[k] = l.Title + "  " + l.URL
	}
	k, err := a.choose(picker.Options{Title: "Remove a web link of " + d.Key, Items: lines})
	if errors.Is(err, errNoTerminal) {
		return "", errors.New("give the URL, or run this in a terminal to choose a web link")
	}
	if err != nil {
		return "", err
	}
	return d.WebLinks[k].URL, nil
}

func epicLine(e core.Related) string {
	line := fmt.Sprintf("%-10s [%s]", e.Key, e.Status)
	if e.Summary != "" {
		line += " " + e.Summary
	}
	return line
}

// noEpics explains where other epics come from.
const noEpics = "none of your tickets is in an open epic%s yet; give the epic's key, as shown in Jira"

// epic returns the epic key in args at i, or lets the user choose one of
// the open epics their tickets belong to. Only the picker shows the epics'
// summaries: it draws on the terminal, which commands started by an
// assistant cannot open.
func (a *App) epic(ctx context.Context, svc *core.Service, project string, args []string, i int) (string, error) {
	if len(args) > i {
		return args[i], nil
	}
	es, err := svc.MyEpicsForPicker(ctx, project)
	if err != nil {
		return "", err
	}
	if len(es) == 0 {
		in := ""
		if project != "" {
			in = " of " + strings.ToUpper(project)
		}
		return "", fmt.Errorf(noEpics, in)
	}
	lines := make([]string, len(es))
	for k, e := range es {
		lines[k] = epicLine(e)
	}
	k, err := a.choose(picker.Options{Title: "Choose an epic", Items: lines})
	if errors.Is(err, errNoTerminal) {
		return "", errors.New("give the epic key, or run this in a terminal to choose one")
	}
	if err != nil {
		return "", err
	}
	return es[k].Key, nil
}

// completeEpics completes the epics the user's tickets belong to, without
// summaries: assistants can run completion too.
func (a *App) completeEpics(cmd *cobra.Command, project, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	svc, _, err := a.service(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	es, err := svc.MyEpics(cmd.Context(), project)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	var out []cobra.Completion
	for _, e := range es {
		if strings.HasPrefix(strings.ToUpper(e.Key), strings.ToUpper(toComplete)) {
			out = append(out, cobra.CompletionWithDesc(e.Key, oneLine(strings.TrimSpace(e.Type+", "+e.Status+" "+e.Summary))))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeParent completes a ticket key, then the epics of the user's
// tickets.
func (a *App) completeParent(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return a.completeTickets(cmd, args, toComplete)
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	project, _ := cmd.Flags().GetString("project")
	return a.completeEpics(cmd, project, toComplete)
}

// completeCreateParent completes --parent of tix create.
func (a *App) completeCreateParent(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	project, _ := cmd.Flags().GetString("project")
	return a.completeEpics(cmd, project, toComplete)
}

// completeUnlink completes a ticket key, then the tickets it is linked to,
// then the relations between the two.
func (a *App) completeUnlink(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return a.completeTickets(cmd, args, toComplete)
	}
	if len(args) > 2 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	svc, _, err := a.service(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	d, err := svc.Issue(cmd.Context(), args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	var out []cobra.Completion
	for _, l := range d.Links {
		switch {
		case len(args) == 1 && strings.HasPrefix(strings.ToUpper(l.Key), strings.ToUpper(toComplete)):
			out = append(out, cobra.CompletionWithDesc(l.Key, oneLine(strings.TrimSpace(l.Relation+", "+l.Status+" "+l.Summary))))
		case len(args) == 2 && strings.EqualFold(l.Key, args[1]) && strings.HasPrefix(l.Relation, toComplete):
			out = append(out, cobra.Completion(l.Relation))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeTickets completes ticket keys with the user's open tickets and
// their summaries.
func (a *App) completeTickets(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	svc, _, err := a.service(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	list, err := svc.MyIssues(cmd.Context(), core.MyIssuesOptions{Max: 200})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	var out []cobra.Completion
	for _, is := range list {
		if strings.HasPrefix(strings.ToUpper(is.Key), strings.ToUpper(toComplete)) {
			out = append(out, cobra.CompletionWithDesc(is.Key, oneLine(is.Summary)))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeTransition completes a ticket key, then its transitions.
func (a *App) completeTransition(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return a.completeTickets(cmd, args, toComplete)
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	svc, _, err := a.service(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	ts, err := svc.Transitions(cmd.Context(), args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
	}
	var out []cobra.Completion
	for _, t := range ts {
		if strings.HasPrefix(strings.ToLower(t.To), strings.ToLower(toComplete)) {
			out = append(out, cobra.CompletionWithDesc(t.To, "transition "+oneLine(t.Name)))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// oneLine keeps a completion description on one line and free of control
// characters, which shells would show raw.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f }), " ")
}

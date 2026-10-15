package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/approvekit/picker"

	"github.com/nyactl/tix-for-jira/internal/core"
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

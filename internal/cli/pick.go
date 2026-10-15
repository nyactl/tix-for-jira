package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/approvekit/picker"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/render"
)

func (a *App) pickCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pick",
		Short: "Browse your tickets, typing to filter: show, open or comment on them until you quit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ch, ok := a.TTY.(Chooser)
			if !ok {
				return errNoTerminal
			}
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			project, _ := cmd.Flags().GetString("project")
			text, _ := cmd.Flags().GetString("text")
			list, err := svc.MyIssues(cmd.Context(), core.MyIssuesOptions{IncludeDone: all, Project: project, Text: text, Max: 200})
			if err != nil {
				return err
			}
			if len(list) == 0 {
				_, err := io.WriteString(a.Out, "No matching tickets.\n")
				return err
			}
			lines := make([]string, len(list))
			for i, is := range list {
				lines[i] = fmt.Sprintf("%-10s [%s] %s (%s)", is.Key, is.Status, is.Summary, is.Type)
			}
			opts := picker.Options{Title: "Your tickets on " + svc.Site(), Items: lines, Enter: "show",
				Actions: []picker.Action{{Key: "ctrl-o", Name: "open in browser"}, {Key: "ctrl-t", Name: "comment"}}}
			for {
				r, err := ch.Choose(opts)
				if errors.Is(err, picker.ErrCanceled) {
					return nil
				}
				if err != nil {
					return err
				}
				opts.Cursor, opts.Query, opts.Status = r.Index, r.Query, ""
				key := list[r.Index].Key
				switch r.Action {
				case "":
					d, err := svc.Issue(cmd.Context(), key)
					if err != nil {
						opts.Status = key + ": " + err.Error()
						continue
					}
					if err := ch.View(key+" "+d.Summary, render.Issue(d)); err != nil {
						return err
					}
				case "ctrl-o":
					opts.Status = a.pickOpen(cmd, svc, key)
				case "ctrl-t":
					opts.Status = a.pickComment(cmd, svc, key)
				}
			}
		},
	}
	cmd.Flags().Bool("all", false, "include tickets that are done")
	cmd.Flags().String("project", "", "only this project")
	cmd.Flags().String("text", "", "full-text filter")
	return cmd
}

// pickOpen opens a ticket in the browser and returns the status to show.
func (a *App) pickOpen(cmd *cobra.Command, svc *core.Service, key string) string {
	d, err := svc.Issue(cmd.Context(), key)
	if err != nil {
		return key + ": " + err.Error()
	}
	if a.Browse == nil {
		return d.URL
	}
	if err := a.Browse(cmd.Context(), d.URL); err != nil {
		return key + ": could not open the browser: " + err.Error()
	}
	return key + " opened in the browser."
}

// pickComment asks for a comment, confirms and adds it, and returns the
// status to show.
func (a *App) pickComment(cmd *cobra.Command, svc *core.Service, key string) string {
	body, err := a.TTY.Prompt(fmt.Sprintf("\nComment on %s (Markdown, one line; empty to cancel): ", key))
	if err != nil {
		return key + ": " + err.Error()
	}
	if strings.TrimSpace(body) == "" {
		return "No comment written; nothing was changed."
	}
	p, err := svc.PlanComment(cmd.Context(), key, body)
	msg, err := a.confirmAndApply(cmd, svc, p, err)
	if err != nil {
		return key + ": " + err.Error()
	}
	return msg
}

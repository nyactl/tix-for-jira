package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/render"
)

func (a *App) pickCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pick",
		Short: "Browse your tickets with the arrow keys: show, open or comment on them until you quit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			picker, ok := a.TTY.(Picker)
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
			title := "Your tickets on " + svc.Site()
			cursor, status := 0, ""
			for {
				i, action, err := picker.Pick(title, lines, cursor, status)
				if err != nil {
					return err
				}
				if action == PickQuit {
					return nil
				}
				cursor, status = i, ""
				key := list[i].Key
				switch action {
				case PickShow:
					d, err := svc.Issue(cmd.Context(), key)
					if err != nil {
						status = key + ": " + err.Error()
						continue
					}
					if err := picker.View(key+" "+d.Summary, render.Issue(d)); err != nil {
						return err
					}
				case PickOpen:
					status = a.pickOpen(cmd, svc, key)
				case PickComment:
					status = a.pickComment(cmd, svc, key)
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

package cli

import (
	"errors"
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
		Short: "Choose one of your tickets with the arrow keys, then show, open or comment on it",
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
			i, action, err := picker.Pick(fmt.Sprintf("Your tickets on %s", svc.Site()), lines)
			if err != nil {
				return err
			}
			key := list[i].Key
			switch action {
			case PickShow:
				d, err := svc.Issue(cmd.Context(), key)
				if err != nil {
					return err
				}
				return a.print(cmd, d, func() string { return render.Issue(d) })
			case PickOpen:
				d, err := svc.Issue(cmd.Context(), key)
				if err != nil {
					return err
				}
				if a.Browse == nil {
					_, err := fmt.Fprintln(a.Out, d.URL)
					return err
				}
				return a.Browse(cmd.Context(), d.URL)
			case PickComment:
				body, err := a.TTY.Prompt(fmt.Sprintf("Comment on %s (Markdown, one line; empty to cancel): ", key))
				if err != nil {
					return err
				}
				if strings.TrimSpace(body) == "" {
					return errors.New("no comment written; nothing was changed")
				}
				p, err := svc.PlanComment(cmd.Context(), key, body)
				return a.apply(cmd, svc, p, err)
			}
			return nil
		},
	}
	cmd.Flags().Bool("all", false, "include tickets that are done")
	cmd.Flags().String("project", "", "only this project")
	cmd.Flags().String("text", "", "full-text filter")
	return cmd
}

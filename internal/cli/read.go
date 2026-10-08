package cli

import (
	"github.com/spf13/cobra"

	"github.com/nyactl/tix-jira/internal/core"
	"github.com/nyactl/tix-jira/internal/render"
)

func (a *App) addReadCommands(root *cobra.Command) {
	mine := &cobra.Command{
		Use:   "mine",
		Short: "List tickets assigned to you, most recently updated first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool("all")
			since, _ := cmd.Flags().GetString("since")
			project, _ := cmd.Flags().GetString("project")
			text, _ := cmd.Flags().GetString("text")
			max, _ := cmd.Flags().GetInt("max")
			list, err := svc.MyIssues(cmd.Context(), core.MyIssuesOptions{IncludeDone: all, Since: since, Project: project, Text: text, Max: max})
			if err != nil {
				return err
			}
			return a.print(cmd, list, func() string { return render.IssueList(list) })
		},
	}
	mine.Flags().Bool("all", false, "include tickets that are done")
	mine.Flags().String("since", "", "only tickets updated since: -12h, -7d, -2w or 2026-10-01")
	mine.Flags().String("project", "", "only this project")
	mine.Flags().String("text", "", "full-text filter")
	mine.Flags().Int("max", 50, "maximum number of tickets")

	search := &cobra.Command{
		Use:   "search <jql>",
		Short: "Search with JQL, limited to your privacy scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			max, _ := cmd.Flags().GetInt("max")
			list, err := svc.Search(cmd.Context(), args[0], max)
			if err != nil {
				return err
			}
			return a.print(cmd, list, func() string { return render.IssueList(list) })
		},
	}
	search.Flags().Int("max", 50, "maximum number of tickets")

	show := keyCmd(a, "show <key>", "Show a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		d, err := svc.Issue(cmd.Context(), key)
		if err != nil {
			return err
		}
		return a.print(cmd, d, func() string { return render.Issue(d) })
	})

	comments := keyCmd(a, "comments <key>", "List a ticket's comments", func(cmd *cobra.Command, svc *core.Service, key string) error {
		max, _ := cmd.Flags().GetInt("max")
		cs, err := svc.Comments(cmd.Context(), key, max)
		if err != nil {
			return err
		}
		return a.print(cmd, cs, func() string { return render.Comments(cs) })
	})
	comments.Flags().Int("max", 50, "maximum number of comments")

	history := keyCmd(a, "history <key>", "Show a ticket's change history", func(cmd *cobra.Command, svc *core.Service, key string) error {
		since, _ := cmd.Flags().GetString("since")
		max, _ := cmd.Flags().GetInt("max")
		hs, err := svc.History(cmd.Context(), key, since, max)
		if err != nil {
			return err
		}
		return a.print(cmd, hs, func() string { return render.History(hs) })
	})
	history.Flags().String("since", "", "only changes since: -12h, -7d or 2026-10-01")
	history.Flags().Int("max", 100, "maximum number of entries (newest kept)")

	worklogs := keyCmd(a, "worklogs <key>", "List work logged on a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		ws, err := svc.Worklogs(cmd.Context(), key, 100)
		if err != nil {
			return err
		}
		return a.print(cmd, ws, func() string { return render.Worklogs(ws) })
	})

	transitions := keyCmd(a, "transitions <key>", "List the status changes available for a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		ts, err := svc.Transitions(cmd.Context(), key)
		if err != nil {
			return err
		}
		return a.print(cmd, ts, func() string { return render.Transitions(ts) })
	})

	fields := keyCmd(a, "fields <key>", "List the fields you can change on a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		fs, err := svc.EditableFields(cmd.Context(), key)
		if err != nil {
			return err
		}
		return a.print(cmd, fs, func() string { return render.EditableFields(fs) })
	})

	attachment := &cobra.Command{
		Use:   "attachment <key> <attachment-id>",
		Short: "Download an attachment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			dir, _ := cmd.Flags().GetString("dir")
			f, err := svc.Attachment(cmd.Context(), args[0], args[1], dir)
			if err != nil {
				return err
			}
			return a.print(cmd, f, func() string { return f.Path + "\n" })
		},
	}
	attachment.Flags().String("dir", ".", "directory to save into (a subdirectory per ticket is created)")

	linkTypes := noArgCmd(a, "link-types", "List link relations for `tix-jira link`", func(cmd *cobra.Command, svc *core.Service) error {
		ls, err := svc.LinkTypes(cmd.Context())
		if err != nil {
			return err
		}
		return a.print(cmd, ls, func() string { return render.LinkTypes(ls) })
	})

	projects := noArgCmd(a, "projects", "List projects where you can create tickets", func(cmd *cobra.Command, svc *core.Service) error {
		ps, err := svc.Projects(cmd.Context())
		if err != nil {
			return err
		}
		return a.print(cmd, ps, func() string { return render.Projects(ps) })
	})

	types := keyCmd(a, "types <project>", "List the issue types you can create in a project", func(cmd *cobra.Command, svc *core.Service, project string) error {
		ts, err := svc.IssueTypes(cmd.Context(), project)
		if err != nil {
			return err
		}
		return a.print(cmd, ts, func() string { return render.IssueTypes(ts) })
	})

	whoami := noArgCmd(a, "whoami", "Show your account, site and privacy mode", func(cmd *cobra.Command, svc *core.Service) error {
		me, err := svc.Whoami(cmd.Context())
		if err != nil {
			return err
		}
		return a.print(cmd, me, func() string { return render.Me(me) })
	})

	root.AddCommand(mine, search, show, comments, history, worklogs, transitions, fields, attachment, linkTypes, projects, types, whoami)
}

func keyCmd(a *App, use, short string, run func(*cobra.Command, *core.Service, string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			return run(cmd, svc, args[0])
		},
	}
}

func noArgCmd(a *App, use, short string, run func(*cobra.Command, *core.Service) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			return run(cmd, svc)
		},
	}
}

package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/render"
)

func (a *App) addReadCommands(root *cobra.Command) {
	mine := &cobra.Command{
		Use:   "mine",
		Short: "List tickets assigned to you, most recently updated first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, err := a.service(cmd)
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
		Use:   "search <jql|@name>",
		Short: "Search with JQL or a saved search, limited to your privacy scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cfg, err := a.service(cmd)
			if err != nil {
				return err
			}
			jql := args[0]
			if strings.HasPrefix(jql, "@") {
				if jql, err = cfg.SavedSearch(jql); err != nil {
					return err
				}
			}
			max, _ := cmd.Flags().GetInt("max")
			list, err := svc.Search(cmd.Context(), jql, max)
			if err != nil {
				return err
			}
			return a.print(cmd, list, func() string { return render.IssueList(list) })
		},
	}
	search.Flags().Int("max", 50, "maximum number of tickets")

	show := a.ticketCmd("show [key]", "Show a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		d, err := svc.Issue(cmd.Context(), key)
		if err != nil {
			return err
		}
		return a.print(cmd, d, func() string { return render.Issue(d) })
	})

	comments := a.ticketCmd("comments [key]", "List a ticket's comments", func(cmd *cobra.Command, svc *core.Service, key string) error {
		max, _ := cmd.Flags().GetInt("max")
		cs, err := svc.Comments(cmd.Context(), key, max)
		if err != nil {
			return err
		}
		return a.print(cmd, cs, func() string { return render.Comments(cs) })
	})
	comments.Flags().Int("max", 50, "maximum number of comments")

	history := a.ticketCmd("history [key]", "Show a ticket's change history", func(cmd *cobra.Command, svc *core.Service, key string) error {
		if statusTimes, _ := cmd.Flags().GetBool("status-times"); statusTimes {
			v, err := svc.StatusTimes(cmd.Context(), key)
			if err != nil {
				return err
			}
			return a.print(cmd, v, func() string { return render.StatusTimes(v) })
		}
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
	history.Flags().Bool("status-times", false, "show how long the ticket spent in each status instead")

	worklogs := a.ticketCmd("worklogs [key]", "List work logged on a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		ws, err := svc.Worklogs(cmd.Context(), key, 100)
		if err != nil {
			return err
		}
		return a.print(cmd, ws, func() string { return render.Worklogs(ws) })
	})

	transitions := a.ticketCmd("transitions [key]", "List the status changes available for a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
		ts, err := svc.Transitions(cmd.Context(), key)
		if err != nil {
			return err
		}
		return a.print(cmd, ts, func() string { return render.Transitions(ts) })
	})

	fields := a.ticketCmd("fields [key]", "List the fields you can change on a ticket", func(cmd *cobra.Command, svc *core.Service, key string) error {
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
			svc, _, err := a.service(cmd)
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

	linkTypes := noArgCmd(a, "link-types", "List link relations for `tix link`", func(cmd *cobra.Command, svc *core.Service) error {
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

	epics := &cobra.Command{
		Use:   "epics",
		Short: "List the open epics of a project, to put tickets under with `tix parent`",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cfg, err := a.service(cmd)
			if err != nil {
				return err
			}
			project, _ := cmd.Flags().GetString("project")
			if project == "" {
				project = cfg.DefaultProject
			}
			if project == "" {
				return errors.New("name the project with --project, or set a default with `tix config project KEY`")
			}
			es, err := svc.Epics(cmd.Context(), project, 200)
			if err != nil {
				return err
			}
			return a.print(cmd, es, func() string { return render.Epics(es) })
		},
	}
	epics.Flags().String("project", "", "project key (default: the profile's default project)")

	whoami := noArgCmd(a, "whoami", "Show your account, site and privacy mode", func(cmd *cobra.Command, svc *core.Service) error {
		me, err := svc.Whoami(cmd.Context())
		if err != nil {
			return err
		}
		return a.print(cmd, me, func() string { return render.Me(me) })
	})

	boards := noArgCmd(a, "boards", "List boards, optionally of one project", func(cmd *cobra.Command, svc *core.Service) error {
		project, _ := cmd.Flags().GetString("project")
		bs, err := svc.Boards(cmd.Context(), project)
		if err != nil {
			return err
		}
		return a.print(cmd, bs, func() string { return render.Boards(bs) })
	})
	boards.Flags().String("project", "", "project key")

	sprint := &cobra.Command{Use: "sprint", Short: "Show sprints and your tickets in them"}
	sprintShow := keyCmd(a, "show <board>", "Show your tickets in the active sprint of a board (ID or name)", func(cmd *cobra.Command, svc *core.Service, board string) error {
		max, _ := cmd.Flags().GetInt("max")
		gs, err := svc.ActiveSprints(cmd.Context(), board, max)
		if err != nil {
			return err
		}
		return a.print(cmd, gs, func() string { return render.BoardIssues(gs) })
	})
	sprintShow.Flags().Int("max", 50, "maximum number of tickets per sprint")
	sprintList := keyCmd(a, "list <board>", "List the active and future sprints of a board", func(cmd *cobra.Command, svc *core.Service, board string) error {
		ss, err := svc.Sprints(cmd.Context(), board)
		if err != nil {
			return err
		}
		return a.print(cmd, ss, func() string { return render.Sprints(ss) })
	})
	sprint.AddCommand(sprintShow, sprintList, a.sprintMoveCmd())

	backlog := keyCmd(a, "backlog <board>", "Show your tickets in the backlog of a board (ID or name)", func(cmd *cobra.Command, svc *core.Service, board string) error {
		max, _ := cmd.Flags().GetInt("max")
		g, err := svc.Backlog(cmd.Context(), board, max)
		if err != nil {
			return err
		}
		return a.print(cmd, g, func() string { return render.BoardIssues([]core.BoardIssues{*g}) })
	})
	backlog.Flags().Int("max", 50, "maximum number of tickets")

	open := a.ticketCmd("open [key]", "Open a ticket in the browser", func(cmd *cobra.Command, svc *core.Service, key string) error {
		d, err := svc.Issue(cmd.Context(), key)
		if err != nil {
			return err
		}
		if onlyPrint, _ := cmd.Flags().GetBool("print"); onlyPrint || a.Browse == nil {
			_, err := fmt.Fprintln(a.Out, d.URL)
			return err
		}
		return a.Browse(cmd.Context(), d.URL)
	})
	open.Flags().Bool("print", false, "print the URL instead of opening it")

	root.AddCommand(mine, a.pickCmd(), search, show, open, comments, history, worklogs, transitions, fields, attachment, linkTypes, projects, types, epics, whoami, boards, sprint, backlog)
}

// ticketCmd is a command on one ticket; without a key, the user chooses
// one of their tickets.
func (a *App) ticketCmd(use, short string, run func(*cobra.Command, *core.Service, string) error) *cobra.Command {
	return &cobra.Command{
		Use:               use,
		Short:             short,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.completeTickets,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			key, err := a.ticket(cmd.Context(), svc, args, 0)
			if err != nil {
				return err
			}
			return run(cmd, svc, key)
		},
	}
}

func keyCmd(a *App, use, short string, run func(*cobra.Command, *core.Service, string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
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
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			return run(cmd, svc)
		},
	}
}

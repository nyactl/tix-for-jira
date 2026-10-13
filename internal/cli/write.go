package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/approvekit/sanitize"
	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/render"
)

// apply asks for confirmation on the terminal, applies the plan and prints
// the result.
func (a *App) apply(cmd *cobra.Command, svc *core.Service, plan *core.Plan, err error) error {
	msg, err := a.confirmAndApply(cmd, svc, plan, err)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.Out, msg)
	return err
}

// confirmAndApply asks for confirmation on the terminal and applies the
// plan, returning the sanitized result message.
func (a *App) confirmAndApply(cmd *cobra.Command, svc *core.Service, plan *core.Plan, err error) (string, error) {
	if err != nil {
		return "", err
	}
	prof, err := a.profile(cmd)
	if err != nil {
		return "", err
	}
	desc := target(svc.Site(), prof) + "\n\n" + render.Plan(plan)
	if err := confirm(a.TTY, desc, plan.Key, plan.Kind == core.Destructive); err != nil {
		return "", err
	}
	msg, err := plan.Apply(cmd.Context())
	if err != nil {
		return "", err
	}
	return sanitize.String(msg), nil
}

// textArg reads --<name>; "-" reads stdin.
func textArg(cmd *cobra.Command, name string) (string, error) {
	v, _ := cmd.Flags().GetString(name)
	if v != "-" {
		return v, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	return strings.TrimRight(string(b), "\n"), err
}

func parseSets(sets []string) ([]core.FieldInput, error) {
	out := make([]core.FieldInput, 0, len(sets))
	for _, s := range sets {
		name, value, ok := strings.Cut(s, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid --set %q: use \"Field name=value\"", s)
		}
		out = append(out, core.FieldInput{Field: strings.TrimSpace(name), Value: value})
	}
	return out, nil
}

func (a *App) addWriteCommands(root *cobra.Command) {
	comment := &cobra.Command{
		Use:   "comment <key>",
		Short: "Add a comment (Markdown; mention people as @[label])",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := textArg(cmd, "body")
			if err != nil {
				return err
			}
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanComment(cmd.Context(), args[0], body)
			return a.apply(cmd, svc, p, err)
		},
	}
	comment.Flags().String("body", "", "comment text, or - to read it from stdin")
	_ = comment.MarkFlagRequired("body")

	update := &cobra.Command{
		Use:   "update <key>",
		Short: "Change fields; you confirm by typing the ticket key",
		Example: `  tix update PROJ-1 --set "Summary=New title" --set "Labels=auth,urgent"
  tix update PROJ-1 --set "Description=$(cat notes.md)"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sets, _ := cmd.Flags().GetStringArray("set")
			changes, err := parseSets(sets)
			if err != nil {
				return err
			}
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanUpdate(cmd.Context(), args[0], changes)
			return a.apply(cmd, svc, p, err)
		},
	}
	update.Flags().StringArray("set", nil, `"Field name=value" (repeatable); see "tix fields <key>"`)
	_ = update.MarkFlagRequired("set")

	transition := &cobra.Command{
		Use:   "transition <key> <status or transition>",
		Short: "Change the status; you confirm by typing the ticket key",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := textArg(cmd, "comment")
			if err != nil {
				return err
			}
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanTransition(cmd.Context(), args[0], args[1], c)
			return a.apply(cmd, svc, p, err)
		},
	}
	transition.Flags().String("comment", "", "comment to add, or - for stdin")

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a ticket assigned to you",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := core.CreateInput{}
			in.Project, _ = cmd.Flags().GetString("project")
			in.Type, _ = cmd.Flags().GetString("type")
			in.Summary, _ = cmd.Flags().GetString("summary")
			in.Labels, _ = cmd.Flags().GetStringSlice("label")
			in.Priority, _ = cmd.Flags().GetString("priority")
			in.Parent, _ = cmd.Flags().GetString("parent")
			desc, err := textArg(cmd, "description")
			if err != nil {
				return err
			}
			in.Description = desc
			sets, _ := cmd.Flags().GetStringArray("set")
			extra, err := parseSets(sets)
			if err != nil {
				return err
			}
			if len(extra) > 0 {
				in.Fields = map[string]string{}
				for _, f := range extra {
					in.Fields[f.Field] = f.Value
				}
			}
			svc, cfg, err := a.service(cmd)
			if err != nil {
				return err
			}
			if in.Project == "" {
				in.Project = cfg.DefaultProject
			}
			if in.Project == "" {
				return errors.New("name the project with --project, or set a default with `tix config project KEY`")
			}
			p, err := svc.PlanCreate(cmd.Context(), in)
			return a.apply(cmd, svc, p, err)
		},
	}
	create.Flags().String("project", "", "project key (default: the profile's default project)")
	create.Flags().String("type", "Task", "issue type")
	create.Flags().String("summary", "", "one-line summary")
	create.Flags().String("description", "", "description in Markdown, or - for stdin")
	create.Flags().StringSlice("label", nil, "label (repeatable)")
	create.Flags().String("priority", "", "priority name")
	create.Flags().String("parent", "", "parent ticket key (for subtasks)")
	create.Flags().StringArray("set", nil, `other field "Name=value" (repeatable)`)
	_ = create.MarkFlagRequired("summary")

	logWork := &cobra.Command{
		Use:   "log <key> <duration>",
		Short: "Log time, e.g. tix log PROJ-1 1h30m --started 09:00",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			started, _ := cmd.Flags().GetString("started")
			c, err := textArg(cmd, "comment")
			if err != nil {
				return err
			}
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanWorklog(cmd.Context(), args[0], args[1], started, c)
			return a.apply(cmd, svc, p, err)
		},
	}
	logWork.Flags().String("started", "", "start time: 09:30 (today) or 2026-10-08 09:30; default now")
	logWork.Flags().String("comment", "", "work description, or - for stdin")

	link := &cobra.Command{
		Use:   "link <key> <relation> <other-key>",
		Short: `Link tickets, e.g. tix link PROJ-1 "is blocked by" PROJ-2`,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanLink(cmd.Context(), args[0], args[1], args[2])
			return a.apply(cmd, svc, p, err)
		},
	}

	take := &cobra.Command{
		Use:   "take <key>",
		Short: "Assign an unassigned ticket to yourself",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanTake(cmd.Context(), args[0])
			return a.apply(cmd, svc, p, err)
		},
	}

	attach := &cobra.Command{
		Use:   "attach <key> <file>",
		Short: "Upload a local file to a ticket",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanAttach(cmd.Context(), args[0], args[1])
			return a.apply(cmd, svc, p, err)
		},
	}

	weblink := &cobra.Command{
		Use:   "weblink <key> <url> [title]",
		Short: "Add a web link to a ticket",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			title := ""
			if len(args) == 3 {
				title = args[2]
			}
			p, err := svc.PlanWebLink(cmd.Context(), args[0], args[1], title)
			return a.apply(cmd, svc, p, err)
		},
	}

	watchCmd := func(use, short string, watch bool) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <key>",
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				svc, _, err := a.service(cmd)
				if err != nil {
					return err
				}
				p, err := svc.PlanWatch(cmd.Context(), args[0], watch)
				return a.apply(cmd, svc, p, err)
			},
		}
	}

	root.AddCommand(comment, update, transition, create, logWork, link, weblink, take, attach,
		watchCmd("watch", "Watch a ticket to get its notifications", true),
		watchCmd("unwatch", "Stop watching a ticket", false))
}

func (a *App) sprintMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move <key> <sprint|backlog>",
		Short: `Move a ticket into an active or future sprint (ID or name), or to the backlog`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd)
			if err != nil {
				return err
			}
			p, err := svc.PlanMoveSprint(cmd.Context(), args[0], args[1])
			return a.apply(cmd, svc, p, err)
		},
	}
}

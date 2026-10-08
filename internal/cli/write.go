package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/tix-jira/internal/core"
	"github.com/nyactl/tix-jira/internal/render"
	"github.com/nyactl/tix-jira/internal/sanitize"
)

// apply asks for confirmation on the terminal and applies the plan.
func (a *App) apply(ctx context.Context, plan *core.Plan, err error) error {
	if err != nil {
		return err
	}
	if err := confirm(a.TTY, render.Plan(plan), plan.Key, plan.Kind == core.Destructive); err != nil {
		return err
	}
	msg, err := plan.Apply(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.Out, sanitize.String(msg))
	return err
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
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			p, err := svc.PlanComment(cmd.Context(), args[0], body)
			return a.apply(cmd.Context(), p, err)
		},
	}
	comment.Flags().String("body", "", "comment text, or - to read it from stdin")
	_ = comment.MarkFlagRequired("body")

	update := &cobra.Command{
		Use:   "update <key>",
		Short: "Change fields; you confirm by typing the ticket key",
		Example: `  tix-jira update PROJ-1 --set "Summary=New title" --set "Labels=auth,urgent"
  tix-jira update PROJ-1 --set "Description=- step one\n- step two"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sets, _ := cmd.Flags().GetStringArray("set")
			changes, err := parseSets(sets)
			if err != nil {
				return err
			}
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			p, err := svc.PlanUpdate(cmd.Context(), args[0], changes)
			return a.apply(cmd.Context(), p, err)
		},
	}
	update.Flags().StringArray("set", nil, `"Field name=value" (repeatable); see "tix-jira fields <key>"`)
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
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			p, err := svc.PlanTransition(cmd.Context(), args[0], args[1], c)
			return a.apply(cmd.Context(), p, err)
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
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			p, err := svc.PlanCreate(cmd.Context(), in)
			return a.apply(cmd.Context(), p, err)
		},
	}
	create.Flags().String("project", "", "project key")
	create.Flags().String("type", "Task", "issue type")
	create.Flags().String("summary", "", "one-line summary")
	create.Flags().String("description", "", "description in Markdown, or - for stdin")
	create.Flags().StringSlice("label", nil, "label (repeatable)")
	create.Flags().String("priority", "", "priority name")
	create.Flags().String("parent", "", "parent ticket key (for subtasks)")
	create.Flags().StringArray("set", nil, `other field "Name=value" (repeatable)`)
	_ = create.MarkFlagRequired("project")
	_ = create.MarkFlagRequired("summary")

	logWork := &cobra.Command{
		Use:   "log <key> <duration>",
		Short: "Log time, e.g. tix-jira log PROJ-1 1h30m --started 09:00",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			started, _ := cmd.Flags().GetString("started")
			c, err := textArg(cmd, "comment")
			if err != nil {
				return err
			}
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			p, err := svc.PlanWorklog(cmd.Context(), args[0], args[1], started, c)
			return a.apply(cmd.Context(), p, err)
		},
	}
	logWork.Flags().String("started", "", "start time: 09:30 (today) or 2026-10-08 09:30; default now")
	logWork.Flags().String("comment", "", "work description, or - for stdin")

	link := &cobra.Command{
		Use:   "link <key> <relation> <other-key>",
		Short: `Link tickets, e.g. tix-jira link PROJ-1 "is blocked by" PROJ-2`,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			p, err := svc.PlanLink(cmd.Context(), args[0], args[1], args[2])
			return a.apply(cmd.Context(), p, err)
		},
	}

	root.AddCommand(comment, update, transition, create, logWork, link)
}

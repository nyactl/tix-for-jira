package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/mcpserver"
)

func (a *App) mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server over stdio",
		Long: "Runs tix-for-jira as an MCP server for MCP clients.\n" +
			"Read-only unless --allow-writes is given. Every write is shown to you in an approval dialog first;\n" +
			"clients that cannot show such dialogs cannot write.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			writes, _ := cmd.Flags().GetBool("allow-writes")
			dir, _ := cmd.Flags().GetString("download-dir")
			// Settings and the development-build lock still end the server.
			prof, err := a.profile(cmd)
			if err != nil {
				return err
			}
			cfg, err := config.Load(prof)
			if err != nil {
				return err
			}
			if err := a.checkDevBuild(prof, cfg); err != nil {
				return err
			}
			svc, _, err := a.service(cmd)
			if err != nil {
				// A rejected or missing token, or Jira out of reach: serve
				// anyway, so the client can tell the user why every tool
				// fails instead of showing a closed connection.
				_, _ = fmt.Fprintf(a.Err, "tix-for-jira %s: cannot use Jira (%v); serving tools that report this\n", a.Version, err)
				connect := func(context.Context) (*core.Service, error) {
					svc, _, err := a.service(cmd)
					return svc, err
				}
				return mcpserver.RunDisconnected(cmd.Context(), err, connect, cfg.Site, cfg.Privacy, mcpserver.Options{Version: a.Version, AllowWrites: writes, DownloadDir: dir,
					Target: target(cfg.Site, prof), Searches: cfg.Searches, DefaultProject: cfg.DefaultProject})
			}
			_, _ = fmt.Fprintf(a.Err, "tix-for-jira %s: serving %s (profile %s, privacy %s, writes %v)\n", a.Version, cfg.Site, prof, cfg.Privacy, writes)
			return mcpserver.Run(cmd.Context(), svc, mcpserver.Options{Version: a.Version, AllowWrites: writes, DownloadDir: dir, Target: target(cfg.Site, prof),
				Searches: cfg.Searches, DefaultProject: cfg.DefaultProject})
		},
	}
	cmd.Flags().Bool("allow-writes", false, "offer write tools (each write still needs your approval)")
	cmd.Flags().String("download-dir", defaultDownloadDir(), "where attachments are saved")
	return cmd
}

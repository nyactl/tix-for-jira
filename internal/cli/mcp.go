package cli

import (
	"fmt"

	"github.com/spf13/cobra"

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
			svc, cfg, err := a.service(cmd)
			if err != nil {
				return err
			}
			prof, _ := a.profile(cmd)
			writes, _ := cmd.Flags().GetBool("allow-writes")
			dir, _ := cmd.Flags().GetString("download-dir")
			_, _ = fmt.Fprintf(a.Err, "tix-for-jira %s: serving %s (profile %s, privacy %s, writes %v)\n", a.Version, cfg.Site, prof, cfg.Privacy, writes)
			return mcpserver.Run(cmd.Context(), svc, mcpserver.Options{Version: a.Version, AllowWrites: writes, DownloadDir: dir, Target: target(cfg.Site, prof)})
		},
	}
	cmd.Flags().Bool("allow-writes", false, "offer write tools (each write still needs your approval)")
	cmd.Flags().String("download-dir", defaultDownloadDir(), "where attachments are saved")
	return cmd
}

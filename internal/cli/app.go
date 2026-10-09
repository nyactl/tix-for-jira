// Package cli implements the tix-jira command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"

	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/core"
	"github.com/nyactl/tix-jira/internal/jira"
	"github.com/nyactl/tix-jira/internal/render"
	"github.com/nyactl/tix-jira/internal/sanitize"
	"github.com/nyactl/tix-jira/internal/secret"
)

// App holds the command dependencies, replaceable in tests.
type App struct {
	Version  string
	In       io.Reader
	Out, Err io.Writer
	TTY      Terminal
	Secrets  secret.Store
	Now      func() time.Time
	// Transport overrides the HTTP transport in tests.
	Transport http.RoundTripper
}

// Default returns the production dependencies.
func Default(version string) *App {
	return &App{Version: resolveVersion(version), In: os.Stdin, Out: os.Stdout, Err: os.Stderr, TTY: devTTY{}, Secrets: secret.NewKeychain(), Now: time.Now}
}

func resolveVersion(v string) string {
	if v != "" && v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Execute runs the CLI with the given arguments.
func (a *App) Execute(ctx context.Context, args []string) error {
	root := a.root()
	root.SetArgs(args)
	root.SetOut(a.Out)
	root.SetErr(sanitizeWriter{a.Err})
	_, err := root.ExecuteContextC(ctx)
	return err
}

type sanitizeWriter struct{ w io.Writer }

func (s sanitizeWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, sanitize.String(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (a *App) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "tix-jira",
		Short:         "Work on your own Jira Cloud tickets from the terminal or an MCP client",
		Long:          "tix-jira reads and changes your own Jira Cloud tickets. Every change asks for confirmation in the terminal.\nRun `tix-jira auth login` first.",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       a.Version,
	}
	root.PersistentFlags().Bool("json", false, "print JSON instead of Markdown")
	root.AddCommand(a.authCmd(), a.configCmd(), a.mcpCmd())
	a.addReadCommands(root)
	a.addWriteCommands(root)
	return root
}

func (a *App) print(cmd *cobra.Command, v any, markdown func() string) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		s, err := render.JSON(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(a.Out, s)
		return err
	}
	_, err := io.WriteString(a.Out, markdown())
	return err
}

// service loads settings and the token and connects to Jira.
func (a *App) service(ctx context.Context) (*core.Service, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if w := cfg.ExpiryWarning(a.Now()); w != "" {
		_, _ = fmt.Fprintln(a.Err, "warning: "+w)
	}
	token, err := a.Secrets.Get(secret.AccountKey(cfg.Site, cfg.Email))
	if err != nil {
		return nil, nil, err
	}
	svc, err := core.New(ctx, a.client(cfg.Site, cfg.Email, token), cfg.Privacy)
	if errors.Is(err, jira.ErrUnauthorized) && cfg.TokenExpiry != "" {
		err = fmt.Errorf("%w (recorded expiry: %s)", err, cfg.TokenExpiry)
	}
	return svc, cfg, err
}

func (a *App) client(site, email, token string) *jira.Client {
	var opts []jira.Option
	if a.Transport != nil {
		opts = append(opts, jira.WithTransport(a.Transport))
	}
	return jira.New(site, email, token, a.Version, opts...)
}

func defaultDownloadDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "tix-jira", "attachments")
}

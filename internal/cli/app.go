// Package cli implements the tix-for-jira command line.
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

	"github.com/nyactl/approvekit/atlassian"
	"github.com/nyactl/approvekit/keychain"
	"github.com/nyactl/approvekit/profile"
	"github.com/nyactl/approvekit/sanitize"
	"github.com/nyactl/tix-for-jira/internal/config"
	"github.com/nyactl/tix-for-jira/internal/core"
	"github.com/nyactl/tix-for-jira/internal/jira"
	"github.com/nyactl/tix-for-jira/internal/render"
)

// App holds the command dependencies, replaceable in tests.
type App struct {
	Version  string
	In       io.Reader
	Out, Err io.Writer
	TTY      Terminal
	Secrets  keychain.Store
	Now      func() time.Time
	// Transport overrides the HTTP transport in tests.
	Transport http.RoundTripper
	// Gateway overrides Atlassian's API gateway in tests.
	Gateway string
}

// Default returns the production dependencies.
func Default(version string) *App {
	return &App{Version: resolveVersion(version), In: os.Stdin, Out: os.Stdout, Err: os.Stderr, TTY: devTTY{}, Secrets: keychain.New("tix-for-jira"), Now: time.Now}
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
		Use:           "tix",
		Short:         "Work on your own Jira Cloud tickets from the terminal or an MCP client",
		Long:          "tix-for-jira reads and changes your own Jira Cloud tickets. Every change asks for confirmation in the terminal.\nRun `tix auth login` first.",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       a.Version,
	}
	root.PersistentFlags().Bool("json", false, "print JSON instead of Markdown")
	root.PersistentFlags().String("profile", "", "config profile to use, e.g. test (default: $TIX_JIRA_PROFILE or \"default\")")
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

var errDevNeedsProfile = errors.New("this is a development build: choose a profile explicitly with --profile or TIX_JIRA_PROFILE, so it cannot touch your production site by accident")

// checkDevBuild keeps development builds away from production: they may only
// use a non-default profile that explicitly allows them.
func (a *App) checkDevBuild(prof string, cfg *config.Config) error {
	var account *profile.Account
	if cfg != nil {
		account = &cfg.Account
	}
	if err := profile.CheckDevBuild(a.Version, prof, account); err != nil {
		return fmt.Errorf("%w; opt in with `tix config dev-builds on`", err)
	}
	return nil
}

// profile returns the selected profile name. Development builds must name
// one explicitly.
func (a *App) profile(cmd *cobra.Command) (string, error) {
	p, _ := cmd.Flags().GetString("profile")
	if !profile.IsRelease(a.Version) && !config.Store.Explicit(p) {
		return "", errDevNeedsProfile
	}
	return config.ResolveProfile(p)
}

// target describes where a write goes, so the human sees it when confirming.
func target(site, profile string) string {
	return fmt.Sprintf("Jira site: %s (profile %s)", site, profile)
}

// service loads the profile's settings and token and connects to Jira.
func (a *App) service(cmd *cobra.Command) (*core.Service, *config.Config, error) {
	ctx := cmd.Context()
	prof, err := a.profile(cmd)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(prof)
	if err != nil {
		return nil, nil, err
	}
	if err := a.checkDevBuild(prof, cfg); err != nil {
		return nil, nil, err
	}
	if w := cfg.ExpiryWarning(a.Now()); w != "" {
		_, _ = fmt.Fprintln(a.Err, "warning: "+w)
	}
	token, err := a.Secrets.Get(keychain.AccountKey(cfg.Site, cfg.Email))
	if errors.Is(err, keychain.ErrNotFound) {
		err = fmt.Errorf("%w; run `tix auth login`", err)
	}
	if err != nil {
		return nil, nil, err
	}
	cloudID := ""
	if cfg.ScopedToken {
		cloudID = cfg.CloudID
	}
	svc, err := core.New(ctx, a.client(cfg.Site, cfg.Email, token, cloudID), cfg.Privacy)
	if errors.Is(err, atlassian.ErrUnauthorized) {
		if cfg.TokenExpiry != "" {
			err = fmt.Errorf("%w (recorded expiry: %s)", err, cfg.TokenExpiry)
		}
		err = fmt.Errorf("%w; then run `tix auth login`", err)
	}
	return svc, cfg, err
}

// client returns a Jira client; a non-empty cloudID routes it through the
// API gateway, as scoped tokens require.
func (a *App) client(site, email, token, cloudID string) *jira.Client {
	var opts []atlassian.Option
	if a.Transport != nil {
		opts = append(opts, atlassian.WithTransport(a.Transport))
	}
	if cloudID != "" {
		gw := a.Gateway
		if gw == "" {
			gw = atlassian.Gateway
		}
		opts = append(opts, atlassian.WithGateway(gw, cloudID))
	}
	return jira.New(site, email, token, a.Version, opts...)
}

func defaultDownloadDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "tix-for-jira", "attachments")
}

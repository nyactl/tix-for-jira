package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nyactl/tix-jira/internal/config"
	"github.com/nyactl/tix-jira/internal/secret"
)

func (a *App) authCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Log in, check or remove your Jira credentials"}

	login := &cobra.Command{
		Use:   "login",
		Short: "Store your Jira site, email and API token",
		Long: "Asks for your API token in the terminal (input hidden), verifies it and stores it in the macOS Keychain.\n" +
			"Create a token at https://id.atlassian.com/manage-profile/security/api-tokens. It has the same permissions as your account.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site, _ := cmd.Flags().GetString("site")
			email, _ := cmd.Flags().GetString("email")
			expires, _ := cmd.Flags().GetString("expires")
			prev, _ := config.Load()
			var err error
			if site == "" {
				if site, err = a.TTY.Prompt("Jira site (e.g. your-team.atlassian.net): "); err != nil {
					return err
				}
			}
			if site, err = config.NormalizeSite(site); err != nil {
				return err
			}
			if email == "" {
				if email, err = a.TTY.Prompt("Account email: "); err != nil {
					return err
				}
			}
			token, err := a.TTY.Secret("API token (input hidden): ")
			if err != nil {
				return err
			}
			if token == "" {
				return errors.New("no token entered")
			}
			if !cmd.Flags().Changed("expires") {
				if expires, err = a.TTY.Prompt("Token expiry date shown by Atlassian (YYYY-MM-DD, empty to skip): "); err != nil {
					return err
				}
			}
			cfg := &config.Config{Site: site, Email: strings.TrimSpace(email), Privacy: config.PrivacyOwn, TokenExpiry: strings.TrimSpace(expires)}
			if prev != nil {
				cfg.Privacy = prev.Privacy
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			me, err := a.client(cfg.Site, cfg.Email, token).Myself(cmd.Context())
			if err != nil {
				return fmt.Errorf("checking the credentials: %w", err)
			}
			if err := a.Secrets.Set(secret.AccountKey(cfg.Site, cfg.Email), token); err != nil {
				return err
			}
			if prev != nil && (prev.Site != cfg.Site || prev.Email != cfg.Email) {
				_ = a.Secrets.Delete(secret.AccountKey(prev.Site, prev.Email))
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.Out, "Logged in as %s on %s. Privacy mode: %s.\n", me.DisplayName, cfg.Site, cfg.Privacy)
			return err
		},
	}
	login.Flags().String("site", "", "Jira site, e.g. your-team.atlassian.net")
	login.Flags().String("email", "", "your Atlassian account email")
	login.Flags().String("expires", "", "token expiry date (YYYY-MM-DD), to be warned in time")

	status := &cobra.Command{
		Use:   "status",
		Short: "Check that the stored credentials work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cfg, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			me, err := svc.Whoami(cmd.Context())
			if err != nil {
				return err
			}
			expiry := "not recorded"
			if cfg.TokenExpiry != "" {
				expiry = cfg.TokenExpiry
			}
			_, err = fmt.Fprintf(a.Out, "Logged in as %s on %s.\nPrivacy mode: %s.\nToken expiry: %s.\n", me.DisplayName, me.Site, me.Privacy, expiry)
			return err
		},
	}

	logout := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored API token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := confirm(a.TTY, fmt.Sprintf("Remove the API token for %s on %s from the Keychain?", cfg.Email, cfg.Site), "", false); err != nil {
				return err
			}
			if err := a.Secrets.Delete(secret.AccountKey(cfg.Site, cfg.Email)); err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.Out, "Token removed. Revoke it at https://id.atlassian.com/manage-profile/security/api-tokens if you no longer need it.")
			return err
		},
	}
	cmd.AddCommand(login, status, logout)
	return cmd
}

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or change settings"}
	show := &cobra.Command{
		Use:   "show",
		Short: "Show the settings (the token is never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			path, _ := config.Path()
			return a.print(cmd, cfg, func() string {
				return fmt.Sprintf("- Site: %s\n- Email: %s\n- Privacy: %s\n- Token expiry: %s\n- File: %s\n", cfg.Site, cfg.Email, cfg.Privacy, orDash(cfg.TokenExpiry), path)
			})
		},
	}
	privacy := &cobra.Command{
		Use:   "privacy own|off",
		Short: "Limit what assistants see (own) or show everything Jira shows you (off)",
		Long: "own: only tickets assigned to you are visible, other people appear as \"Person A\", \"Person B\", ...\n" +
			"off: everything your Jira account can see.\n" +
			"Switching to off must be confirmed in the terminal. Restart MCP clients afterwards.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"own", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			mode := config.PrivacyMode(strings.ToLower(args[0]))
			if mode != config.PrivacyOwn && mode != config.PrivacyOff {
				return fmt.Errorf("unknown mode %q; use own or off", args[0])
			}
			if mode == config.PrivacyOff && cfg.Privacy != config.PrivacyOff {
				answer, err := a.TTY.Prompt("Privacy off lets assistants see every ticket you can see, with real names.\nType off to confirm: ")
				if err != nil {
					return err
				}
				if answer != "off" {
					return errors.New("not confirmed; privacy mode unchanged")
				}
			}
			cfg.Privacy = mode
			if err := cfg.Save(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.Out, "Privacy mode: %s. Restart MCP clients to apply it.\n", mode)
			return err
		},
	}
	cmd.AddCommand(show, privacy)
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

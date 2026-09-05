package command

import (
	"context"
	"errors"
	"fmt"
	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/auth"
	"github.com/lettermint/lettermint-cli/internal/config"
	"github.com/spf13/cobra"
	"net/url"
	"time"
)

func (a *app) authCommands(root *cobra.Command) {
	parent := &cobra.Command{Use: "auth", Short: "Manage OAuth login"}
	root.AddCommand(parent)
	var name, base, clientID string
	var noBrowser bool
	login := &cobra.Command{Use: "login", Short: "Approve access to one team in your browser", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if a.noInput {
			return errors.New("auth login requires browser approval; use an existing profile for scripts")
		}
		if err := config.ValidateName(name); err != nil {
			return err
		}
		s, err := a.storage()
		if err != nil {
			return err
		}
		unlock, err := s.Lock(c.Context(), "profile-"+name)
		if err != nil {
			return err
		}
		defer unlock()
		cfg, err := s.Read()
		if err != nil {
			return err
		}
		if _, exists := cfg.Profiles[name]; exists {
			return errors.New("profile already exists; log out before replacing its grant")
		}
		// Test the credential store before asking the user to approve a grant.
		probe := "@probe/" + name
		if err = s.Vault.Set(probe, config.Credentials{}); err != nil {
			return fmt.Errorf("credential store is unavailable: %w", err)
		}
		if err = s.Vault.Delete(probe); err != nil {
			return err
		}
		if err = a.ui(c).Banner(a.version, true); err != nil {
			return err
		}
		credentials, err := auth.Login(c.Context(), base, clientID, a.ui(c).DiagnosticWriter(), noBrowser)
		if err != nil {
			return err
		}
		client := &api.Client{BaseURL: base, HTTP: api.Transport(), Token: func(context.Context) (string, error) { return credentials.AccessToken, nil }}
		saved := false
		defer func() {
			if !saved {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = client.Do(cleanup, "DELETE", "/v1/connection", nil, nil, nil)
				_ = s.Vault.Delete(name)
			}
		}()
		identity, err := client.Identity(c.Context())
		if err != nil {
			return err
		}
		if identity.Data.User.ID == "" || identity.Data.Team.ID == "" || identity.Data.ConnectionID == "" {
			return errors.New("API returned an incomplete identity")
		}
		credentials.ConnectionID = identity.Data.ConnectionID
		if err = s.Vault.Set(name, credentials); err != nil {
			_, _ = client.Do(c.Context(), "DELETE", "/v1/connection", nil, nil, nil)
			return fmt.Errorf("could not save credentials: %w", err)
		}
		err = s.Update(c.Context(), func(cfg *config.Config) error {
			cfg.Profiles[name] = config.Profile{ConnectionID: identity.Data.ConnectionID, APIURL: base, ClientID: clientID, UserID: identity.Data.User.ID, TeamID: identity.Data.Team.ID}
			cfg.Selected = name
			return nil
		})
		if err != nil {
			return err
		}
		saved = true
		return a.output(c, map[string]any{"profile": name, "identity": identity.Data})
	}}
	login.Flags().StringVar(&name, "name", "default", "New profile name")
	login.Flags().StringVar(&base, "api-url", "https://api.lettermint.co", "API origin")
	login.Flags().StringVar(&clientID, "client-id", a.clientID, "Official public client ID for this environment")
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the login URL without opening a browser")
	parent.AddCommand(login)
	parent.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		client, _, name, err := a.client(c)
		if err != nil {
			return err
		}
		identity, err := client.Identity(c.Context())
		if err != nil {
			return err
		}
		return a.output(c, map[string]any{"profile": name, "identity": identity.Data})
	}})
	var local bool
	logout := &cobra.Command{Use: "logout", Short: "Revoke this grant and its listeners", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		client, profile, name, err := a.client(c)
		if err != nil {
			return err
		}
		if local {
			if err = a.confirm(c, "Remove the saved login for "+name+" without revoking server access?"); err != nil {
				return err
			}
		} else if _, err = client.Token(c.Context()); err != nil {
			return logoutError(err, name)
		}
		s, _ := a.storage()
		unlock, err := s.Lock(c.Context(), "profile-"+name)
		if err != nil {
			return err
		}
		defer unlock()
		_, saved, err := s.Resolve(name)
		if err != nil {
			return err
		}
		if saved.ConnectionID != profile.ConnectionID {
			return errors.New("the profile grant changed")
		}
		current, err := s.Vault.Get(name)
		if err != nil && !(local && errors.Is(err, config.ErrCredentialsNotFound)) {
			return err
		}
		if err == nil && current.ConnectionID != profile.ConnectionID {
			return errors.New("the profile grant changed")
		}
		if !local {
			client.Token = func(context.Context) (string, error) { return current.AccessToken, nil }
			if _, err = client.Do(c.Context(), "DELETE", "/v1/connection", nil, nil, nil); err != nil {
				return logoutError(err, name)
			}
		}
		if err = s.Vault.Delete(name); err != nil && !errors.Is(err, config.ErrCredentialsNotFound) {
			return err
		}
		err = s.Update(c.Context(), func(cfg *config.Config) error {
			if cfg.Profiles[name].ConnectionID != profile.ConnectionID {
				return errors.New("the profile grant changed")
			}
			delete(cfg.Profiles, name)
			if cfg.Selected == name {
				cfg.Selected = ""
			}
			return nil
		})
		if err != nil {
			return err
		}
		return a.output(c, map[string]bool{"revoked": !local, "local_credentials_removed": true})
	}}
	logout.Flags().BoolVar(&local, "local", false, "Remove only the saved login; server access may remain active")
	parent.AddCommand(logout)
}

func logoutError(err error, profile string) error {
	var remote *api.Error
	if (errors.As(err, &remote) && remote.Status == 401) || errors.Is(err, config.ErrCredentialsNotFound) {
		return fmt.Errorf("%w; to remove this saved login, run auth logout --profile %s --local", err, profile)
	}
	return err
}
func (a *app) profileCommands(root *cobra.Command) {
	profiles := &cobra.Command{Use: "profiles", Short: "Select saved user and team grants"}
	root.AddCommand(profiles)
	profiles.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		s, err := a.storage()
		if err != nil {
			return err
		}
		cfg, err := s.Read()
		if err != nil {
			return err
		}
		return a.output(c, cfg)
	}})
	profiles.AddCommand(&cobra.Command{Use: "use <name>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		s, err := a.storage()
		if err != nil {
			return err
		}
		if err = s.Update(c.Context(), func(cfg *config.Config) error {
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return errors.New("profile does not exist")
			}
			cfg.Selected = args[0]
			return nil
		}); err != nil {
			return err
		}
		return a.output(c, map[string]string{"profile": args[0]})
	}})
	ctx := &cobra.Command{Use: "context", Short: "Manage project and route defaults"}
	root.AddCommand(ctx)
	ctx.AddCommand(&cobra.Command{Use: "show", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		_, p, name, err := a.client(c)
		if err != nil {
			return err
		}
		return a.output(c, map[string]any{"profile": name, "context": p})
	}})
	ctx.AddCommand(&cobra.Command{Use: "set", Short: "Save --project and --route after access checks", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		client, p, name, err := a.client(c)
		if err != nil {
			return err
		}
		if p.Project == "" && p.Route != "" {
			return errors.New("a route requires a project")
		}
		if p.Project != "" {
			if _, err = client.Do(c.Context(), "GET", "/v1/projects/"+url.PathEscape(p.Project), nil, nil, nil); err != nil {
				return err
			}
		}
		if p.Route != "" {
			if _, err = client.Do(c.Context(), "GET", "/v1/routes/"+url.PathEscape(p.Route), url.Values{"filter[project]": {p.Project}}, nil, nil); err != nil {
				return err
			}
		}
		s, _ := a.storage()
		err = s.Update(c.Context(), func(cfg *config.Config) error {
			saved, ok := cfg.Profiles[name]
			if !ok || saved.ConnectionID != p.ConnectionID {
				return errors.New("profile was removed or its grant changed")
			}
			saved.Project = p.Project
			saved.Route = p.Route
			cfg.Profiles[name] = saved
			return nil
		})
		if err != nil {
			return err
		}
		return a.output(c, p)
	}})
}

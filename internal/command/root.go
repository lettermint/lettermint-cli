package command

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/auth"
	"github.com/lettermint/lettermint-cli/internal/config"
	"github.com/lettermint/lettermint-cli/internal/listener"
	"github.com/lettermint/lettermint-cli/internal/presentation"
	"github.com/lettermint/lettermint-cli/skills"
	"github.com/spf13/cobra"
	"io"
	"net/url"
	"os"
	"strings"
)

type app struct {
	profile, project, route string
	noInput, yes            bool
	version, clientID       string
	store                   *config.Store
	display                 presentation.Options
	presenter               *presentation.Presenter
	scope                   presentation.Context
}

func New(version, clientID string) *cobra.Command {
	return newWithStore(version, clientID, nil)
}

func newWithStore(version, clientID string, store *config.Store) *cobra.Command {
	a := &app{version: version, clientID: clientID, store: store}
	root := &cobra.Command{Use: "lettermint", Short: "Send email and develop with Lettermint", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&a.profile, "profile", "", "Saved profile")
	root.PersistentFlags().StringVar(&a.project, "project", "", "Project ID; overrides the profile default")
	root.PersistentFlags().StringVar(&a.route, "route", "", "Route ID; overrides the profile default")
	root.PersistentFlags().BoolVar(&a.display.JSON, "json", false, "Write JSON; listeners write newline-delimited JSON (automatic in pipes)")
	root.PersistentFlags().BoolVar(&a.display.Plain, "plain", false, "Write readable text without color, banners, or animation")
	root.PersistentFlags().StringVar(&a.display.Color, "color", "auto", "Human output color: auto, always, or never")
	root.PersistentFlags().BoolVar(&a.noInput, "no-input", false, "Do not prompt")
	root.PersistentFlags().BoolVar(&a.yes, "yes", false, "Confirm a destructive operation")
	root.AddCommand(&cobra.Command{Use: "version", Short: "Show the CLI version", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return a.output(c, map[string]string{"version": version}) }})
	a.authCommands(root)
	a.profileCommands(root)
	a.resourceCommands(root)
	a.utilityCommands(root)
	a.configurePresentation(root)
	return root
}
func (a *app) storage() (*config.Store, error) {
	if a.store != nil {
		return a.store, nil
	}
	s, err := config.New()
	if err == nil {
		a.store = s
	}
	return s, err
}
func (a *app) client(c *cobra.Command) (*api.Client, config.Profile, string, error) {
	s, err := a.storage()
	if err != nil {
		return nil, config.Profile{}, "", err
	}
	name, p, err := s.Resolve(a.profile)
	if err != nil {
		return nil, p, name, err
	}
	// Take one profile and context snapshot for this command.
	if c.Flags().Changed("project") || c.InheritedFlags().Changed("project") {
		if a.project != p.Project {
			p.Route = ""
		}
		p.Project = a.project
	}
	if c.Flags().Changed("route") || c.InheritedFlags().Changed("route") {
		p.Route = a.route
	}
	h := api.Transport()
	a.scope = presentation.Context{Profile: name, Project: p.Project, Route: p.Route}
	return &api.Client{BaseURL: p.APIURL, HTTP: h, Token: auth.TokenSource(s, name, p, h)}, p, name, nil
}
func (a *app) output(c *cobra.Command, value any) error {
	return a.ui(c).Result(strings.TrimPrefix(c.CommandPath(), "lettermint "), value, a.scope)
}
func (a *app) confirm(c *cobra.Command, description string) error {
	a.ui(c).StopProgress()
	if a.yes {
		return nil
	}
	if a.noInput {
		return errors.New("this operation requires --yes with --no-input")
	}
	if err := a.ui(c).Prompt(description); err != nil {
		return err
	}
	answer, err := bufio.NewReader(c.InOrStdin()).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return errors.New("operation canceled")
	}
	return nil
}
func input(c *cobra.Command, file string) (map[string]any, error) {
	if file == "" {
		return map[string]any{}, nil
	}
	var reader io.Reader = c.InOrStdin()
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	data, err := io.ReadAll(io.LimitReader(reader, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, errors.New("JSON input exceeds 32 MiB")
	}
	var object map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err = dec.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("JSON input must be an object")
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("input must contain one JSON object")
	}
	return object, nil
}
func (a *app) resourceCommands(root *cobra.Command) {
	for _, group := range []string{"projects", "routes", "domains", "messages", "webhooks", "listeners"} {
		parent := &cobra.Command{Use: group, Short: "Manage " + group}
		root.AddCommand(parent)
		actions := []string{"list", "get"}
		switch group {
		case "projects":
			actions = append(actions, "create")
		case "routes":
			actions = append(actions, "create", "update", "verify-inbound-domain")
		case "domains":
			actions = append(actions, "create", "verify", "assign")
		case "messages":
			actions = append(actions, "events")
		case "webhooks":
			actions = append(actions, "create", "update", "delete", "test")
		case "listeners":
			actions = append(actions, "stop", "replay", "secret")
		}
		for _, action := range actions {
			a.resource(parent, group, action)
		}
		if group == "messages" {
			a.sendCommand(parent)
			a.contentCommand(parent)
		}
		if group == "webhooks" {
			a.listenCommand(parent)
		}
	}
}
func (a *app) resource(parent *cobra.Command, group, action string) {
	var file string
	var limit int
	var cursor string
	var sequence int64
	needsID := action != "list" && action != "create"
	use := action
	if needsID {
		use += " <id>"
	}
	cmd := &cobra.Command{Use: use, Short: action + " " + group, Args: func(c *cobra.Command, args []string) error {
		if needsID {
			return cobra.ExactArgs(1)(c, args)
		}
		return cobra.NoArgs(c, args)
	}, RunE: func(c *cobra.Command, args []string) error {
		client, p, _, err := a.client(c)
		if err != nil {
			return err
		}
		path := "/v1/" + group
		query := url.Values{}
		if p.Project != "" {
			query.Set("filter[project]", p.Project)
		}
		if p.Route != "" && (group == "messages" || group == "webhooks") {
			query.Set("filter[route_id]", p.Route)
		}
		if group == "routes" && (action == "list" || action == "create") {
			if p.Project == "" {
				return errors.New("select a project with --project or context set")
			}
			path = "/v1/projects/" + url.PathEscape(p.Project) + "/routes"
			query = url.Values{}
		}
		if group == "messages" && p.Project == "" {
			return errors.New("select a project with --project or context set")
		}
		if group == "projects" || group == "listeners" {
			query = url.Values{}
		}
		if needsID {
			path += "/" + url.PathEscape(args[0])
		}
		method := "GET"
		var body any
		switch action {
		case "list":
			b, err := client.List(c.Context(), path, query, limit, cursor)
			if err != nil {
				return err
			}
			return a.output(c, b)
		case "create", "update", "assign":
			m, err := input(c, file)
			if err != nil {
				return err
			}
			if group == "projects" && action == "create" {
				if _, ok := m["initial_routes"]; !ok {
					m["initial_routes"] = "transactional"
				}
				if _, ok := m["smtp_enabled"]; !ok {
					m["smtp_enabled"] = false
				}
			}
			body = m
			method = "POST"
			if action == "update" {
				method = "PUT"
			}
			if action == "assign" {
				if err = a.confirm(c, "Replace the projects assigned to this domain?"); err != nil {
					return err
				}
				path += "/projects"
				method = "PUT"
			}
		case "delete", "stop":
			if err = a.confirm(c, "Stop or delete "+group+" "+args[0]+"?"); err != nil {
				return err
			}
			method = "DELETE"
		case "verify", "verify-inbound-domain", "test", "replay":
			method = "POST"
			if group == "domains" && action == "verify" {
				path += "/dns-records/verify"
			} else {
				path += "/" + action
			}
			if action == "replay" {
				if sequence < 1 {
					return errors.New("--sequence must be positive")
				}
				body = map[string]any{"sequence": sequence}
			}
		case "events", "secret":
			path += "/" + action
			if action == "events" {
				b, err := client.List(c.Context(), path, query, limit, cursor)
				if err != nil {
					return err
				}
				return a.output(c, b)
			}
		}
		b, err := client.Do(c.Context(), method, path, query, body, nil)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}}
	if action == "list" || action == "events" {
		cmd.Flags().IntVar(&limit, "limit", 30, "Results per page (1 to 100)")
		cmd.Flags().StringVar(&cursor, "cursor", "", "Cursor from the previous response")
	}
	if action == "create" || action == "update" || action == "assign" {
		cmd.Flags().StringVar(&file, "file", "", "JSON input file; use - for standard input")
		_ = cmd.MarkFlagRequired("file")
	}
	if action == "replay" {
		cmd.Flags().Int64Var(&sequence, "sequence", 0, "Original event sequence")
		_ = cmd.MarkFlagRequired("sequence")
	}
	parent.AddCommand(cmd)
}
func (a *app) sendCommand(parent *cobra.Command) {
	var file, key, from, subject, html, text, headers, metadata, attachments, tags, settings string
	var to, cc, bcc, reply []string
	cmd := &cobra.Command{Use: "send", Short: "Send one message; an accepted response does not confirm delivery", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		fields := []string{"from", "to", "cc", "bcc", "reply-to", "subject", "html", "text", "headers", "metadata", "attachments", "tags", "settings"}
		if file != "" {
			for _, field := range fields {
				if c.Flags().Changed(field) {
					return errors.New("--file cannot be combined with message fields")
				}
			}
		}
		body, err := input(c, file)
		if err != nil {
			return err
		}
		if file == "" {
			body = map[string]any{"from": from, "to": to, "subject": subject}
			for k, v := range map[string]string{"html": html, "text": text} {
				if c.Flags().Changed(k) {
					body[k] = v
				}
			}
			if len(cc) > 0 {
				body["cc"] = cc
			}
			if len(bcc) > 0 {
				body["bcc"] = bcc
			}
			if len(reply) > 0 {
				body["reply_to"] = reply
			}
			for k, v := range map[string]string{"headers": headers, "metadata": metadata, "attachments": attachments, "tags": tags, "settings": settings} {
				if v != "" {
					var parsed any
					if err = json.Unmarshal([]byte(v), &parsed); err != nil {
						return fmt.Errorf("--%s: %w", k, err)
					}
					body[k] = parsed
				}
			}
		}
		if _, ok := body["scheduled_at"]; ok {
			return errors.New("scheduling is not supported")
		}
		client, p, _, err := a.client(c)
		if err != nil {
			return err
		}
		if p.Project == "" {
			return errors.New("select a project with --project or context set")
		}
		if p.Route != "" {
			if _, ok := body["route"]; ok {
				return errors.New("route is set in both context and message input")
			}
			body["route_id"] = p.Route
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		var message api.SendInput
		decoder := json.NewDecoder(strings.NewReader(string(encoded)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&message); err != nil {
			return err
		}
		b, err := client.Send(c.Context(), p.Project, key, message)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}}
	f := cmd.Flags()
	f.StringVar(&file, "file", "", "JSON message file; use - for standard input")
	f.StringVar(&key, "idempotency-key", "", "Stable key; reuse it with the same input after an uncertain response")
	_ = cmd.MarkFlagRequired("idempotency-key")
	for _, v := range []struct {
		target     *string
		name, help string
	}{{&from, "from", "Sender address"}, {&subject, "subject", "Subject"}, {&html, "html", "HTML body"}, {&text, "text", "Text body"}, {&headers, "headers", "Headers as a JSON object"}, {&metadata, "metadata", "Metadata as a JSON object"}, {&attachments, "attachments", "Attachments as a JSON array with base64 content"}, {&tags, "tags", "Tags as a JSON array"}, {&settings, "settings", "Message settings as a JSON object"}} {
		f.StringVar(v.target, v.name, "", v.help)
	}
	f.StringArrayVar(&to, "to", nil, "Recipient; repeat for more than one")
	f.StringArrayVar(&cc, "cc", nil, "CC recipient")
	f.StringArrayVar(&bcc, "bcc", nil, "BCC recipient")
	f.StringArrayVar(&reply, "reply-to", nil, "Reply-to address")
	parent.AddCommand(cmd)
}
func (a *app) contentCommand(parent *cobra.Command) {
	var format, output string
	cmd := &cobra.Command{Use: "content <message-id>", Short: "Export exact message source, HTML, or text", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if format != "raw" && format != "html" && format != "text" {
			return errors.New("--format must be raw, html, or text")
		}
		client, p, _, err := a.client(c)
		if err != nil {
			return err
		}
		if p.Project == "" {
			return errors.New("select a project")
		}
		endpoint := format
		query := url.Values{"filter[project]": {p.Project}}
		if format == "raw" {
			endpoint = "source"
			query.Set("format", "stored")
		}
		resp, err := client.Open(c.Context(), "GET", "/v1/messages/"+url.PathEscape(args[0])+"/"+endpoint, query, nil, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if output != "" && output != "-" {
			return writeContentFile(c.Context(), output, resp.Body)
		}
		_, err = io.Copy(c.OutOrStdout(), resp.Body)
		return err
	}}
	cmd.Flags().StringVar(&format, "format", "raw", "raw, html, or text")
	cmd.Flags().StringVar(&output, "output", "-", "Output file; default is standard output")
	parent.AddCommand(cmd)
}
func (a *app) listenCommand(parent *cobra.Command) {
	var target string
	var events []string
	var machine bool
	cmd := &cobra.Command{Use: "listen", Short: "Forward message and suppression webhooks to a local application", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		local, err := listener.LocalClient(target)
		if err != nil {
			return err
		}
		local.CloseIdleConnections()
		client, p, _, err := a.client(c)
		if err != nil {
			return err
		}
		if p.Project == "" {
			return errors.New("select a project")
		}
		session, err := client.CreateListener(c.Context(), p.Project, p.Route, events, machine)
		if err != nil {
			return err
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*1e9)
			defer cancel()
			_, _ = client.Do(ctx, "DELETE", "/v1/listeners/"+url.PathEscape(session.ID), nil, nil, nil)
		}()
		if err = a.ui(c).ListenerStarted(session.ID, target, events, machine, a.scope); err != nil {
			return err
		}
		return listener.RunWithObserver(c.Context(), client, session, target, listener.Observer{
			Attempt: func(event api.Event, attempt api.Attempt) error {
				return a.ui(c).ListenerAttempt(event, attempt)
			},
			Connection: a.ui(c).ListenerConnection,
		})
	}}
	cmd.Flags().StringVar(&target, "forward-to", "", "Loopback HTTP or HTTPS destination")
	_ = cmd.MarkFlagRequired("forward-to")
	cmd.Flags().StringSliceVar(&events, "events", nil, "Event types; default is all message and suppression events")
	cmd.Flags().BoolVar(&machine, "include-machine-events", false, "Include machine tracking events")
	parent.AddCommand(cmd)
}
func (a *app) utilityCommands(root *cobra.Command) {
	skill := &cobra.Command{Use: "skills", Short: "Use the agent skills included in this release"}
	skill.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return a.output(c, map[string]any{"version": a.version, "skills": []string{"lettermint-cli"}})
	}})
	var output string
	export := &cobra.Command{Use: "export", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := skills.Export(output); err != nil {
			return err
		}
		return a.output(c, map[string]string{"output": output, "version": a.version})
	}}
	export.Flags().StringVar(&output, "output", "", "New directory for the skill package")
	_ = export.MarkFlagRequired("output")
	skill.AddCommand(export)
	root.AddCommand(skill)
	root.AddCommand(&cobra.Command{Use: "doctor", Short: "Check the saved login and API access", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		client, _, _, err := a.client(c)
		if err != nil {
			return err
		}
		identity, err := client.Identity(c.Context())
		if err != nil {
			return err
		}
		return a.output(c, map[string]any{"version": a.version, "api": "available", "identity": identity.Data})
	}})
	completion := &cobra.Command{Use: "completion <bash|zsh|fish|powershell>", Short: "Write shell completion", Args: cobra.ExactArgs(1), ValidArgs: []string{"bash", "zsh", "fish", "powershell"}, RunE: func(c *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletion(c.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(c.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(c.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletionWithDesc(c.OutOrStdout())
		default:
			return errors.New("unsupported shell")
		}
	}}
	root.AddCommand(completion)
}

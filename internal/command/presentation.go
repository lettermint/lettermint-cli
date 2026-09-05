package command

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/presentation"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Execute renders failures from both Cobra parsing and command execution.
func Execute(ctx context.Context, args []string, version, clientID string, in io.Reader, out, diagnostics io.Writer) int {
	root := New(version, clientID)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(diagnostics)
	root.SetArgs(args)
	if err := errorOptions(root, args).Validate(); err != nil {
		_ = presentation.New(out, diagnostics, errorOptions(root, args)).Error(err)
		return api.ExitCode(err)
	}
	if err := root.ExecuteContext(ctx); err != nil {
		ui := presentation.New(out, diagnostics, errorOptions(root, args))
		_ = ui.Error(err)
		return api.ExitCode(err)
	}
	return 0
}

// Cobra can stop parsing before a display flag. Read only known display flags
// for error rendering, while skipping values of the actual command flags.
func errorOptions(root *cobra.Command, args []string) presentation.Options {
	options := presentation.Options{Color: "auto"}
	flags := map[string]*pflag.Flag{}
	var collect func(*cobra.Command)
	collect = func(c *cobra.Command) {
		c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = f })
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = f })
		for _, child := range c.Commands() {
			collect(child)
		}
	}
	collect(root)
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		name, value, equals := strings.Cut(strings.TrimPrefix(args[i], "--"), "=")
		flag := flags[name]
		if flag == nil {
			continue
		}
		if !equals {
			value = flag.NoOptDefVal
			if value == "" && i+1 < len(args) {
				i++
				value = args[i]
			}
		}
		switch name {
		case "json":
			if v, err := strconv.ParseBool(value); err == nil {
				options.JSON = v
			}
		case "plain":
			if v, err := strconv.ParseBool(value); err == nil {
				options.Plain = v
			}
		case "color":
			options.Color = value
		}
	}
	return options
}

func (a *app) ui(c *cobra.Command) *presentation.Presenter {
	if a.presenter == nil {
		a.presenter = presentation.New(c.OutOrStdout(), c.ErrOrStderr(), a.display)
	}
	return a.presenter
}

func (a *app) configurePresentation(root *cobra.Command) {
	root.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
	root.Args = cobra.NoArgs
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error { return a.display.Validate() }
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		ui := a.ui(c)
		if err := a.display.Validate(); err != nil {
			_ = ui.Error(err)
			return
		}
		if c == root {
			_ = ui.Banner(a.version, false)
		}
		_ = ui.Help(c.CommandPath(), c.Short, c.Example, c.UsageString(), c == root)
	})
	groups := map[string]string{
		"auth": "setup", "profiles": "setup", "context": "setup",
		"messages": "email", "projects": "email", "routes": "email", "domains": "email",
		"webhooks": "webhooks", "listeners": "webhooks",
		"skills": "utilities", "doctor": "utilities", "completion": "utilities", "version": "utilities",
	}
	root.AddGroup(
		&cobra.Group{ID: "setup", Title: "Account and context:"},
		&cobra.Group{ID: "email", Title: "Email and resources:"},
		&cobra.Group{ID: "webhooks", Title: "Local webhooks:"},
		&cobra.Group{ID: "utilities", Title: "Utilities:"},
	)
	for _, c := range root.Commands() {
		c.GroupID = groups[c.Name()]
	}
	root.SetHelpCommandGroupID("utilities")
	root.SetCompletionCommandGroupID("utilities")
	var wrap func(*cobra.Command)
	wrap = func(c *cobra.Command) {
		key := strings.TrimPrefix(c.CommandPath(), "lettermint ")
		if example := commandExample(key); example != "" {
			c.Example = "  " + example
		}
		if original := c.RunE; original != nil {
			c.RunE = func(cmd *cobra.Command, args []string) error {
				ui := a.ui(cmd)
				// These commands produce raw bytes or help and must stay undecorated.
				if key != "messages content" && !strings.HasPrefix(key, "completion") && key != "version" && cmd != root {
					ui.StartProgress(cmd.Context(), "Running "+key)
				}
				defer ui.StopProgress()
				return original(cmd, args)
			}
		}
		for _, child := range c.Commands() {
			wrap(child)
		}
	}
	wrap(root)
}

func commandExample(key string) string {
	switch key {
	case "auth login":
		return "lettermint auth login --name work"
	case "auth logout":
		return "lettermint auth logout --profile work"
	case "auth status":
		return "lettermint auth status --profile work"
	case "profiles use":
		return "lettermint profiles use work"
	case "context set":
		return "lettermint context set --profile work --project PROJECT_ID"
	case "context show":
		return "lettermint context show --profile work"
	case "messages send":
		return "lettermint messages send --profile work --project PROJECT_ID --file message.json --idempotency-key order-1042"
	case "messages content":
		return "lettermint messages content MESSAGE_ID --project PROJECT_ID --format text --output message.txt"
	case "webhooks listen":
		return "lettermint webhooks listen --project PROJECT_ID --forward-to http://localhost:3000/webhooks/lettermint"
	case "listeners replay":
		return "lettermint listeners replay SESSION_ID --sequence 12 --profile work"
	case "skills export":
		return "lettermint skills export --output ./lettermint-skills"
	case "completion":
		return "lettermint completion powershell"
	case "doctor", "version":
		return "lettermint " + key
	}
	parts := strings.Fields(key)
	if len(parts) != 2 {
		return ""
	}
	base := "lettermint " + key
	context := " --profile work"
	if parts[0] == "messages" || parts[0] == "routes" {
		context += " --project PROJECT_ID"
	}
	switch parts[1] {
	case "list":
		return base + context
	case "get", "events", "secret", "stop", "delete", "test", "verify", "verify-inbound-domain":
		return base + " RESOURCE_ID" + context
	case "create":
		return base + " --file input.json" + context
	case "update", "assign":
		return base + " RESOURCE_ID --file input.json" + context
	}
	return ""
}

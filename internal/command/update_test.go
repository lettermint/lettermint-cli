package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lettermint/lettermint-cli/internal/presentation"
	"github.com/lettermint/lettermint-cli/internal/update"
	"github.com/spf13/cobra"
)

func updateFixture(t *testing.T) (*update.Checker, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"tag_name":"v1.2.0","assets":[
			{"name":"lettermint_1.2.0_linux_amd64.tar.gz","state":"uploaded","size":100},
			{"name":"install.sh","state":"uploaded","size":100},
			{"name":"checksums.txt","state":"uploaded","size":100},
			{"name":"provenance.jsonl","state":"uploaded","size":100}]}`)
	}))
	t.Cleanup(server.Close)
	return &update.Checker{
		CachePath: filepath.Join(t.TempDir(), "cache", "update.json"),
		HTTP:      server.Client(), Endpoint: server.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
		OS:  "linux", Arch: "amd64",
	}, requests
}

func updateRoot(a *app, run func(*cobra.Command, []string) error) *cobra.Command {
	root := &cobra.Command{Use: "lettermint", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().BoolVar(&a.noInput, "no-input", false, "Do not prompt")
	for _, key := range []string{"skills list", "version", "completion", "messages content", "webhooks listen"} {
		parts := strings.Fields(key)
		parent := root
		if len(parts) == 2 {
			parent = &cobra.Command{Use: parts[0]}
			root.AddCommand(parent)
		}
		parent.AddCommand(&cobra.Command{Use: parts[len(parts)-1], Args: cobra.NoArgs, RunE: run})
	}
	a.configurePresentation(root)
	return root
}

func updateApp(c *update.Checker, out, diagnostic io.Writer, options presentation.Options, stdoutTTY, stderrTTY bool) *app {
	return &app{
		version: "1.0.0", updates: c, display: options,
		presenter: presentation.WithTerminals(out, diagnostic, options,
			presentation.Terminal{TTY: stdoutTTY}, presentation.Terminal{TTY: stderrTTY}),
	}
}

func waitForUpdate(t *testing.T, c *update.Checker) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(c.CachePath)
		var cached struct{ Latest string }
		if json.Unmarshal(data, &cached) == nil && cached.Latest == "1.2.0" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("update was not cached")
}

func TestFreshUpdateFollowsSuccessfulOutput(t *testing.T) {
	c, requests := updateFixture(t)
	var out, diagnostic bytes.Buffer
	a := updateApp(c, &out, &diagnostic, presentation.Options{Plain: true}, true, true)
	root := updateRoot(a, func(cmd *cobra.Command, _ []string) error {
		waitForUpdate(t, c)
		if diagnostic.Len() != 0 {
			t.Fatal("background worker wrote output")
		}
		_, err := io.WriteString(cmd.OutOrStdout(), "result\n")
		return err
	})
	root.SetOut(&out)
	root.SetErr(&diagnostic)
	root.SetArgs([]string{"skills", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "Update available: 1.0.0 -> 1.2.0\n" +
		"https://github.com/lettermint/lettermint-cli/releases/tag/v1.2.0\n" +
		"Installation guide: https://github.com/lettermint/lettermint-cli/blob/main/docs/installation.md\n"
	if out.String() != "result\n" || diagnostic.String() != want || requests.Load() != 1 {
		t.Fatalf("stdout=%q stderr=%q requests=%d", out.String(), diagnostic.String(), requests.Load())
	}
}

func TestUpdateSuppression(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		args                 []string
		options              presentation.Options
		stdoutTTY, stderrTTY bool
	}{
		{"json", []string{"skills", "list"}, presentation.Options{JSON: true}, true, true},
		{"pipe", []string{"skills", "list"}, presentation.Options{}, false, true},
		{"plain-pipe", []string{"skills", "list"}, presentation.Options{Plain: true}, false, true},
		{"stderr-file", []string{"skills", "list"}, presentation.Options{}, true, false},
		{"no-input", []string{"skills", "list", "--no-input"}, presentation.Options{}, true, true},
		{"version", []string{"version"}, presentation.Options{}, true, true},
		{"content", []string{"messages", "content"}, presentation.Options{}, true, true},
		{"completion", []string{"completion"}, presentation.Options{}, true, true},
		{"dynamic-completion", []string{"__complete", "skills", ""}, presentation.Options{}, true, true},
		{"help", []string{"skills", "--help"}, presentation.Options{}, true, true},
		{"help-command", []string{"help", "skills"}, presentation.Options{}, true, true},
		{"root", nil, presentation.Options{}, true, true},
		{"invalid-arguments", []string{"skills", "list", "extra"}, presentation.Options{}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, requests := updateFixture(t)
			var out, diagnostic bytes.Buffer
			a := updateApp(c, &out, &diagnostic, tc.options, tc.stdoutTTY, tc.stderrTTY)
			ran := false
			root := updateRoot(a, func(cmd *cobra.Command, _ []string) error {
				ran = true
				_, err := io.WriteString(cmd.OutOrStdout(), "{\"ok\":true}\n")
				return err
			})
			root.SetOut(&out)
			root.SetErr(&diagnostic)
			root.SetArgs(tc.args)
			err := root.Execute()
			if (err != nil) != (tc.name == "invalid-arguments") {
				t.Fatal(err)
			}
			if ran && out.String() != "{\"ok\":true}\n" {
				t.Fatalf("result changed: %q", out.String())
			}
			if requests.Load() != 0 || strings.Contains(diagnostic.String(), "Update available") {
				t.Fatal("suppressed command checked for an update")
			}
			if _, err := os.Stat(filepath.Dir(c.CachePath)); !os.IsNotExist(err) {
				t.Fatal("suppressed command opened the cache")
			}
		})
	}
}

func TestListenerAndFailedCommandDeferFreshAlert(t *testing.T) {
	for _, listener := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "listener"}[listener], func(t *testing.T) {
			c, requests := updateFixture(t)
			var out, diagnostic bytes.Buffer
			a := updateApp(c, &out, &diagnostic, presentation.Options{Plain: true}, true, true)
			failure := errors.New("command failed")
			root := updateRoot(a, func(cmd *cobra.Command, _ []string) error {
				waitForUpdate(t, c)
				_, _ = io.WriteString(cmd.OutOrStdout(), "event\n")
				if diagnostic.Len() != 0 {
					t.Fatal("background worker mixed an alert into command output")
				}
				if !listener {
					return failure
				}
				return nil
			})
			root.SetOut(&out)
			root.SetErr(&diagnostic)
			args := []string{"skills", "list"}
			if listener {
				args = []string{"webhooks", "listen"}
			}
			root.SetArgs(args)
			err := root.Execute()
			if listener && err != nil || !listener && err != failure {
				t.Fatalf("command error changed: %v", err)
			}
			if diagnostic.Len() != 0 || out.String() != "event\n" {
				t.Fatalf("stdout=%q stderr=%q", out.String(), diagnostic.String())
			}
			// The next command must display the cached result before its own work.
			next := updateRoot(a, func(*cobra.Command, []string) error {
				if !strings.Contains(diagnostic.String(), "Update available: 1.0.0 -> 1.2.0") {
					t.Fatal("cached alert was not displayed before command work")
				}
				return nil
			})
			next.SetArgs([]string{"skills", "list"})
			if err := next.Execute(); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatal("cached alert made a new request")
			}
		})
	}
}

func TestRequiredFlagsValidatedBeforeUpdateCheck(t *testing.T) {
	c, requests := updateFixture(t)
	var out, diagnostic bytes.Buffer
	a := updateApp(c, &out, &diagnostic, presentation.Options{}, true, true)
	root := updateRoot(a, func(*cobra.Command, []string) error {
		t.Fatal("command ran without required flag")
		return nil
	})
	cmd, _, err := root.Find([]string{"skills", "list"})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Flags().String("required", "", "Required input")
	if err := cmd.MarkFlagRequired("required"); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"skills", "list"})
	if root.Execute() == nil || requests.Load() != 0 {
		t.Fatal("missing required flag did not prevent the check")
	}
	if _, err := os.Stat(filepath.Dir(c.CachePath)); !os.IsNotExist(err) {
		t.Fatal("opened cache before flag validation")
	}
}

type failedNoticeWriter struct{}

func (failedNoticeWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestNoticeWriteFailureDoesNotFailCommand(t *testing.T) {
	c, _ := updateFixture(t)
	var out bytes.Buffer
	a := updateApp(c, &out, failedNoticeWriter{}, presentation.Options{Plain: true}, true, true)
	root := updateRoot(a, func(*cobra.Command, []string) error {
		waitForUpdate(t, c)
		return nil
	})
	root.SetArgs([]string{"skills", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal("notice changed command result:", err)
	}
}

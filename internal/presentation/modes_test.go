package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/lettermint/lettermint-cli/internal/api"
)

func TestOutputModes(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		options                                  Options
		outTTY, errTTY, json, colorOut, colorErr bool
	}{
		{"terminal", Options{}, true, true, false, true, true},
		{"pipes", Options{}, false, false, true, false, false},
		{"stdout pipe", Options{}, false, true, true, false, false},
		{"stderr pipe", Options{}, true, false, false, true, false},
		{"explicit json", Options{JSON: true, Color: "always"}, true, true, true, false, false},
		{"plain", Options{Plain: true, Color: "always"}, true, true, false, false, false},
		{"plain pipe", Options{Plain: true}, false, false, false, false, false},
		{"never", Options{Color: "never"}, true, true, false, false, false},
		{"force stderr color", Options{Color: "always"}, true, false, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			terminal := func(tty bool) Terminal {
				profile := colorprofile.NoTTY
				if tty {
					profile = colorprofile.TrueColor
				}
				return Terminal{TTY: tty, Width: 80, Profile: profile}
			}
			p := WithTerminals(&out, &diagnostics, tc.options, terminal(tc.outTTY), terminal(tc.errTTY))
			if p.JSON() != tc.json {
				t.Fatal("wrong output mode")
			}
			if err := p.Result("skills list", map[string]any{"version": "test", "skills": []string{"lettermint-cli"}}, Context{}); err != nil {
				t.Fatal(err)
			}
			if err := p.Error(errors.New("local failure")); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "\x1b") != tc.colorOut || strings.Contains(diagnostics.String(), "\x1b") != tc.colorErr {
				t.Fatalf("wrong color: stdout=%q stderr=%q", out.String(), diagnostics.String())
			}
			if json.Valid(out.Bytes()) != tc.json || json.Valid(diagnostics.Bytes()) != tc.json {
				t.Fatal("wrong result encoding")
			}
			out.Reset()
			diagnostics.Reset()
			if err := p.Banner("test", false); err != nil {
				t.Fatal(err)
			}
			if err := p.Banner("test", true); err != nil {
				t.Fatal(err)
			}
			if (tc.json || tc.options.Plain) && (out.Len() != 0 || diagnostics.Len() != 0) {
				t.Fatal("banner entered machine or plain output")
			}
		})
	}
}

func TestJSONPreservesDataAndListenerRecords(t *testing.T) {
	var out, diagnostics bytes.Buffer
	p := testPresenter(&out, &diagnostics, Options{JSON: true}, 80)
	input := json.RawMessage(`{"data":{"name":"a\u001b[2J\nvalue","count":9007199254740993},"next_cursor":null}`)
	if err := p.Result("projects get", input, Context{}); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != compact.String() {
		t.Fatal("JSON data changed", out.String())
	}
	out.Reset()
	event := api.Event{Sequence: 1, DeliveryID: "delivery-one", Event: "message.inbound", Attempt: 1}
	attempt := api.Attempt{Sequence: 1, Status: 200, DurationMS: 20}
	for i := 0; i < 2; i++ {
		if err := p.ListenerAttempt(event, attempt); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatal("NDJSON framing changed")
	}
	for _, line := range lines {
		var record struct {
			Event  api.Event
			Result api.Attempt
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil || record.Event != event || record.Result != attempt {
			t.Fatal("listener record changed", line)
		}
	}
	p.ListenerConnection(false)
	if diagnostics.Len() != 0 {
		t.Fatal("added connection chatter to JSON mode")
	}
}

func TestDisplayEscapesUntrustedControlCharacters(t *testing.T) {
	untrusted := "Café\x1b[2J\x1b]52;c;secret\a\r\n\t\u009b31m\u202eevil"
	var out, diagnostics bytes.Buffer
	p := testPresenter(&out, &diagnostics, Options{Plain: true}, 40)
	if err := p.Result("projects get", map[string]any{"data": map[string]any{"name": untrusted, "étiquette": untrusted}}, Context{Profile: untrusted}); err != nil {
		t.Fatal(err)
	}
	if err := p.Error(errors.New(untrusted)); err != nil {
		t.Fatal(err)
	}
	if err := p.Prompt(untrusted); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{out.String(), diagnostics.String()} {
		for _, control := range []string{"\x1b", "\a", "\r", "\t", "\u009b", "\u202e"} {
			if strings.Contains(value, control) {
				t.Fatalf("unescaped terminal control: %q", value)
			}
		}
		if !strings.Contains(value, "Café") || !strings.Contains(value, "\\u001b") {
			t.Fatal("lost visible text", value)
		}
	}
}

func TestDetectPipeIgnoresColorEnvironment(t *testing.T) {
	for _, name := range []string{"NO_COLOR", "TERM", "CLICOLOR_FORCE"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, map[string]string{"NO_COLOR": "1", "TERM": "dumb", "CLICOLOR_FORCE": "1"}[name])
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			defer write.Close()
			got := Detect(write)
			if got.TTY || got.Profile != colorprofile.NoTTY {
				t.Fatal(got)
			}
		})
	}
}

func TestProgressModesAndDelayedStart(t *testing.T) {
	for _, options := range []Options{{JSON: true}, {Plain: true}} {
		var out, diagnostics bytes.Buffer
		p := testPresenter(&out, &diagnostics, options, 80)
		p.StartProgress(context.Background(), "Loading")
		if p.progress != nil {
			t.Fatal("progress started in a non-interactive mode")
		}
	}
	var out, diagnostics bytes.Buffer
	p := testPresenter(&out, &diagnostics, Options{}, 80)
	p.diagnostic.Profile = colorprofile.NoTTY
	p.StartProgress(context.Background(), "Loading")
	if p.progress != nil {
		t.Fatal("progress started on a dumb terminal")
	}
	p.diagnostic.Profile = colorprofile.TrueColor
	p.StartProgress(context.Background(), "Loading")
	p.StopProgress()
	if diagnostics.Len() != 0 {
		t.Fatal("short operation showed progress")
	}
}

func TestProgressStopsBeforeOutputAndOnCancellation(t *testing.T) {
	for _, finish := range []string{"result", "error", "prompt", "cancel"} {
		t.Run(finish, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			p := testPresenter(&out, &diagnostics, Options{Color: "never"}, 80)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &notifyWriter{Buffer: &diagnostics, wrote: make(chan struct{})}
			p.progress = startProgress(ctx, w, "Loading", time.Millisecond)
			select {
			case <-w.wrote:
			case <-time.After(time.Second):
				t.Fatal("progress did not start")
			}
			switch finish {
			case "result":
				if err := p.Result("version", map[string]string{"version": "test"}, Context{}); err != nil {
					t.Fatal(err)
				}
			case "error":
				if err := p.Error(errors.New("failed")); err != nil {
					t.Fatal(err)
				}
			case "prompt":
				if err := p.Prompt("Continue?"); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
				p.StopProgress()
			}
			p.StopProgress()
			value := diagnostics.String()
			clear := strings.LastIndex(value, "\r\x1b[2K")
			if clear < 0 {
				t.Fatal("activity line was not cleared")
			}
			if finish == "error" && !strings.Contains(value[clear:], "Error:") {
				t.Fatal("error preceded progress cleanup")
			}
			if finish == "prompt" && !strings.Contains(value[clear:], "[y/N]:") {
				t.Fatal("prompt preceded progress cleanup")
			}
		})
	}
}

type notifyWriter struct {
	*bytes.Buffer
	wrote    chan struct{}
	notified bool
}

func (w *notifyWriter) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	if !w.notified {
		w.notified = true
		close(w.wrote)
	}
	return n, err
}

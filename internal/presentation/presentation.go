// Package presentation renders command results without changing their data.
package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/lettermint/lettermint-cli/internal/api"
	"golang.org/x/term"
)

type Options struct {
	JSON, Plain bool
	Color       string
}

func (o Options) Validate() error {
	if o.JSON && o.Plain {
		return errors.New("--json and --plain cannot be combined")
	}
	switch o.Color {
	case "", "auto", "always", "never":
		return nil
	default:
		return errors.New("--color must be auto, always, or never")
	}
}

type Terminal struct {
	TTY     bool
	Width   int
	Profile colorprofile.Profile
}

func Detect(w io.Writer) Terminal {
	t := Terminal{Width: 80, Profile: colorprofile.NoTTY}
	if f, ok := w.(interface{ Fd() uintptr }); ok {
		t.TTY = term.IsTerminal(int(f.Fd()))
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			t.Width = width
		}
	}
	if t.TTY && os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == "" {
		t.Profile = colorprofile.Detect(w, os.Environ())
	}
	return t
}

type Presenter struct {
	out, err           io.Writer
	options            Options
	output, diagnostic Terminal
	progress           *progress
	now                func() time.Time
	location           *time.Location
}

func New(out, err io.Writer, options Options) *Presenter {
	return WithTerminals(out, err, options, Detect(out), Detect(err))
}

// WithTerminals permits deterministic tests without changing the process terminal.
func WithTerminals(out, err io.Writer, options Options, output, diagnostic Terminal) *Presenter {
	if output.Width < 1 {
		output.Width = 80
	}
	if diagnostic.Width < 1 {
		diagnostic.Width = 80
	}
	return &Presenter{out: out, err: err, options: options, output: output, diagnostic: diagnostic, now: time.Now, location: time.Local}
}

func (p *Presenter) JSON() bool {
	return p.options.JSON || (!p.options.Plain && !p.output.TTY)
}

func (p *Presenter) profile(t Terminal) colorprofile.Profile {
	if p.JSON() || p.options.Plain || p.options.Color == "never" {
		return colorprofile.NoTTY
	}
	if p.options.Color == "always" {
		return colorprofile.TrueColor
	}
	return t.Profile
}

func (p *Presenter) write(w io.Writer, t Terminal, text string) error {
	writer := &colorprofile.Writer{Forward: consoleWriter{w}, Profile: p.profile(t)}
	_, err := io.WriteString(writer, text)
	return err
}

func (p *Presenter) heading(text string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#40916c")).Render(text)
}

// Text makes untrusted text visible without executing terminal control sequences.
func Text(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
				(r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
				fmt.Fprintf(&b, "\\u%04x", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func (p *Presenter) Banner(version string, diagnostic bool) error {
	p.StopProgress()
	t, w := p.output, p.out
	if diagnostic {
		t, w = p.diagnostic, p.err
	}
	if p.JSON() || p.options.Plain || !t.TTY {
		return nil
	}
	wordmark := lipgloss.NewStyle().Bold(true).Render("Lettermint CLI") + " " +
		lipgloss.NewStyle().Faint(true).Render(Text(version))
	if t.Width < 60 {
		return p.write(w, t, wordmark+"\n\n")
	}
	body := lipgloss.NewStyle().Foreground(lipgloss.Color("#2d6a4f"))
	flap := lipgloss.NewStyle().Foreground(lipgloss.Color("#40916c")).Background(lipgloss.Color("#40916c"))
	edge := body.Background(lipgloss.Color("#40916c")).Render("▄▄")
	fold := edge + flap.Render("████") + edge
	if p.profile(t) <= colorprofile.ASCII {
		fold = body.Render("████████")
	}
	mark := strings.Join([]string{
		body.Render("▗▄▄▖"),
		body.Render("████"),
		body.Render("████"),
		fold,
		body.Render("▝██████▘"),
	}, "\n")
	banner := lipgloss.JoinHorizontal(lipgloss.Center, mark, "   "+wordmark)
	banner = lipgloss.NewStyle().PaddingLeft(2).Render(banner)
	return p.write(w, t, banner+"\n\n")
}

func (p *Presenter) Notice(text string) error {
	p.StopProgress()
	return p.write(p.err, p.diagnostic, Text(text)+"\n")
}

func (p *Presenter) Prompt(text string) error {
	p.StopProgress()
	return p.write(p.err, p.diagnostic, Text(text)+" [y/N]: ")
}

// DiagnosticWriter keeps login instructions on stderr and away from activity output.
func (p *Presenter) DiagnosticWriter() io.Writer { return diagnosticWriter{p} }

type diagnosticWriter struct{ p *Presenter }

func (w diagnosticWriter) Write(b []byte) (int, error) {
	w.p.StopProgress()
	lines := strings.Split(string(b), "\n")
	for i := range lines {
		lines[i] = Text(lines[i])
	}
	if err := w.p.write(w.p.err, w.p.diagnostic, strings.Join(lines, "\n")); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p *Presenter) Error(err error) error {
	p.StopProgress()
	if p.JSON() {
		return api.WriteError(p.err, err)
	}
	if errors.Is(err, context.Canceled) {
		return p.write(p.err, p.diagnostic, "Canceled.\n")
	}
	var b strings.Builder
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Red).Render("Error")
	fmt.Fprintf(&b, "%s: %s\n", title, Text(err.Error()))
	var remote *api.Error
	if errors.As(err, &remote) && len(remote.Details) > 0 {
		var details any
		if json.Unmarshal(remote.Details, &details) == nil {
			if fields, ok := details.(map[string]any); ok {
				for _, key := range sortedKeys(fields) {
					fmt.Fprintf(&b, "  %s:\n", Text(key))
					p.fields(&b, fields[key], "    ", false)
				}
			} else {
				p.fields(&b, details, "  ", false)
			}
		}
	}
	hint := ""
	switch api.ExitCode(err) {
	case 3:
		hint = "Check auth status for this profile. If its login is unusable, use auth logout --local, then auth login."
	case 4:
		hint = "This profile does not have permission for this operation. Stop here and check its assigned access."
	case 5:
		hint = "Check the resource ID and the selected project."
	case 7:
		hint = "Retry later. For a send, keep the same input and idempotency key."
	case 8:
		hint = "For an uncertain send result, keep the same input and idempotency key."
	case 1:
		hint = "Use the command's --help to check its arguments."
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		hint = "Check the connection and retry later. For an uncertain send, keep the same input and idempotency key."
	}
	if hint != "" {
		fmt.Fprintf(&b, "\n%s\n", hint)
	}
	return p.write(p.err, p.diagnostic, b.String())
}

type consoleWriter struct{ io.Writer }

func (w consoleWriter) Write(b []byte) (int, error) {
	restore := enableANSI(w.Writer)
	defer restore()
	return w.Writer.Write(b)
}

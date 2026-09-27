package presentation

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestUpdateNoticeModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options Options
		profile colorprofile.Profile
		color   bool
	}{
		{"terminal", Options{}, colorprofile.TrueColor, true},
		{"plain", Options{Plain: true, Color: "always"}, colorprofile.TrueColor, false},
		{"never", Options{Color: "never"}, colorprofile.TrueColor, false},
		{"no-color-terminal", Options{}, colorprofile.NoTTY, false},
		{"forced-color", Options{Color: "always"}, colorprofile.NoTTY, true},
		{"json", Options{JSON: true}, colorprofile.TrueColor, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			terminal := Terminal{TTY: true, Width: 80, Profile: tc.profile}
			p := WithTerminals(&out, &diagnostic, tc.options, terminal, terminal)
			if err := p.UpdateAvailable("v1.0.0", "1.2.0", []string{"Update with Homebrew:", "  brew update && brew upgrade --cask lettermint"}); err != nil {
				t.Fatal(err)
			}
			if out.Len() != 0 || strings.Contains(diagnostic.String(), "\x1b") != tc.color {
				t.Fatalf("stdout=%q stderr=%q", out.String(), diagnostic.String())
			}
			if tc.options.JSON != (diagnostic.Len() == 0) {
				t.Fatal("incorrect notice visibility")
			}
			if !tc.options.JSON && !strings.Contains(diagnostic.String(), "  brew update && brew upgrade --cask lettermint\n") {
				t.Fatal("missing update command")
			}
		})
	}
}

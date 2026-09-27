package update

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectHomebrewFromResolvedExecutable(t *testing.T) {
	for _, prefix := range []string{"opt/homebrew", "usr/local", "custom/brew"} {
		t.Run(prefix, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), prefix, "Caskroom", "lettermint", "1.0.0", "lettermint")
			installFile(t, executable, "binary")
			got := detectInstallation(executable, "darwin", "", "1.0.0")
			if got.method != "homebrew" {
				t.Fatalf("installation = %+v", got)
			}
			if got := strings.Join(got.instructions("1.2.0", "darwin", "arm64"), "\n"); got != "Update with Homebrew:\n  brew update && brew upgrade --cask lettermint" {
				t.Fatal(got)
			}
			link := filepath.Join(t.TempDir(), "lettermint")
			if err := os.Symlink(executable, link); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
			if detectInstallation(link, "darwin", "", "1.0.0").method != "homebrew" {
				t.Fatal("did not resolve the executable symlink")
			}
		})
	}
}

func TestDetectShellOwnershipAndCustomDirectory(t *testing.T) {
	for _, system := range []string{"darwin", "linux"} {
		t.Run(system, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "Bob's custom tools")
			executable := filepath.Join(bin, "lettermint")
			installFile(t, executable, "installed binary")
			hash := sha256.Sum256([]byte("installed binary"))
			marker := filepath.Join(bin, ".lettermint-install")
			installFile(t, marker, fmt.Sprintf("lettermint-shell-v1\nv1.0.0\n%x\n", hash))
			got := detectInstallation(executable, system, "", "1.0.0")
			resolved, err := filepath.EvalSymlinks(bin)
			if err != nil {
				t.Fatal(err)
			}
			if got.method != "shell" || got.binDir != resolved {
				t.Fatalf("installation = %+v", got)
			}
			lines := got.instructions("1.2.0", system, "amd64")
			if len(lines) != 2 || !strings.Contains(lines[1], "/releases/download/v1.2.0/install.sh | sh -s -- --version v1.2.0 --bin-dir ") ||
				!strings.HasSuffix(lines[1], shellQuote(resolved)) {
				t.Fatal(lines)
			}
			// A copied or changed executable is no longer owned by this record.
			installFile(t, executable, "changed binary")
			if detectInstallation(executable, system, "", "1.0.0").method != "" {
				t.Fatal("accepted a stale executable hash")
			}
		})
	}
}

func TestDetectPowerShellOwnership(t *testing.T) {
	for _, bom := range []string{"", "\xef\xbb\xbf"} {
		t.Run(fmt.Sprintf("bom=%t", bom != ""), func(t *testing.T) {
			localAppData := t.TempDir()
			root := filepath.Join(localAppData, "Lettermint CLI")
			executable := filepath.Join(root, "bin", "lettermint.exe")
			installFile(t, executable, "binary")
			installFile(t, filepath.Join(root, "install.json"), bom+`{"manager":"lettermint-powershell","version":"v1.0.0"}`)
			got := detectInstallation(executable, "windows", localAppData, "1.0.0")
			if got.method != "powershell" {
				t.Fatalf("installation = %+v", got)
			}
			lines := got.instructions("1.2.0", "windows", "arm64")
			if len(lines) != 3 || !strings.Contains(lines[1], "/releases/download/v1.2.0/install.ps1' -OutFile ") ||
				lines[2] != `  powershell -NoProfile -ExecutionPolicy AllSigned -File "$env:TEMP\lettermint.ps1" -Version v1.2.0` {
				t.Fatal(lines)
			}
			// The record must belong to the running executable, not another install.
			other := filepath.Join(t.TempDir(), "lettermint.exe")
			installFile(t, other, "binary")
			if detectInstallation(other, "windows", localAppData, "1.0.0").method != "" {
				t.Fatal("used the ownership record from another install")
			}
		})
	}
}

func TestUnknownOrInvalidOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, system, executable, marker, data string
	}{
		{"manual-macos", "darwin", "bin/lettermint", "", ""},
		{"manual-linux", "linux", "bin/lettermint", "", ""},
		{"homebrew-lookalike", "darwin", "Caskroom/other/1.0.0/lettermint", "", ""},
		{"homebrew-wrong-version", "darwin", "Caskroom/lettermint/2.0.0/lettermint", "", ""},
		{"bad-shell-owner", "linux", "bin/lettermint", "bin/.lettermint-install", "another-installer\nv1.0.0\nhash\n"},
		{"bad-shell-hash", "linux", "bin/lettermint", "bin/.lettermint-install", "lettermint-shell-v1\nv1.0.0\nwrong\n"},
		{"manual-windows", "windows", "Lettermint CLI/bin/lettermint.exe", "", ""},
		{"bad-powershell-owner", "windows", "Lettermint CLI/bin/lettermint.exe", "Lettermint CLI/install.json", `{"manager":"other","version":"v1.0.0"}`},
		{"bad-powershell-version", "windows", "Lettermint CLI/bin/lettermint.exe", "Lettermint CLI/install.json", `{"manager":"lettermint-powershell","version":"v2.0.0"}`},
		{"corrupt-powershell-record", "windows", "Lettermint CLI/bin/lettermint.exe", "Lettermint CLI/install.json", "{"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, filepath.FromSlash(tc.executable))
			installFile(t, executable, "binary")
			if tc.marker != "" {
				installFile(t, filepath.Join(root, filepath.FromSlash(tc.marker)), tc.data)
			}
			if got := detectInstallation(executable, tc.system, root, "1.0.0"); got.method != "" {
				t.Fatalf("guessed installation method: %+v", got)
			}
		})
	}
}

func TestManualInstructionsMatchPlatform(t *testing.T) {
	for _, system := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			lines := (installation{}).instructions("1.2.0", system, arch)
			extension := ".tar.gz"
			if system == "windows" {
				extension = ".zip"
			}
			want := fmt.Sprintf("/releases/download/v1.2.0/lettermint_1.2.0_%s_%s%s", system, arch, extension)
			if len(lines) != 2 || !strings.HasSuffix(lines[1], want) || !strings.Contains(lines[0], "manual installation guide") {
				t.Fatal(lines)
			}
		}
	}
	if lines := (installation{}).instructions("bad version", "linux", "amd64"); len(lines) != 0 {
		t.Fatal("rendered an invalid release version")
	}
}

func TestShellQuoteKeepsLiteralPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell is not required on Windows")
	}
	for _, path := range []string{"/home/user/.local/bin", "/home/Bob's tools", "/tmp/$HOME/$(echo changed)/`echo changed`"} {
		out, err := exec.Command("sh", "-c", "printf '%s' "+shellQuote(path)).Output()
		if err != nil || string(out) != path {
			t.Fatalf("path=%q output=%q error=%v", path, out, err)
		}
	}
}

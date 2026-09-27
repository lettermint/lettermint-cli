package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

type installation struct {
	method string
	binDir string
}

// Instructions describes an update. It never runs an installer or package manager.
func Instructions(current, latest string) []string {
	executable, _ := os.Executable()
	installed := detectInstallation(executable, runtime.GOOS, os.Getenv("LOCALAPPDATA"), current)
	return installed.instructions(latest, runtime.GOOS, runtime.GOARCH)
}

func detectInstallation(executable, system, localAppData, current string) installation {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || stable(current) == "" {
		return installation{}
	}
	binDir := filepath.Dir(resolved)
	if system == "darwin" && filepath.Base(resolved) == "lettermint" {
		versionDir := filepath.Dir(resolved)
		packageDir := filepath.Dir(versionDir)
		if filepath.Base(filepath.Dir(packageDir)) == "Caskroom" &&
			filepath.Base(packageDir) == "lettermint" && filepath.Base(versionDir) == stable(current) {
			return installation{method: "homebrew"}
		}
	}
	if system == "windows" && localAppData != "" {
		root := filepath.Join(localAppData, "Lettermint CLI")
		expected, err := filepath.EvalSymlinks(filepath.Join(root, "bin", "lettermint.exe"))
		if err != nil || !strings.EqualFold(resolved, expected) {
			return installation{}
		}
		var record struct{ Manager, Version string }
		// Windows PowerShell 5.1 writes UTF-8 with a byte-order mark.
		data := bytes.TrimPrefix(ownershipRecord(filepath.Join(root, "install.json")), []byte{0xef, 0xbb, 0xbf})
		if json.Unmarshal(data, &record) == nil && record.Manager == "lettermint-powershell" && record.Version == "v"+stable(current) {
			return installation{method: "powershell"}
		}
	}
	if (system == "darwin" || system == "linux") && filepath.Base(resolved) == "lettermint" {
		record := strings.Split(strings.TrimSuffix(string(ownershipRecord(filepath.Join(binDir, ".lettermint-install"))), "\n"), "\n")
		if len(record) == 3 && record[0] == "lettermint-shell-v1" && record[1] == "v"+stable(current) &&
			strings.IndexFunc(binDir, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf) }) < 0 &&
			matchesHash(resolved, record[2]) {
			return installation{method: "shell", binDir: binDir}
		}
	}
	return installation{}
}

func ownershipRecord(path string) []byte {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return nil
	}
	return data
}

func matchesHash(path, expected string) bool {
	if len(expected) != sha256.Size*2 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return false
	}
	return fmt.Sprintf("%x", hash.Sum(nil)) == expected
}

func (i installation) instructions(latest, system, arch string) []string {
	version := stable(latest)
	if version == "" {
		return nil
	}
	base := "https://github.com/lettermint/lettermint-cli/releases/download/v" + version
	switch i.method {
	case "homebrew":
		return []string{"Update with Homebrew:", "  brew update && brew upgrade --cask lettermint"}
	case "shell":
		return []string{
			"Update with the shell installer:",
			"  curl -fsSL " + base + "/install.sh | sh -s -- --version v" + version + " --bin-dir " + shellQuote(i.binDir),
		}
	case "powershell":
		return []string{
			"Update in PowerShell:",
			`  Invoke-WebRequest -UseBasicParsing -Uri '` + base + `/install.ps1' -OutFile "$env:TEMP\lettermint.ps1"`,
			`  powershell -NoProfile -ExecutionPolicy AllSigned -File "$env:TEMP\lettermint.ps1" -Version v` + version,
		}
	default:
		lines := []string{"Installation method not detected. Use your package manager or the manual installation guide."}
		label := map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows"}[system]
		if label != "" && (arch == "amd64" || arch == "arm64") {
			extension := ".tar.gz"
			if system == "windows" {
				extension = ".zip"
			}
			lines = append(lines, fmt.Sprintf("Download for %s (%s): %s/lettermint_%s_%s_%s%s", label, arch, base, version, system, arch, extension))
		}
		return lines
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

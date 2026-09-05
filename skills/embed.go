package skills

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed lettermint-cli
var Package embed.FS

func Export(destination string) error {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("skill output directory must not exist")
	}
	parent := filepath.Dir(destination)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(parent, ".lettermint-skills-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	err = fs.WalkDir(Package, "lettermint-cli", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		target := filepath.Join(temp, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		b, e := Package.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0644)
	})
	if err != nil {
		return err
	}
	return os.Rename(temp, destination)
}

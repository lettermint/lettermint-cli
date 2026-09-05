package command

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func writeContentFile(ctx context.Context, destination string, source io.Reader) error {
	if _, err := os.Lstat(destination); err == nil {
		return &os.PathError{Op: "create", Path: destination, Err: os.ErrExist}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".lettermint-content-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err = io.Copy(temp, source); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Link publishes the complete file without replacing a path created during the download.
	return os.Link(temp.Name(), destination)
}

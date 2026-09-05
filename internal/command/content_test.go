package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestContentFileIsPublishedOnlyAfterCompleteDownload(t *testing.T) {
	for _, format := range []string{"raw", "html", "text"} {
		for _, interrupted := range []bool{false, true} {
			t.Run(format+"/interrupted="+strconv.FormatBool(interrupted), func(t *testing.T) {
				content := []byte("  Café\r\n\x00end\n")
				directory := filepath.Join(t.TempDir(), "Exports café")
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(directory, "Message café.txt")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
						t.Error("output exists before the download completes")
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(content)))
					if interrupted {
						w.Write(content[:4])
						return
					}
					w.Write(content)
				}))
				defer server.Close()
				cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
				cmd.SetArgs([]string{"messages", "content", "message-one", "--format", format, "--output", destination, "--no-input"})
				cmd.SetOut(io.Discard)
				err := cmd.Execute()
				if interrupted {
					if !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("download error=%v", err)
					}
					if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("failed download left its destination: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					actual, err := os.ReadFile(destination)
					if err != nil || !bytes.Equal(actual, content) {
						t.Fatalf("output=%q error=%v", actual, err)
					}
					info, err := os.Stat(destination)
					if err != nil {
						t.Fatal(err)
					}
					if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
						t.Fatal("export is not private")
					}
				}
				assertNoContentTempFiles(t, directory)
			})
		}
	}
}

type callbackReader struct {
	read func([]byte) (int, error)
}

func (r callbackReader) Read(p []byte) (int, error) { return r.read(p) }

func TestContentExportDoesNotReplaceAnExistingOrNewDestination(t *testing.T) {
	for _, duringDownload := range []bool{false, true} {
		t.Run("during download="+strconv.FormatBool(duringDownload), func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, "output.txt")
			create := func() {
				if err := os.WriteFile(destination, []byte("keep this file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if !duringDownload {
				create()
			}
			err := writeContentFile(context.Background(), destination, callbackReader{read: func(p []byte) (int, error) {
				if !duringDownload {
					t.Fatal("read content despite an existing destination")
				}
				create()
				return copy(p, "new content"), io.EOF
			}})
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("error=%v", err)
			}
			actual, err := os.ReadFile(destination)
			if err != nil || string(actual) != "keep this file" {
				t.Fatalf("replaced existing output: %q error=%v", actual, err)
			}
			assertNoContentTempFiles(t, dir)
		})
	}
}

func TestCanceledContentExportRemovesTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "output.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := writeContentFile(ctx, destination, callbackReader{read: func(p []byte) (int, error) {
		cancel()
		return copy(p, "partial content"), io.EOF
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled download left output: %v", err)
	}
	assertNoContentTempFiles(t, dir)
}

func assertNoContentTempFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".lettermint-content-") {
			t.Errorf("temporary content file remains: %s", entry.Name())
		}
	}
}

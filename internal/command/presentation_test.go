package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandErrorsUseSelectedModeBeforeAndAfterInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		json bool
	}{
		{[]string{"unknown-command"}, true},
		{[]string{"--json", "unknown-command"}, true},
		{[]string{"unknown-command", "--json"}, true},
		{[]string{"--unknown", "--json"}, true},
		{[]string{"version", "--unknown", "--plain"}, false},
		{[]string{"--plain", "unknown-command"}, false},
		{[]string{"projects", "get", "--plain"}, false},
		{[]string{"version", "--color", "wrong", "--plain"}, false},
		{[]string{"version", "--json", "--plain"}, true},
		{[]string{"--help", "--json", "--plain"}, true},
		{[]string{"messages", "send", "--subject", "--plain", "--unknown", "--json"}, true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			code := Execute(context.Background(), tc.args, "test", "public", strings.NewReader(""), &out, &diagnostics)
			if code != 1 || out.Len() != 0 || json.Valid(diagnostics.Bytes()) != tc.json || bytes.Contains(diagnostics.Bytes(), []byte("\x1b")) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
			}
		})
	}
}

func TestDisplayFlagsAreNotReadFromInputValues(t *testing.T) {
	root := New("test", "public")
	for _, tc := range []struct {
		args        []string
		json, plain bool
	}{
		{[]string{"messages", "send", "--subject", "--json", "--plain"}, false, true},
		{[]string{"messages", "send", "--subject=--plain", "--json"}, true, false},
		{[]string{"version", "--", "--plain"}, false, false},
		{[]string{"version", "--json=false", "--plain=true"}, false, true},
	} {
		options := errorOptions(root, tc.args)
		if options.JSON != tc.json || options.Plain != tc.plain {
			t.Fatalf("%v: %+v", tc.args, options)
		}
	}
}

func TestWelcomeNeedsNoSavedLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("LETTERMINT_CONFIG_DIR", path)
	for _, args := range [][]string{nil, {"--help"}, {"--plain"}, {"messages", "--help"}} {
		var out, diagnostics bytes.Buffer
		code := Execute(context.Background(), args, "test", "", strings.NewReader(""), &out, &diagnostics)
		if code != 0 || diagnostics.Len() != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatalf("code=%d output=%s error=%s", code, out.String(), diagnostics.String())
		}
		if strings.Contains(out.String(), "Send email and develop with Lettermint") || strings.HasPrefix(out.String(), "lettermint\n") {
			t.Fatal("welcome repeats the application name and description")
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("help opened the configuration store")
	}
}

func TestReadableResourceOutputKeepsRequestsAndContext(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/projects/project-one/routes" || r.URL.Query().Get("page[size]") != "2" || r.Header.Get("Authorization") != "Bearer grant" {
			t.Errorf("request changed: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, `{"data":[{"id":"route-one","name":"Receipt emails","route_type":"transactional","is_default":true}],"links":{"next":null}}`)
	}))
	defer server.Close()
	for _, flag := range []string{"--plain", "--json"} {
		cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
		cmd.SetArgs([]string{"routes", "list", "--limit", "2", flag})
		var out, diagnostics bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&diagnostics)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if flag == "--plain" && (!strings.Contains(out.String(), "Profile: work") || !strings.Contains(out.String(), "route-one") || strings.Contains(out.String(), "\"data\"")) {
			t.Fatal(out.String())
		}
		if flag == "--json" && !json.Valid(out.Bytes()) {
			t.Fatal(out.String())
		}
	}
	if calls != 2 {
		t.Fatalf("presentation made extra API calls: %d", calls)
	}
}

func TestContentAndCompletionRemainUndecorated(t *testing.T) {
	body := []byte("Café\r\n\x1b[31mexact source\x00\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer server.Close()
	for _, flags := range [][]string{{"--plain"}, {"--json"}, {"--color", "always"}, {"--plain", "--color", "always"}} {
		cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
		cmd.SetArgs(append([]string{"messages", "content", "message-one"}, flags...))
		var out, diagnostics bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&diagnostics)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), body) || diagnostics.Len() != 0 {
			t.Fatal("changed content export")
		}
		for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
			plain := newWithStore("test", "public", nil)
			plain.SetArgs([]string{"completion", shell})
			var original bytes.Buffer
			plain.SetOut(&original)
			if err := plain.Execute(); err != nil {
				t.Fatal(err)
			}
			styled := newWithStore("test", "public", nil)
			styled.SetArgs(append([]string{"completion", shell}, flags...))
			var actual bytes.Buffer
			styled.SetOut(&actual)
			if err := styled.Execute(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original.Bytes(), actual.Bytes()) {
				t.Fatalf("%s completion changed for %v", shell, flags)
			}
		}
	}
}

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/lettermint/lettermint-cli/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSendRejectsMixedInputBeforeAuthentication(t *testing.T) {
	cmd := New("test", "")
	cmd.SetArgs([]string{"messages", "send", "--file", "-", "--subject", "mixed", "--idempotency-key", "key"})
	cmd.SetIn(strings.NewReader(`{}`))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatal(err)
	}
}
func TestEveryPlannedCommandHasHelp(t *testing.T) {
	for group, actions := range map[string][]string{"auth": {"login", "status", "logout"}, "profiles": {"list", "use"}, "context": {"show", "set"}, "messages": {"send", "list", "get", "events", "content"}, "projects": {"list", "get", "create"}, "routes": {"list", "get", "create", "update", "verify-inbound-domain"}, "domains": {"list", "get", "create", "verify", "assign"}, "webhooks": {"list", "get", "create", "update", "delete", "test", "listen"}, "listeners": {"list", "get", "stop", "replay", "secret"}, "skills": {"list", "export"}} {
		for _, action := range actions {
			t.Run(group+"/"+action, func(t *testing.T) {
				root := New("test", "")
				root.SetArgs([]string{group, action, "--help"})
				var out bytes.Buffer
				root.SetOut(&out)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "Usage:") {
					t.Fatal(out.String())
				}
			})
		}
	}
}
func TestCompletionForEveryShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		cmd := New("test", "")
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"completion", shell})
		if err := cmd.Execute(); err != nil || out.Len() == 0 {
			t.Fatalf("%s: %v", shell, err)
		}
	}
}
func TestExportSkillsToUnicodePathWithSpaces(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills café")
	cmd := New("test", "")
	cmd.SetArgs([]string{"skills", "export", "--output", target, "--json", "--no-input"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(target, "lettermint-cli", "SKILL.md"))
	if err != nil || !bytes.Contains(b, []byte("name: lettermint-cli")) {
		t.Fatalf("%s %v", b, err)
	}
}

type memoryVault struct{ credentials config.Credentials }

func (v *memoryVault) Get(string) (config.Credentials, error)   { return v.credentials, nil }
func (v *memoryVault) Set(_ string, c config.Credentials) error { v.credentials = c; return nil }
func (v *memoryVault) Delete(string) error                      { return nil }

func TestSendUsesExplicitProjectAndClearsOldRoute(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/send" || r.URL.Query().Get("project_id") != "new-project" || r.Header.Get("Idempotency-Key") != "order-1042" {
			t.Error("wrong send context")
		}
		if r.Header.Get("Authorization") != "Bearer original-grant" {
			t.Error("wrong grant")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["route_id"]; ok {
			t.Error("route from old project was retained")
		}
		if body["text"] != "Line one.\r\nLine two." {
			t.Error("content changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write([]byte(`{"message_id":"message-one","status":"pending"}`))
	}))
	defer server.Close()
	store := &config.Store{Dir: t.TempDir(), Vault: &memoryVault{config.Credentials{ConnectionID: "grant-one", AccessToken: "original-grant", ExpiresAt: time.Now().Add(time.Hour)}}}
	err := store.Update(context.Background(), func(c *config.Config) error {
		c.Selected = "work"
		c.Profiles["work"] = config.Profile{ConnectionID: "grant-one", APIURL: server.URL, ClientID: "public", Project: "old-project", Route: "old-route"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := newWithStore("test", "public", store)
	cmd.SetArgs([]string{"messages", "send", "--project", "new-project", "--file", "-", "--idempotency-key", "order-1042", "--json", "--no-input"})
	cmd.SetIn(strings.NewReader(`{"from":"sender@example.com","to":["reader@example.net"],"subject":"Order","text":"Line one.\r\nLine two.","metadata":{"order":"1042"}}`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(out.String(), `"status":"accepted"`) {
		t.Fatal(out.String())
	}
	_, p, err := store.Resolve("work")
	if err != nil || p.Project != "old-project" || p.Route != "old-route" {
		t.Fatal("explicit flags changed saved defaults")
	}
}

func TestSharedResourceContract(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		input        string
		method, path string
		query        map[string]string
		body         map[string]any
	}{
		{"route list", []string{"routes", "list", "--limit", "2"}, "", "GET", "/v1/projects/project-one/routes", map[string]string{"page[size]": "2"}, nil},
		{"route create", []string{"routes", "create", "--file", "-"}, `{"name":"Receipts","route_type":"transactional"}`, "POST", "/v1/projects/project-one/routes", nil, map[string]any{"name": "Receipts", "route_type": "transactional"}},
		{"route update", []string{"routes", "update", "route-one", "--file", "-"}, `{"name":"Updated"}`, "PUT", "/v1/routes/route-one", map[string]string{"filter[project]": "project-one"}, map[string]any{"name": "Updated"}},
		{"domain verify", []string{"domains", "verify", "domain-one"}, "", "POST", "/v1/domains/domain-one/dns-records/verify", map[string]string{"filter[project]": "project-one"}, nil},
		{"domain assign", []string{"domains", "assign", "domain-one", "--file", "-", "--yes"}, `{"project_ids":["project-one"]}`, "PUT", "/v1/domains/domain-one/projects", map[string]string{"filter[project]": "project-one"}, map[string]any{"project_ids": []any{"project-one"}}},
		{"project defaults", []string{"projects", "create", "--file", "-"}, `{"name":"New project"}`, "POST", "/v1/projects", nil, map[string]any{"name": "New project", "smtp_enabled": false, "initial_routes": "transactional"}},
		{"project explicit options", []string{"projects", "create", "--file", "-"}, `{"name":"New project","smtp_enabled":true,"initial_routes":"both"}`, "POST", "/v1/projects", nil, map[string]any{"name": "New project", "smtp_enabled": true, "initial_routes": "both"}},
		{"message events", []string{"messages", "events", "message-one", "--limit", "2"}, "", "GET", "/v1/messages/message-one/events", map[string]string{"filter[project]": "project-one", "filter[route_id]": "route-one", "page[size]": "2"}, nil},
		{"listener list", []string{"listeners", "list", "--limit", "2"}, "", "GET", "/v1/listeners", map[string]string{"limit": "2"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path || r.Header.Get("Authorization") != "Bearer grant" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if len(r.URL.Query()) != len(tc.query) {
					t.Errorf("unexpected query: %v", r.URL.Query())
				}
				for key, value := range tc.query {
					if r.URL.Query().Get(key) != value {
						t.Errorf("query %s: %s", key, r.URL.Query().Get(key))
					}
				}
				if tc.body != nil {
					var actual map[string]any
					if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
						t.Error(err)
					}
					expected, _ := json.Marshal(tc.body)
					received, _ := json.Marshal(actual)
					if !bytes.Equal(expected, received) {
						t.Errorf("body: %s", received)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[],"links":{"next":null}}`))
			}))
			defer server.Close()
			store := authenticatedStore(t, server.URL)
			cmd := newWithStore("test", "public", store)
			cmd.SetArgs(append(tc.args, "--json", "--no-input"))
			cmd.SetIn(strings.NewReader(tc.input))
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one request, got %d", calls)
			}
		})
	}
}

func authenticatedStore(t *testing.T, apiURL string) *config.Store {
	t.Helper()
	store := &config.Store{Dir: t.TempDir(), Vault: &memoryVault{config.Credentials{ConnectionID: "grant-one", AccessToken: "grant", ExpiresAt: time.Now().Add(time.Hour)}}}
	if err := store.Update(context.Background(), func(c *config.Config) error {
		c.Selected = "work"
		c.Profiles["work"] = config.Profile{ConnectionID: "grant-one", APIURL: apiURL, ClientID: "public", Project: "project-one", Route: "route-one"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestContentUsesExistingEndpointsWithoutChangingBytes(t *testing.T) {
	for _, format := range []string{"raw", "html", "text"} {
		t.Run(format, func(t *testing.T) {
			content := []byte("  Café\r\n\x00end\n")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				endpoint := format
				if format == "raw" {
					endpoint = "source"
					if r.URL.Query().Get("format") != "stored" {
						t.Error("missing exact source format")
					}
				}
				if r.URL.Path != "/v1/messages/message-one/"+endpoint || r.URL.Query().Get("filter[project]") != "project-one" {
					t.Errorf("wrong content request: %s", r.URL)
				}
				w.Write(content)
			}))
			defer server.Close()
			cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"messages", "content", "message-one", "--format", format, "--no-input"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), content) {
				t.Fatalf("content changed: %q", out.Bytes())
			}
		})
	}
}

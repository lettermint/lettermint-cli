package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/lettermint/lettermint-cli/internal/api"
)

const testIdentity = `{"data":{"user":{"id":"user-one","email":"user@example.com"},"team":{"id":"team-one","name":"Demo"},"connection_id":"grant-one"}}`

func capabilitiesServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/identity":
			w.Write([]byte(testIdentity))
		case "/v1/capabilities":
			if r.Header.Get("Authorization") != "Bearer grant" {
				t.Error("capabilities used the wrong grant")
			}
			w.WriteHeader(status)
			w.Write([]byte(body))
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runJSON(t *testing.T, apiURL string, args ...string) map[string]any {
	t.Helper()
	cmd := newWithStore("test", "public", authenticatedStore(t, apiURL))
	cmd.SetArgs(append(args, "--json", "--no-input"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAuthStatusShowsPermissions(t *testing.T) {
	server := capabilitiesServer(t, 200, `{"data":{"api_version":"v1","permissions":["projects:read","messages:send"]}}`)
	result := runJSON(t, server.URL, "auth", "status")
	if result["profile"] != "work" || result["identity"] == nil {
		t.Fatalf("existing fields changed: %v", result)
	}
	permissions, _ := result["permissions"].([]any)
	if len(permissions) != 2 || permissions[0] != "projects:read" || permissions[1] != "messages:send" {
		t.Fatalf("permissions: %v", result["permissions"])
	}
}

func TestAuthStatusWorksWithoutCapabilities(t *testing.T) {
	for _, status := range []int{404, 500} {
		server := capabilitiesServer(t, status, `{"message":"unavailable"}`)
		result := runJSON(t, server.URL, "auth", "status")
		if _, ok := result["permissions"]; ok || result["identity"] == nil {
			t.Fatalf("%d: %v", status, result)
		}
	}
}

func TestDoctorReportsCapabilities(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		check      map[string]any
		warning    string
	}{
		{"available", `{"data":{"api_version":"v1","permissions":["projects:read","messages:send"]}}`, 200, map[string]any{"status": "available", "api_version": "v1", "permission_count": 2.0}, ""},
		{"unsupported", `{"data":{"api_version":"v2","permissions":[]}}`, 200, map[string]any{"status": "available", "api_version": "v2", "permission_count": 0.0}, "API version v2 is not supported"},
		{"older server", `{"message":"Not Found"}`, 404, map[string]any{"status": "not available"}, ""},
		{"failure", `{"message":"Server Error"}`, 500, map[string]any{"status": "failed", "error": "http_error: Server Error"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runJSON(t, capabilitiesServer(t, tc.status, tc.body).URL, "doctor")
			if result["api"] != "available" || result["identity"] == nil {
				t.Fatalf("existing fields changed: %v", result)
			}
			check, _ := result["capabilities"].(map[string]any)
			expected, _ := json.Marshal(tc.check)
			actual, _ := json.Marshal(check)
			if !bytes.Equal(expected, actual) {
				t.Fatalf("capabilities: %s", actual)
			}
			warnings, _ := result["warnings"].([]any)
			if tc.warning == "" && len(warnings) > 0 || tc.warning != "" && (len(warnings) != 1 || !strings.Contains(warnings[0].(string), tc.warning)) {
				t.Fatalf("warnings: %v", warnings)
			}
		})
	}
}

func TestPermissionErrorNamesRequiredPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"This action is unauthorized."}`))
	}))
	defer server.Close()
	cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
	cmd.SetArgs([]string{"messages", "send", "--from", "a@example.com", "--to", "b@example.net", "--subject", "Hi", "--text", "Hi", "--json", "--no-input"})
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	var missing *api.PermissionError
	if !errors.As(err, &missing) || !slices.Equal(missing.Permissions, []string{"messages:send"}) || api.ExitCode(err) != 4 {
		t.Fatalf("%T %v", err, err)
	}
	var out bytes.Buffer
	if err = api.WriteError(&out, err); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"required_permissions":["messages:send"]`) || !strings.Contains(out.String(), `"code":"permission_denied"`) {
		t.Fatal(out.String())
	}
}

func TestEveryPermissionHintIsAKnownCommand(t *testing.T) {
	root := New("test", "")
	for command := range commandPermissions {
		found, _, err := root.Find(strings.Fields(command))
		if err != nil || found.CommandPath() != "lettermint "+command {
			t.Errorf("unknown command %q", command)
		}
	}
}

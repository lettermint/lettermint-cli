package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lettermint/lettermint-cli/internal/api"
)

func TestResendPostsToMessageWithoutSavedContext(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		project    string
		key        string
		recipients []string
	}{
		{name: "all recipients", args: []string{"--route", "ignored-route"}},
		{name: "explicit project, recipients, and key", args: []string{"--project", "message-project", "--recipient", "reader@example.net", "--recipient", "copy@example.net", "--idempotency-key", "resend-1042"}, project: "message-project", key: "resend-1042", recipients: []string{"reader@example.net", "copy@example.net"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v1/messages/message-one/resend" {
					t.Errorf("wrong request: %s %s", r.Method, r.URL)
				}
				if r.URL.Query().Get("filter[project]") != tc.project || (tc.project == "" && r.URL.Query().Has("filter[project]")) || r.URL.Query().Has("filter[route_id]") {
					t.Errorf("wrong filters: %s", r.URL)
				}
				if r.Header.Get("Idempotency-Key") != tc.key || (tc.key == "" && r.Header.Values("Idempotency-Key") != nil) {
					t.Errorf("wrong idempotency key: %q", r.Header.Values("Idempotency-Key"))
				}
				var body struct {
					Recipients []string `json:"recipients"`
				}
				raw, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(raw, &body); err != nil || strings.Join(body.Recipients, ",") != strings.Join(tc.recipients, ",") || (tc.recipients == nil && strings.Contains(string(raw), "recipients")) {
					t.Errorf("wrong body: %s", raw)
				}
				w.WriteHeader(http.StatusAccepted)
				io.WriteString(w, `{"message_id":"message-two","status":"pending"}`)
			}))
			defer server.Close()
			cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
			cmd.SetArgs(append([]string{"messages", "resend", "message-one", "--profile", "work", "--plain", "--yes"}, tc.args...))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if !strings.Contains(out.String(), "Check delivery with: lettermint messages events message-two --profile work\n") {
				t.Fatalf("missing delivery hint: %s", &out)
			}
			if strings.Contains(out.String(), "Route:") || strings.Contains(out.String(), "project-one") {
				t.Fatalf("ignored context displayed: %s", &out)
			}
		})
	}
}

func TestResendRequiresConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, input, expected string
		args                  []string
	}{
		{name: "no input", args: []string{"--no-input"}, expected: "this operation requires --yes with --no-input"},
		{name: "declined", input: "n\n", expected: "operation canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s", r.URL)
			}))
			defer server.Close()
			cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
			cmd.SetArgs(append([]string{"messages", "resend", "message-one", "--plain"}, tc.args...))
			cmd.SetIn(strings.NewReader(tc.input))
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			if err := cmd.Execute(); err == nil || err.Error() != tc.expected {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestResendFailureKeepsRetryContext(t *testing.T) {
	for _, key := range []string{"", "resend-1042"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"code":"RESEND_LIMIT_REACHED","message":"A message can be resent at most 5 times in 24 hours."}}`)
		}))
		cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
		args := []string{"messages", "resend", "message-one", "--yes", "--json"}
		if key != "" {
			args = append(args, "--idempotency-key", key)
		}
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		err := cmd.Execute()
		server.Close()
		var send *api.SendError
		if !errors.As(err, &send) || send.HasIdempotencyKey != (key != "") || api.ExitCode(err) != 7 || api.ErrorCode(err) != "RESEND_LIMIT_REACHED" {
			t.Fatalf("error=%v code=%s", err, api.ErrorCode(err))
		}
	}
}

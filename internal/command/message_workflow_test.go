package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/config"
	"github.com/lettermint/lettermint-cli/internal/presentation"
)

func TestSendWithoutKeyProducesUsableDeliveryCommand(t *testing.T) {
	sends, reads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/send":
			sends++
			if r.Method != "POST" || r.URL.Query().Get("project_id") != "send-project" || r.URL.Query().Has("route_id") {
				t.Errorf("wrong send context: %s %s", r.Method, r.URL)
			}
			if _, present := r.Header["Idempotency-Key"]; present {
				t.Error("unexpected idempotency header")
			}
			io.WriteString(w, `{"message_id":"message-one","status":"pending"}`)
		case "/v1/messages/message-one/events":
			reads++
			if r.URL.Query().Has("filter[project]") || r.URL.Query().Has("filter[route_id]") {
				t.Errorf("unexpected filters: %s", r.URL)
			}
			io.WriteString(w, `{"data":[],"links":{"next":null}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	store := authenticatedStore(t, server.URL)
	cmd := newWithStore("test", "public", store)
	cmd.SetArgs([]string{"messages", "send", "--profile", "work", "--project", "send-project", "--file", "-", "--plain"})
	cmd.SetIn(strings.NewReader(`{"from":"sender@example.com","to":["reader@example.net"],"subject":"Order","text":"Hello"}`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var next string
	for _, line := range strings.Split(out.String(), "\n") {
		if suffix, ok := strings.CutPrefix(line, "Check delivery with: lettermint "); ok {
			next = suffix
		}
	}
	if next != "messages events message-one --profile work" {
		t.Fatalf("wrong delivery hint: %s", out.String())
	}
	// The copied command must still use work after another profile becomes selected.
	if err := store.Update(context.Background(), func(c *config.Config) error {
		c.Selected = "other"
		c.Profiles["other"] = config.Profile{APIURL: "https://invalid.example"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lookup := newWithStore("test", "public", store)
	lookup.SetArgs(strings.Fields(next))
	out.Reset()
	lookup.SetOut(&out)
	if err := lookup.Execute(); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || sends != 1 || reads != 1 {
		t.Fatalf("sends=%d reads=%d output=%s", sends, reads, &out)
	}
	_, profile, err := store.Resolve("work")
	if err != nil || profile.Project != "project-one" || profile.Route != "route-one" {
		t.Fatal("saved context changed")
	}
}

func TestMessageLookupUsesOnlyExplicitProject(t *testing.T) {
	for _, action := range []string{"get", "events", "content"} {
		for _, savedProject := range []string{"", "unrelated-project"} {
			for _, explicitProject := range []string{"", "message-project", "wrong-project"} {
				t.Run(action+"/saved="+savedProject+"/explicit="+explicitProject, func(t *testing.T) {
					calls := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Query().Get("filter[project]") != explicitProject || r.URL.Query().Has("filter[route_id]") {
							t.Errorf("wrong filters: %s", r.URL)
						}
						if explicitProject == "" && r.URL.Query().Has("filter[project]") {
							t.Error("empty project filter was sent")
						}
						if explicitProject == "wrong-project" {
							w.WriteHeader(404)
							io.WriteString(w, `{"message":"Not found"}`)
							return
						}
						switch action {
						case "events":
							io.WriteString(w, `{"data":[],"links":{"next":null}}`)
						case "get":
							io.WriteString(w, `{"data":{"id":"message-one","status":"delivered"}}`)
						case "content":
							io.WriteString(w, "Exact content\r\n")
						}
					}))
					defer server.Close()
					store := authenticatedStore(t, server.URL)
					if err := store.Update(context.Background(), func(c *config.Config) error {
						p := c.Profiles["work"]
						p.Project = savedProject
						c.Profiles["work"] = p
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					cmd := newWithStore("test", "public", store)
					args := []string{"messages", action, "message-one", "--plain", "--route", "ignored-route"}
					if explicitProject != "" {
						args = append(args, "--project", explicitProject)
					}
					cmd.SetArgs(args)
					var out bytes.Buffer
					cmd.SetOut(&out)
					err := cmd.Execute()
					if explicitProject == "wrong-project" {
						if api.ExitCode(err) != 5 {
							t.Fatalf("expected not found, got %v", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if calls != 1 {
						t.Fatalf("calls=%d", calls)
					}
					if action == "content" && err == nil && out.String() != "Exact content\r\n" {
						t.Fatal(out.String())
					}
					if strings.Contains(out.String(), "Route:") || strings.Contains(out.String(), "unrelated-project") || (explicitProject == "" && strings.Contains(out.String(), "Project:")) {
						t.Fatalf("ignored context displayed: %s", &out)
					}
				})
			}
		}
	}
}

func TestEventsCursorIgnoresSavedContextChanges(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Has("filter[project]") || r.URL.Query().Has("filter[route_id]") {
			t.Errorf("unexpected filters: %s", r.URL)
		}
		if calls == 1 {
			io.WriteString(w, `{"data":[{"event":"message.accepted"}],"links":{"next":"/v1/messages/message-one/events?page%5Bcursor%5D=next&page%5Bsize%5D=1"}}`)
		} else {
			if r.URL.Query().Get("page[cursor]") != "next" || r.URL.Query().Get("page[size]") != "1" {
				t.Errorf("wrong cursor: %s", r.URL)
			}
			io.WriteString(w, `{"data":[{"event":"message.delivered"}],"links":{"next":null}}`)
		}
	}))
	defer server.Close()
	store := authenticatedStore(t, server.URL)
	var cursor string
	for page := 0; page < 2; page++ {
		cmd := newWithStore("test", "public", store)
		args := []string{"messages", "events", "message-one", "--json", "--limit", "1"}
		if page == 1 {
			args = append(args, "--cursor", cursor)
		}
		cmd.SetArgs(args)
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var result struct {
			NextCursor *string             `json:"next_cursor"`
			Data       []map[string]string `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if page == 0 {
			if result.NextCursor == nil {
				t.Fatal("missing next cursor")
			}
			cursor = *result.NextCursor
			if err := store.Update(context.Background(), func(c *config.Config) error {
				p := c.Profiles["work"]
				p.Project = "changed-project"
				p.Route = "changed-route"
				c.Profiles["work"] = p
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		} else if result.NextCursor != nil || result.Data[0]["event"] != "message.delivered" {
			t.Fatal(out.String())
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestSendRejectsInvalidExplicitKeyBeforeAuthentication(t *testing.T) {
	for _, key := range []string{"", strings.Repeat("k", 256), strings.Repeat("é", 128)} {
		cmd := New("test", "public")
		cmd.SetArgs([]string{"messages", "send", "--idempotency-key", key})
		if err := cmd.Execute(); err == nil || err.Error() != "--idempotency-key must contain 1 to 255 bytes" {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestSendFailureKeepsRetryContextAndDoesNotRetry(t *testing.T) {
	for _, key := range []string{"", "order-1042", strings.Repeat("k", 255)} {
		for _, status := range []int{422, 503} {
			t.Run(fmt.Sprintf("key-length=%d/status=%d", len(key), status), func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Header.Get("Idempotency-Key") != key {
						t.Error("key changed")
					}
					if key == "" {
						if _, exists := r.Header["Idempotency-Key"]; exists {
							t.Error("empty header present")
						}
					}
					w.WriteHeader(status)
					io.WriteString(w, `{"message":"Send failed","errors":{"from":["Invalid sender"]}}`)
				}))
				defer server.Close()
				cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
				args := []string{"messages", "send", "--from", "sender@example.com", "--to", "reader@example.net", "--subject", "Order", "--text", "Hello"}
				if key != "" {
					args = append(args, "--idempotency-key", key)
				}
				cmd.SetArgs(args)
				err := cmd.Execute()
				var send *api.SendError
				if !errors.As(err, &send) || send.HasIdempotencyKey != (key != "") || calls != 1 {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
				var out, diagnostics bytes.Buffer
				p := presentation.New(&out, &diagnostics, presentation.Options{Plain: true})
				if renderErr := p.Error(err); renderErr != nil {
					t.Fatal(renderErr)
				}
				if status == 503 {
					expected := "another send can create a duplicate"
					if key != "" {
						expected = "same profile, project, route, input, and idempotency key"
					}
					if !strings.Contains(diagnostics.String(), expected) {
						t.Fatal(diagnostics.String())
					}
				} else if strings.Contains(diagnostics.String(), "retry") {
					t.Fatal("retry advice for validation error")
				}
				diagnostics.Reset()
				if renderErr := presentation.New(&out, &diagnostics, presentation.Options{JSON: true}).Error(err); renderErr != nil {
					t.Fatal(renderErr)
				}
				if !json.Valid(diagnostics.Bytes()) || !strings.Contains(diagnostics.String(), `"from":["Invalid sender"]`) {
					t.Fatal(diagnostics.String())
				}
			})
		}
	}
}

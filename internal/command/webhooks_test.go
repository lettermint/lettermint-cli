package command

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookDeliveryAndSecretRequests(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		method, path string
		query        map[string]string
	}{
		{"regenerate secret", []string{"webhooks", "regenerate-secret", "hook-one", "--yes"}, "POST", "/v1/webhooks/hook-one/regenerate-secret", nil},
		{"deliveries", []string{"webhooks", "deliveries", "hook-one"}, "GET", "/v1/webhooks/hook-one/deliveries", map[string]string{"page[size]": "15"}},
		{"filtered deliveries", []string{"webhooks", "deliveries", "hook-one", "--status", "failed", "--status", "timeout", "--event", "message.delivered", "--from", "2026-09-01", "--to", "2026-09-05"}, "GET", "/v1/webhooks/hook-one/deliveries", map[string]string{"page[size]": "15", "filter[status]": "failed,timeout", "filter[event_type]": "message.delivered", "filter[from_date]": "2026-09-01", "filter[to_date]": "2026-09-05"}},
		{"delivery", []string{"webhooks", "delivery", "hook-one", "delivery-one"}, "GET", "/v1/webhooks/hook-one/deliveries/delivery-one", nil},
		{"retry", []string{"webhooks", "retry", "hook-one", "delivery-one"}, "POST", "/v1/webhooks/hook-one/deliveries/delivery-one/retry", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				// Saved project and route defaults do not apply to a webhook addressed by ID.
				if r.Method != tc.method || r.URL.Path != tc.path || r.Header.Get("Authorization") != "Bearer grant" || len(r.URL.Query()) != len(tc.query) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				for key, value := range tc.query {
					if r.URL.Query().Get(key) != value {
						t.Errorf("query %s: %s", key, r.URL.Query().Get(key))
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"data":[],"next_page_url":null}`)
			}))
			defer server.Close()
			cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
			cmd.SetArgs(append(tc.args, "--json", "--no-input"))
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

func TestRegenerateSecretNeedsConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("secret regenerated without confirmation: %s", r.URL)
	}))
	defer server.Close()
	for _, tc := range []struct {
		args  []string
		input string
		error string
	}{
		{[]string{"--no-input"}, "", "requires --yes"},
		{[]string{"--plain"}, "n\n", "canceled"},
	} {
		cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
		cmd.SetArgs(append([]string{"webhooks", "regenerate-secret", "hook-one"}, tc.args...))
		cmd.SetIn(strings.NewReader(tc.input))
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.error) {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
}

func TestRegeneratedSecretIsShown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"id":"hook-one","name":"Orders","secret":"whsec_new"},"message":"Webhook secret regenerated successfully."}`)
	}))
	defer server.Close()
	cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
	cmd.SetArgs([]string{"webhooks", "regenerate-secret", "hook-one", "--plain"})
	cmd.SetIn(strings.NewReader("y\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Secret: whsec_new") {
		t.Fatal(out.String())
	}
}

func TestDeliveriesContinueWithFilters(t *testing.T) {
	calls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("filter[status]") != "failed" {
			t.Errorf("filter lost on page %d: %s", calls, r.URL)
		}
		if calls == 1 {
			io.WriteString(w, `{"data":[{"id":"delivery-one"}],"next_page_url":"`+server.URL+`/v1/webhooks/hook-one/deliveries?cursor=page-two"}`)
			return
		}
		if r.URL.Query().Get("cursor") != "page-two" {
			t.Errorf("wrong continuation: %s", r.URL)
		}
		io.WriteString(w, `{"data":[{"id":"delivery-two"}],"next_page_url":null}`)
	}))
	defer server.Close()
	run := func(args ...string) map[string]any {
		cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
		cmd.SetArgs(append([]string{"webhooks", "deliveries", "hook-one", "--status", "failed", "--json", "--no-input"}, args...))
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
	cursor, _ := run()["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("missing next cursor")
	}
	if next := run("--cursor", cursor)["next_cursor"]; next != nil || calls != 2 {
		t.Fatalf("next=%v calls=%d", next, calls)
	}
}

func TestDeliveriesRejectInvalidDateBeforeRequest(t *testing.T) {
	cmd := New("test", "")
	cmd.SetArgs([]string{"webhooks", "deliveries", "hook-one", "--from", "05-09-2026", "--no-input"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatal(err)
	}
}

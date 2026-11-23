package presentation

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/lettermint/lettermint-cli/internal/api"
)

func testPresenter(out, diagnostics *bytes.Buffer, options Options, width int) *Presenter {
	terminal := Terminal{TTY: true, Width: width, Profile: colorprofile.TrueColor}
	p := WithTerminals(out, diagnostics, options, terminal, terminal)
	p.now = func() time.Time { return time.Date(2026, 9, 5, 14, 32, 8, 0, time.UTC) }
	p.location = time.UTC
	return p
}

func TestHumanResults(t *testing.T) {
	cases := []struct{ command, payload string }{
		{"projects list", `{"data":[{"id":"proj_01","name":"Café orders","created_at":"2026-09-01T10:00:00Z"}],"next_cursor":"opaque-cursor"}`},
		{"routes list", `{"data":[{"id":"route_01","name":"Receipts","slug":"receipts","route_type":"transactional","is_default":true}]}`},
		{"domains list", `{"data":[{"id":"domain_01","domain":"example.com","status":"verified"}]}`},
		{"webhooks list", `{"data":[{"id":"hook_01","name":"Local test","enabled":false,"url":"https://example.com/hook"}]}`},
		{"messages list", `{"data":[{"id":"msg_01","created_at":"2026-09-05T12:00:00Z","type":"outbound","status":"delivered","to":[{"email":"café@example.net"}],"subject":"Order 1042"}]}`},
		{"messages events", `{"data":[{"timestamp":"2026-09-05T12:00:00Z","event":"message.delivered"}]}`},
		{"listeners list", `{"data":[{"id":"session_01","project_id":"proj_01","expires_at":"2026-09-06T00:00:00Z","stopped_at":null}]}`},
		{"profiles list", `{"selected":"work","profiles":{"work":{"team_id":"team_01","project":"proj_01"},"test":{"team_id":"team_02"}}}`},
		{"skills list", `{"version":"1.0.0","skills":["lettermint-cli"]}`},
		{"projects get", `{"data":{"id":"proj_01","name":"Orders","smtp_enabled":false,"settings":{"tracking":true},"routes":[]}}`},
		{"routes get", `{"data":{"id":"route_01","name":"Receipts","settings":{"click_tracking":false},"inbound_settings":{"enabled":null}}}`},
		{"domains get", `{"data":{"id":"domain_01","domain":"example.com","status":"pending","dns_records":[{"type":"TXT","value":"verification-value"}]}}`},
		{"webhooks get", `{"data":{"id":"hook_01","name":"Local test","secret":"do-not-display","events":["message.delivered"]}}`},
		{"messages get", `{"data":{"id":"msg_01","subject":"Full subject with Unicode: 你好 👩‍💻","to":[{"email":"café@example.net"}],"status":"delivered"}}`},
		{"listeners get", `{"data":{"id":"session_01","project_id":"proj_01","oauth_connection_id":"private-field","secret":"do-not-display","expires_at":"2026-09-05T00:00:00Z","attempts":[{"sequence":12,"local_status":500}]}}`},
		{"auth status", `{"profile":"work","identity":{"team":{"name":"Demo","id":"team_01"},"user":{"email":"user@example.com","id":"user_01"}}}`},
		{"auth status", `{"profile":"work","identity":{"team":{"name":"Demo"}},"permissions":["projects:read","projects:create","messages:read","messages:read_content","messages:send","team_tokens:read"]}`},
		{"auth status", `{"profile":"work","identity":{"team":{"name":"Demo"}},"permissions":[]}`},
		{"auth login", `{"profile":"work","identity":{"team":{"name":"Demo"},"user":{"email":"user@example.com"}}}`},
		{"auth logout", `{"revoked":false,"local_credentials_removed":true}`},
		{"context show", `{"profile":"work","context":{"project":"proj_01","route":null}}`},
		{"context set", `{"project":"proj_01","route":"route_01"}`},
		{"profiles use", `{"profile":"work"}`},
		{"doctor", `{"version":"1.0.0","api":"available","identity":{"team":{"name":"Demo"}}}`},
		{"doctor", `{"version":"1.0.0","api":"available","identity":{"team":{"name":"Demo"}},"capabilities":{"status":"available","api_version":"v2","permission_count":12},"warnings":["API version v2 is not supported by this CLI, which supports v1. Update the CLI."]}`},
		{"doctor", `{"version":"1.0.0","api":"available","identity":{"team":{"name":"Demo"}},"capabilities":{"status":"not available"}}`},
		{"messages send", `{"data":{"message_id":"msg_01","status":"accepted","idempotent_replay":false}}`},
		{"messages resend", `{"message_id":"msg_02","status":"pending"}`},
		{"analytics", `{"data":{"summary":{"metrics":{"delivered":48210,"bounced":312,"bounce_rate":0.006423586090465504,"delivery_latency_p95_ms":1834.5},"rate_bases":{"bounce_rate":{"numerator":312,"denominator":48571}}}},"meta":{"timezone":"UTC","interval":"day","from":"2026-10-13T00:00:00.000000Z","to":"2026-11-12T00:00:00.000000Z","effective_to":"2026-11-11T09:41:07.512000Z","ongoing":true,"partial":false},"pagination":{"total_groups":0,"returned_groups":0,"next_cursor":null,"truncated":false}}`},
		{"analytics", `{"data":{"summary":{"metrics":{"delivered":48210,"bounce_rate":0.0064},"previous":{"metrics":{"delivered":46680,"bounce_rate":0.0062}},"change":{"delivered":{"absolute":1530,"relative":0.0327},"bounce_rate":{"absolute":0.0002,"relative":0.0322,"percentage_points":0.02}}},"time_series":[{"from":"2026-11-10T00:00:00+01:00","to":"2026-11-11T00:00:00+01:00","available":true,"partial":false,"metrics":{"delivered":1874,"bounce_rate":0.0211},"change":{"delivered":{"absolute":null,"relative":null},"bounce_rate":{"absolute":null,"relative":null,"percentage_points":null}}},{"from":"2026-11-11T00:00:00+01:00","to":"2026-11-12T00:00:00+01:00","available":true,"partial":true,"metrics":{"delivered":0,"bounce_rate":null},"change":{"delivered":{"absolute":-12,"relative":null},"bounce_rate":{"absolute":null,"relative":null,"percentage_points":null}}}],"breakdown":[{"dimensions":{"provider_family":"Microsoft","message_type":"transactional"},"metrics":{"delivered":8350,"bounce_rate":0.0833},"change":{"delivered":{"absolute":-50,"relative":-0.006},"bounce_rate":{"absolute":0.001,"relative":0.01,"percentage_points":0.1}},"trend":[]},{"dimensions":{"provider_family":null,"message_type":"broadcast"},"metrics":{"delivered":12,"bounce_rate":null},"change":{"delivered":{"absolute":12,"relative":null},"bounce_rate":{"absolute":null,"relative":null,"percentage_points":null}},"trend":[]}]},"meta":{"timezone":"Europe/Amsterdam","interval":"day","from":"2026-11-09T23:00:00.000000Z","to":"2026-11-11T23:00:00.000000Z","ongoing":false,"partial":true,"available_since":"2026-11-10T00:00:00.000000Z","ranked_group_limit":1000,"comparison":{"from":"2026-11-07T23:00:00.000000Z","to":"2026-11-09T23:00:00.000000Z","partial":false}},"pagination":{"total_groups":1200,"returned_groups":2,"next_cursor":"next-page","truncated":true}}`},
		{"projects create", `{"data":{"id":"proj_01","name":"Orders"}}`},
		{"routes update", `{"data":{"id":"route_01","name":"Receipts"}}`},
		{"domains verify", `{"data":{"verified":false,"status":"pending"}}`},
		{"domains assign", `{"data":{"project_ids":["proj_01"]}}`},
		{"webhooks test", `{"data":{"success":true}}`},
		{"webhooks delete", `{"data":{"deleted":true}}`},
		{"listeners stop", `{"data":{"stopped":true}}`},
		{"listeners replay", `{"data":{"sequence":13,"delivery_id":"delivery_01","attempt":2,"event":"suppression.added"}}`},
		{"listeners secret", `{"data":{"secret":"local-signing-secret"}}`},
		{"webhooks deliveries", `{"data":[{"id":"delivery_01","webhook_id":"hook_01","event_type":"message.delivered","status":"server_error","attempt_number":2,"http_status_code":503,"created_at":"2026-09-05T12:00:00Z"},{"id":"delivery_02","webhook_id":"hook_01","event_type":"message.sent","status":"success","attempt_number":1,"http_status_code":200,"created_at":"2026-09-05T11:00:00Z"}],"next_cursor":null}`},
		{"webhooks delivery", `{"data":{"id":"delivery_01","webhook_id":"hook_01","event_type":"message.delivered","status":"failed","attempt_number":1,"http_status_code":null,"payload_retained":true,"payload":{"event":"message.delivered"},"response_headers":null,"error_message":"Connection refused","timestamp":"2026-09-05T12:00:00Z","next_retry_at":"2026-09-05T12:05:00Z"}}`},
		{"webhooks retry", `{"message":"Retry queued.","delivery_id":"delivery_01"}`},
		{"webhooks regenerate-secret", `{"data":{"id":"hook_01","name":"Orders","enabled":true,"secret":"whsec_new-secret"},"message":"Webhook secret regenerated successfully. Please update your integration."}`},
		{"webhooks create", `{"data":{"id":"hook_01","name":"Orders","secret":"whsec_created-secret"},"message":"Webhook created successfully."}`},
		{"skills export", `{"output":"./Skills café","version":"1.0.0"}`},
		{"version", `{"version":"1.0.0"}`},
		{"projects list", `{"data":[]}`},
	}
	var actual bytes.Buffer
	for _, tc := range cases {
		var out, diagnostics bytes.Buffer
		p := testPresenter(&out, &diagnostics, Options{Plain: true}, 160)
		if err := p.Result(tc.command, json.RawMessage(tc.payload), Context{}); err != nil {
			t.Fatalf("%s: %v", tc.command, err)
		}
		actual.WriteString(">>> " + tc.command + "\n" + out.String() + "\n")
		if strings.Contains(out.String(), "do-not-display") || strings.Contains(out.String(), "private-field") {
			t.Fatal("human output exposed a secret or server storage field")
		}
	}
	checkGolden(t, "testdata/results.golden", actual.Bytes())
}

func TestNarrowTablesAndMissingFields(t *testing.T) {
	id := "4cc9444c-0377-4e3b-a2d0-7554faaf1ea4"
	payload := map[string]any{"data": []any{map[string]any{"id": id, "name": strings.Repeat("你好é👩‍💻", 15)}, map[string]any{"id": "second"}}}
	var out, diagnostics bytes.Buffer
	p := testPresenter(&out, &diagnostics, Options{Plain: true}, 40)
	if err := p.Result("projects list", payload, Context{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ID: "+id) || !strings.Contains(out.String(), "Created: —") || !strings.Contains(out.String(), "Name: —") {
		t.Fatal(out.String())
	}
	out.Reset()
	p.output.Width = 100
	if err := p.Result("projects list", payload, Context{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "…") {
		t.Fatal(out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if lipgloss.Width(line) > 100 {
			t.Fatalf("table exceeds width: %s", line)
		}
	}
}

func TestBannerAndListenerOutput(t *testing.T) {
	var out, diagnostics bytes.Buffer
	p := testPresenter(&out, &diagnostics, Options{Color: "never"}, 100)
	if err := p.Banner("1.0.0", false); err != nil {
		t.Fatal(err)
	}
	p.output.Width = 35
	if err := p.Banner("1.0.0", false); err != nil {
		t.Fatal(err)
	}
	scope := Context{Profile: "work", Project: "proj_01"}
	if err := p.ListenerStarted("session_01", "http://localhost:3000/hook", nil, false, scope); err != nil {
		t.Fatal(err)
	}
	p.ListenerConnection(false)
	p.ListenerConnection(true)
	for _, attempt := range []api.Attempt{
		{Sequence: 12, Status: 200, DurationMS: 42},
		{Sequence: 13, Status: 500, DurationMS: 18, Error: "local_http_500"},
		{Sequence: 14, DurationMS: 30000, Error: "callback timeout\ntry again\x1b[2J"},
	} {
		start := out.Len()
		if err := p.ListenerAttempt(api.Event{Sequence: attempt.Sequence, DeliveryID: "delivery_01", Event: "suppression.added", Attempt: 1}, attempt); err != nil {
			t.Fatal(err)
		}
		if record := out.String()[start:]; strings.Count(record, "\n") != 1 || strings.ContainsAny(record, "\r\x1b") {
			t.Fatalf("attempt is not one safe log line: %q", record)
		}
	}
	checkGolden(t, "testdata/listener.golden", []byte(out.String()+"\nSTDERR\n"+diagnostics.String()))
}

func TestHumanErrorKeepsFieldDetailsAndPermissionBoundary(t *testing.T) {
	var out, diagnostics bytes.Buffer
	p := testPresenter(&out, &diagnostics, Options{Plain: true}, 80)
	failure := &api.Error{Status: 422, Code: "validation_failed", Message: "Invalid input",
		Details: json.RawMessage(`{"from":["Invalid sender"],"metadata.order_id":["Must be a string"]}`)}
	if err := p.Error(failure); err != nil {
		t.Fatal(err)
	}
	if err := p.Error(&api.Error{Status: 403, Code: "permission_denied", Message: "Access denied"}); err != nil {
		t.Fatal(err)
	}
	denied := &api.PermissionError{Err: &api.Error{Status: 403, Code: "permission_denied", Message: "Access denied"}, Permissions: []string{"messages:read", "messages:read_content"}}
	if err := p.Error(denied); err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "testdata/errors.golden", diagnostics.Bytes())
}

func checkGolden(t *testing.T, path string, actual []byte) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, actual, 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("output differs from %s:\n%s", path, actual)
	}
}

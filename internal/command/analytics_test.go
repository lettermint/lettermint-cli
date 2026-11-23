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

const analyticsResponse = `{"data":{"summary":{"metrics":{"delivered":10,"bounce_rate":0.1},"rate_bases":{"bounce_rate":{"numerator":1,"denominator":10}}}},"meta":{"timezone":"UTC","interval":"day"},"pagination":{"total_groups":0,"returned_groups":0,"next_cursor":null,"truncated":false}}`

func TestAnalyticsQueryContract(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		input string
		body  string
	}{
		{
			"flags",
			[]string{"--metric", "delivered,bounce_rate", "--from", "2026-11-01", "--to", "2026-11-07", "--timezone", "Europe/Amsterdam", "--interval", "hour", "--group-by", "provider_family", "--filter", "message_type=transactional", "--filter", "provider_family=Google", "--filter", "provider_family=Microsoft", "--compare", "--trend", "--sort", "bounce_rate:asc", "--limit", "10"},
			"",
			`{"compare":"previous_period","filters":[{"dimension":"message_type","operator":"eq","values":["transactional"]},{"dimension":"provider_family","operator":"in","values":["Google","Microsoft"]},{"dimension":"project_id","operator":"eq","values":["project-one"]},{"dimension":"route_id","operator":"eq","values":["route-one"]}],"from":"2026-11-01","group_by":["provider_family"],"include":["summary","time_series","breakdown"],"include_trend":true,"interval":"hour","limit":10,"metrics":["delivered","bounce_rate"],"sort":{"direction":"asc","metric":"bounce_rate"},"timezone":"Europe/Amsterdam","to":"2026-11-07"}`,
		},
		{
			"minimal flags",
			[]string{"--metric", "delivered", "--sort", "delivered", "--group-by", "smtp_code"},
			"",
			`{"filters":[{"dimension":"project_id","operator":"eq","values":["project-one"]},{"dimension":"route_id","operator":"eq","values":["route-one"]}],"group_by":["smtp_code"],"include":["summary","breakdown"],"metrics":["delivered"],"sort":{"direction":"desc","metric":"delivered"}}`,
		},
		{
			"file with cursor and explicit project",
			[]string{"--file", "-", "--cursor", "next-page", "--project", "project-two"},
			`{"metrics":["delivered"],"limit":5,"filters":[{"dimension":"recipient_domain","operator":"not_in","values":["example.com"]}]}`,
			`{"cursor":"next-page","filters":[{"dimension":"recipient_domain","operator":"not_in","values":["example.com"]},{"dimension":"project_id","operator":"eq","values":["project-two"]}],"limit":5,"metrics":["delivered"]}`,
		},
		{
			"team-wide query",
			[]string{"--file", "-", "--project", ""},
			`{"metrics":["delivered"],"filters":[{"dimension":"project_id","operator":"in","values":["a","b"]}]}`,
			`{"filters":[{"dimension":"project_id","operator":"in","values":["a","b"]}],"metrics":["delivered"]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v1/analytics" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				received, _ := json.Marshal(body)
				if string(received) != tc.body {
					t.Errorf("body:\n%s\nwant:\n%s", received, tc.body)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, analyticsResponse)
			}))
			defer server.Close()
			cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
			cmd.SetArgs(append(append([]string{"analytics"}, tc.args...), "--json", "--no-input"))
			cmd.SetIn(strings.NewReader(tc.input))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one request, got %d", calls)
			}
			var expected, actual any
			_ = json.Unmarshal([]byte(analyticsResponse), &expected)
			if err := json.Unmarshal(out.Bytes(), &actual); err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(actual)
			e, _ := json.Marshal(expected)
			if !bytes.Equal(a, e) {
				t.Fatalf("response changed: %s", out.String())
			}
		})
	}
}

func TestAnalyticsRejectsInvalidInputBeforeAuthentication(t *testing.T) {
	cases := []struct {
		name, input, message string
		args                 []string
	}{
		{"mixed input", `{"metrics":["delivered"]}`, "cannot be combined", []string{"--file", "-", "--metric", "delivered"}},
		{"no metric", "", "select at least one metric", nil},
		{"file without metrics", `{"from":"2026-11-01"}`, "metrics array", []string{"--file", "-"}},
		{"from without to", "", "used together", []string{"--metric", "delivered", "--from", "2026-11-01"}},
		{"mixed window", "", "both be dates", []string{"--metric", "delivered", "--from", "2026-11-01", "--to", "2026-11-07T00:00:00Z"}},
		{"reversed window", "", "after --from", []string{"--metric", "delivered", "--from", "2026-11-07", "--to", "2026-11-01"}},
		{"interval", "", "hour, day, or week", []string{"--metric", "delivered", "--interval", "month"}},
		{"duplicate metric", "", "more than once", []string{"--metric", "delivered,delivered"}},
		{"group limit", "", "at most 3", []string{"--metric", "delivered", "--group-by", "a,b,c,d"}},
		{"trend without group", "", "--trend requires --group-by", []string{"--metric", "delivered", "--trend"}},
		{"sort metric", "", "must name a metric", []string{"--metric", "delivered", "--group-by", "provider_family", "--sort", "bounced"}},
		{"sort direction", "", "asc or desc", []string{"--metric", "delivered", "--group-by", "provider_family", "--sort", "delivered:up"}},
		{"limit", "", "between 1 and 200", []string{"--metric", "delivered", "--group-by", "provider_family", "--limit", "201"}},
		{"filter", "", "DIMENSION=VALUE", []string{"--metric", "delivered", "--filter", "provider_family"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := New("test", "")
			cmd.SetArgs(append(append([]string{"analytics"}, tc.args...), "--profile", "missing", "--json", "--no-input"))
			cmd.SetIn(strings.NewReader(tc.input))
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
}

func TestAnalyticsRejectsContextAndQueryProjectFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL)
	}))
	defer server.Close()
	cmd := newWithStore("test", "public", authenticatedStore(t, server.URL))
	cmd.SetArgs([]string{"analytics", "--file", "-", "--json", "--no-input"})
	cmd.SetIn(strings.NewReader(`{"metrics":["delivered"],"filters":[{"dimension":"project_id","operator":"eq","values":["other"]}]}`))
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `--project ""`) {
		t.Fatal(err)
	}
}

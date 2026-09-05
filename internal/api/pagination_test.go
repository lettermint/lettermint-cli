package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestContinuationKeepsProjectAndPageSize(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("page[size]") != "2" || r.URL.Query().Get("filter[project]") != "project-one" {
			t.Error("lost pagination context")
		}
		if calls == 1 {
			_, _ = w.Write([]byte(`{"data":[{"id":"one"}],"links":{"next":"/v1/messages?page%5Bcursor%5D=next-value"}}`))
		} else {
			if r.URL.Query().Get("page[cursor]") != "next-value" {
				t.Error("lost page cursor")
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"two"}],"links":{"next":null}}`))
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: Transport(), Token: func(context.Context) (string, error) { return "grant", nil }}
	query := url.Values{"filter[project]": {"project-one"}}
	first, err := client.List(context.Background(), "/v1/messages", query, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	page, err := Decode[Page[map[string]string]](first)
	if err != nil || page.NextCursor == nil {
		t.Fatalf("%s: %v", first, err)
	}
	if _, err = client.List(context.Background(), "/v1/messages", url.Values{"filter[project]": {"project-two"}}, 2, *page.NextCursor); err == nil {
		t.Fatal("accepted another project")
	}
	second, err := client.List(context.Background(), "/v1/messages", query, 30, *page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := Decode[Page[map[string]string]](second)
	if calls != 2 || result.NextCursor != nil || result.Data[0]["id"] != "two" {
		t.Fatalf("%s", second)
	}
}

func TestContinuationRejectsCredentialAndDestinationChanges(t *testing.T) {
	for _, next := range []string{"https://other.example/v1/messages?page[cursor]=next", "/v1/identity?page[cursor]=next", "/v1/messages?filter[project]=hidden&page[cursor]=next", "/v1/messages?page[unknown]=x", "/v1/messages?page[size]=10"} {
		t.Run(next, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "links": map[string]string{"next": next}})
			}))
			defer server.Close()
			client := Client{BaseURL: server.URL, HTTP: Transport(), Token: func(context.Context) (string, error) { return "grant", nil }}
			if _, err := client.List(context.Background(), "/v1/messages", nil, 30, ""); err == nil {
				t.Fatal("unsafe continuation accepted")
			}
		})
	}
	client := Client{BaseURL: "https://api.example", HTTP: Transport(), Token: func(context.Context) (string, error) {
		t.Fatal("invalid cursor reached authentication")
		return "", nil
	}}
	bytes, _ := json.Marshal(continuation{1, client.BaseURL, "/v1/messages", "", url.Values{"Authorization": {"injected"}}})
	if _, err := client.List(context.Background(), "/v1/messages", nil, 30, base64.RawURLEncoding.EncodeToString(bytes)); err == nil {
		t.Fatal("invalid parameters accepted")
	}
}

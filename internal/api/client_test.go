package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDoesNotFollowRedirectWithBearer(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer")
		}
		w.Header().Set("Location", "/another")
		w.WriteHeader(307)
	}))
	defer s.Close()
	c := Client{BaseURL: s.URL, HTTP: Transport(), Token: func(context.Context) (string, error) { return "secret", nil }}
	_, err := c.Do(context.Background(), "GET", "/v1/identity", nil, nil, nil)
	if calls != 1 || err == nil {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestPermissionErrorKeepsStableCode(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"permission_denied","message":"No access"}}`))
	}))
	defer s.Close()
	c := Client{BaseURL: s.URL, HTTP: Transport(), Token: func(context.Context) (string, error) { return "secret", nil }}
	_, err := c.Do(context.Background(), "GET", "/v1/projects", nil, nil, nil)
	var remote *Error
	if !errors.As(err, &remote) || remote.Code != "permission_denied" || ExitCode(err) != 4 {
		t.Fatal(err)
	}
}
